package main

import (
	"gopkg.in/alecthomas/kingpin.v2"
	"gopkg.in/yaml.v2"
	"os"
	"github.com/reddec/container"
	"context"
	"log"
	"github.com/reddec/monexec/monexec"
	"os/signal"
	"syscall"
)

var (
	runCommand      = kingpin.Command("run", "Run single executable")
	runGenerate     = runCommand.Flag("generate", "Generate instead of run YAML configuration based on args").Bool()
	runBin          = runCommand.Arg("command", "Path to executable").Required().String()
	runArgs         = runCommand.Arg("args", "Arguments to executable").Strings()
	runRestartCount = runCommand.Flag("restart-count", "Restart count (negative means infinity)").Short('r').Default("-1").Int()
	runRestartDelay = runCommand.Flag("restart-delay", "Delay before restart").Short('d').Default("5s").Duration()
	runStopTimeout  = runCommand.Flag("graceful-timeout", "Timeout for graceful shutdown").Short('g').Default("5s").Duration()
	runLabel        = runCommand.Flag("label", "Label name for executable. Default - autogenerated").Short('l').String()
	runWorkDir      = runCommand.Flag("workdir", "Workdir for executable").Short('w').String()
	runEnv          = runCommand.Flag("env", "Environment addition variables").Short('e').StringMap()

	runConsulEnable    = runCommand.Flag("consul", "Enable consul integration").Bool()
	runConsulAddress   = runCommand.Flag("consul-address", "Consul address").Default("http://localhost:8500").String()
	runConsulPermanent = runCommand.Flag("consul-permanent", "Keep service in consul auto timeout").Bool()
	runConsulTTL       = runCommand.Flag("consul-ttl", "Keep-alive TTL for services").Default("3s").Duration()
	runConsulDeRegTTL  = runCommand.Flag("consul-unreg", "Timeout after for auto de-registration").Default("1m").Duration()
)

var (
	startCommand = kingpin.Command("start", "Start supervisor from configuration files")
	startSources = startCommand.Arg("source", "Source files and/or directories with YAML files (.yml or .yaml)").Required().Strings()
)

func run() {
	config := DefaultConfig()

	config.Services = append(config.Services, monexec.Executable{
		Name:           *runLabel,
		Command:        *runBin,
		Args:           *runArgs,
		RestartTimeout: *runRestartDelay,
		Restart:        *runRestartCount,
		StopTimeout:    *runStopTimeout,
		WorkDir:        *runWorkDir,
		Environment:    *runEnv,
	})
	FillDefaultExecutable(&config.Services[0])

	if *runConsulEnable {
		config.Consul = DefaultConsulConfig()
		config.Consul.URL = *runConsulAddress
		config.Consul.TTL = *runConsulTTL
		config.Consul.AutoDeregistrationTimeout = *runConsulDeRegTTL
		if *runConsulPermanent {
			config.Consul.Permanent = append(config.Consul.Permanent, config.Services[0].Name)
		} else {
			config.Consul.Dynamic = append(config.Consul.Dynamic, config.Services[0].Name)
		}
	}

	if *runGenerate {
		data, err := yaml.Marshal(config)
		if err != nil {
			panic(err)
		}
		os.Stdout.Write(data)
	} else {
		sv := container.NewSupervisor(log.New(os.Stderr, "[supervisor] ", log.LstdFlags))
		runConfigInSupervisor(&config, sv)
	}
}

func start() {
	config, err := LoadConfig(*startSources...)
	if err != nil {
		log.Fatal(err)
	}
	sv := container.NewSupervisor(log.New(os.Stderr, "[supervisor] ", log.LstdFlags))
	runConfigInSupervisor(config, sv)
}

func runConfigInSupervisor(config *Config, sv container.Supervisor) {
	ctx, stop := context.WithCancel(context.Background())

	c := make(chan os.Signal, 2)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for range c {
			stop()
			break
		}
	}()

	err := config.Run(sv, ctx)

	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	kingpin.Version("0.1.0").DefaultEnvars()
	switch kingpin.Parse() {
	case "run":
		run()
	case "start":
		start()
	}
}
