package pool

import (
	"context"
	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//  Executable - basic information about process.
//  实现了Supervisor接口
type Executable struct {
	Name           string            `yaml:"label,omitempty"`         // Human-readable label for process. If not set - command used
	Command        string            `yaml:"command"`                 // Executable
	Args           []string          `yaml:"args,omitempty"`          // Arguments to command
	Environment    map[string]string `yaml:"environment,omitempty"`   // Additional environment variables
	EnvFiles       []string          `yaml:"envFiles"`                // Additional environment variables from files (not found files ignored). Format key=value
	WorkDir        string            `yaml:"workdir,omitempty"`       // Working directory. If not set - current dir used
	StopTimeout    time.Duration     `yaml:"stop_timeout,omitempty"`  // Timeout before terminate process
	RestartTimeout time.Duration     `yaml:"restart_delay,omitempty"` // Restart delay
	Restart        int               `yaml:"restart,omitempty"`       // How much restart allowed. -1 infinite
	LogFile        string            `yaml:"logFile,omitempty"`       // if empty - only to log. If not absolute - relative to workdir
	RawOutput      bool              `yaml:"raw,omitempty"`           // print stdout as-is without prefixes

	log        *log.Logger
	loggerInit sync.Once
}

func (exe *Executable) WithName(name string) *Executable {
	cp := *exe
	cp.loggerInit = sync.Once{}
	cp.Name = name
	return &cp
}

// Arg adds additional positional argument
func (exe *Executable) Arg(arg string) *Executable {
	exe.Args = append(exe.Args, arg)
	return exe
}

// Env adds additional environment key-value pair
func (exe *Executable) Env(arg, value string) *Executable {
	if exe.Environment == nil {
		exe.Environment = make(map[string]string)
	}
	exe.Environment[arg] = value
	return exe
}

//获取Executable中绑定的logger 即$exe.log
func (exe *Executable) logger() *log.Logger {
	exe.loggerInit.Do(func() {
		exe.log = log.New(os.Stderr, "["+exe.Name+"] ", log.LstdFlags)
	})
	return exe.log
}

// try to do graceful process termination by sending SIGKILL. If no response after StopTimeout
// SIGTERM is used
func (exe *Executable) stopOrKill(cmd *exec.Cmd, res <-chan error) error {
	exe.logger().Println("Sending SIGINT")
	err := cmd.Process.Signal(os.Interrupt)
	if err != nil {
		exe.logger().Println("Failed send SIGINT:", err)
	}

	select {
	case err = <-res:
		exe.logger().Println("Process graceful stopped")
	case <-time.After(exe.StopTimeout):
		exe.logger().Println("Process graceful shutdown waiting timeout")
		err = kill(cmd, exe.logger())
	}
	return err
}

//  run once executable, wrap output and wait for finish
//  运行一次executable即Supervisor 包装输出并等待执行完成
func (exe *Executable) run(ctx context.Context) error {
	cmd := exec.Command(exe.Command, exe.Args...)
	for _, param := range os.Environ() {
		cmd.Env = append(cmd.Env, param)
	}
	if exe.Environment != nil {
		for k, v := range exe.Environment {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	for _, fileName := range exe.EnvFiles {
		params, err := ParseEnvironmentFile(fileName)
		if err != nil {
			exe.logger().Println("failed parse environment file", fileName, ":", err)
			continue
		}
		for k, v := range params {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	if exe.WorkDir != "" {
		cmd.Dir = exe.WorkDir
	}

	setAttrs(cmd)

	var outputs []io.Writer
	var stderr []io.Writer
	var stdout []io.Writer

	output := NewLoggerStream(exe.logger(), "|ServiceOut  ▶▶▶| ")
	outputs = append(outputs, output)
	defer output.Close()
	stderr = outputs
	stdout = outputs

	if exe.RawOutput {
		stdout = append(stdout, os.Stdout)
	}

	res := make(chan error, 1)

	if exe.LogFile != "" {
		pth, _ := filepath.Abs(exe.LogFile)
		if pth != exe.LogFile {
			// relative
			wd, _ := filepath.Abs(exe.WorkDir)
			exe.LogFile = filepath.Join(wd, exe.LogFile)
		}
		// 处理linux下目录不存在则的情况
		//fileDirPath := filepath.Dir(exe.LogFile)
		//if _, err := os.Stat(fileDirPath); os.IsNotExist(err) {
		//	os.MkdirAll(fileDirPath, 0666)
		//}
		// 创建文件前需保证所有层级的目录都存在
		//logFile, err := os.OpenFile(exe.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0666)

		// 日志切割
		var logNameSlice []string
		if strings.HasSuffix(exe.LogFile, ".log") {
			logNameSlice = strings.Split(exe.LogFile, ".")
			if len(logNameSlice) > 2 {
				logNameSlice[1] = "log"
			}
		} else {
			logNameSlice[0] = exe.LogFile
			logNameSlice[1] = "log"
		}
		logFileOut, errOut := rotatelogs.New(
			logNameSlice[0]+".%F."+logNameSlice[1],
			rotatelogs.WithLinkName(logNameSlice[0]+".log"),
			//rotatelogs.WithMaxAge(7*24*time.Hour),     // 1周
			//rotatelogs.WithRotationTime(24*time.Hour), // 1天
			rotatelogs.WithRotationSize(1024*1024*10), // 最大10M
			rotatelogs.WithRotationCount(20),          // 最多20个文件
		)
		if errOut != nil {
			exe.logger().Println("Failed open stdout log file: ", errOut)
		}
		logFileErr, errErr := rotatelogs.New(
			logNameSlice[0]+"_err.%F."+logNameSlice[1],
			rotatelogs.WithLinkName(logNameSlice[0]+"_err.log"),
			//rotatelogs.WithMaxAge(7*24*time.Hour),     // 1周
			//rotatelogs.WithRotationTime(24*time.Hour), // 1天
			rotatelogs.WithRotationSize(1024*1024*10), // 最大10M
			rotatelogs.WithRotationCount(20),          // 最多20个文件
		)
		if errErr != nil {
			exe.logger().Println("Failed open stderr log file: ", errErr)
		}
		defer logFileOut.Close()
		defer logFileErr.Close()
		outputs = append(outputs, logFileOut, logFileErr)
		//支持将服务的日志输出到文件
		stdout = append(stdout, logFileOut)
		stderr = append(stderr, logFileErr)
	}

	logStderrStream := io.MultiWriter(stderr...)
	logStdoutStream := io.MultiWriter(stdout...)

	cmd.Stderr = logStderrStream
	cmd.Stdout = logStdoutStream

	err := cmd.Start()
	if err == nil {
		exe.logger().Println("Started with PID", cmd.Process.Pid)
	} else {
		exe.logger().Println("Failed start `", exe.Command, strings.Join(exe.Args, " "), "` :", err)
	}

	go func() { res <- cmd.Wait() }()
	select {
	case <-ctx.Done():
		err = exe.stopOrKill(cmd, res)
	case err = <-res:
	}
	return err
}

//实现了Instance接口
type runnable struct {
	Executable *Executable `json:"config"`
	Running    bool        `json:"running"`
	pool       *Pool
	closer     func()
	done       chan struct{}
}

//  启动Executable 即Supervisor
//  返回Instance 即runnable
func (exe *Executable) Start(ctx context.Context, pool *Pool) Instance {
	chCtx, closer := context.WithCancel(ctx)
	run := &runnable{
		Executable: exe,
		closer:     closer,
		done:       make(chan struct{}),
		pool:       pool,
	}
	go run.run(chCtx)
	return run
}

func (exe *Executable) Config() *Executable { return exe }

//运行runnable中的Executable
func (rn *runnable) run(ctx context.Context) {
	defer rn.closer()
	defer close(rn.done)
	restarts := rn.Executable.Restart
	rn.pool.OnSpawned(ctx, rn)
LOOP:
	for {
		rn.Running = true
		rn.pool.OnStarted(ctx, rn)
		err := rn.Executable.run(ctx) //执行Executable
		if err != nil {
			rn.Executable.logger().Println("stopped with error:", err)
		} else {
			rn.Executable.logger().Println("stopped")
		}
		rn.Running = false
		rn.pool.OnStopped(ctx, rn, err)
		if restarts != -1 {
			if restarts <= 0 {
				rn.Executable.logger().Println("max restarts attempts reached")
				break
			} else {
				restarts--
			}
		}
		rn.Executable.logger().Println("waiting", rn.Executable.RestartTimeout)
		select {
		case <-time.After(rn.Executable.RestartTimeout):
		case <-ctx.Done():
			rn.Executable.logger().Println("instance done:", ctx.Err())
			break LOOP
		}
	}
	rn.Executable.logger().Println("instance restart loop done")
	rn.pool.OnFinished(ctx, rn)
}

func (rn *runnable) Supervisor() Supervisor { return rn.Executable }

func (rn *runnable) Config() *Executable { return rn.Executable }

func (rn *runnable) Pool() *Pool { return rn.pool }

func (rn *runnable) Stop() {
	rn.closer()
	<-rn.done
}
