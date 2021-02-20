package pool

import (
	"context"
	log "github.com/sirupsen/logrus"
	"sync"
)

type Instance interface {
	Stop()
	Config() *Executable
	Supervisor() Supervisor
	Pool() *Pool
}

type Supervisor interface {
	Start(ctx context.Context, pool *Pool) Instance
	Config() *Executable
}

type EventHandler interface {
	OnSpawned(ctx context.Context, in Instance)
	OnStarted(ctx context.Context, in Instance)
	OnStopped(ctx context.Context, in Instance, err error)
	OnFinished(ctx context.Context, in Instance)
}

//  状态池
//  实现EventHandler接口
type Pool struct {
	handlers     []EventHandler
	handlersLock sync.RWMutex

	supervisors []Supervisor
	svLock      sync.RWMutex

	instances []Instance
	inLock    sync.RWMutex

	doneInit sync.Once
	done     chan struct{}

	terminating bool
}

// 全局信道 传递需要新启动的服务
var NewServChan = make(chan Supervisor)

// 全局信道 控制是否需要退出监听新服务的协程
var HotReloadCloseChan = make(chan bool)

func (p *Pool) StopAll() {
	wg := sync.WaitGroup{}
	for _, sv := range p.grabInstances() {
		wg.Add(1)
		go func(sv Instance) {
			defer wg.Done()
			p.Stop(sv)
		}(sv)
	}
	wg.Wait()
}

//启动Pool里所有的Supervisor
func (p *Pool) StartAll(ctx context.Context) {
	if p.terminating {
		return
	}
	wg := sync.WaitGroup{}
	for _, sv := range p.Supervisors() {
		wg.Add(1)
		go func(sv Supervisor) {
			defer wg.Done()
			p.Start(ctx, sv)
		}(sv)
	}

	// 启动新Goroutine监听是否有新服务需要启动，并不需要关心是否启用了热重载，只需关心接收到退出信号退出协程
	go p.startListenNewService(ctx, NewServChan, &wg)

	wg.Wait()
}

// 监听是否有新服务需要启动，并在原Pool和WaitGroup中启动新服务
func (p *Pool) startListenNewService(ctx context.Context, c <-chan Supervisor, wg *sync.WaitGroup) {
	for {
		select {
		case <-HotReloadCloseChan:
			log.Infoln("New service running goroutine exit...")
			return
		case sv := <-c:
			wg.Add(1)
			go func(sv Supervisor) {
				defer wg.Done()
				p.Start(ctx, sv)
				log.Infoln("---> 服务", sv.Config().Name, "就绪 <---")
			}(sv)
		}
	}
}

//启动Pool里的一个Supervisor
func (p *Pool) Start(ctx context.Context, sv Supervisor) Instance {
	if p.terminating {
		return nil
	}

	ins := sv.Start(ctx, p)
	p.inLock.Lock()
	p.instances = append(p.instances, ins)
	p.inLock.Unlock()
	return ins
}

func (p *Pool) Stop(in Instance) {
	in.Stop()
	p.inLock.Lock()
	for i, v := range p.instances {
		if v == in {
			p.instances = append(p.instances[:i], p.instances[i+1:]...)
			break
		}
	}
	p.inLock.Unlock()
}

// 往Pool的supervisors即[]Supervisor中添加新增的Supervisor
func (p *Pool) Add(sv Supervisor) {
	if p.terminating {
		return
	}
	p.svLock.Lock()
	defer p.svLock.Unlock()
	p.supervisors = append(p.supervisors, sv)
}

//  往Pool的handlers即[]EventHandler中添加新增的handler
func (p *Pool) Watch(handler EventHandler) {
	p.handlersLock.Lock()
	defer p.handlersLock.Unlock()
	p.handlers = append(p.handlers, handler)
}

//复制Pool中所有的Handler并返回对应切片
func (p *Pool) cloneHandlers() []EventHandler {
	p.handlersLock.RLock()
	var dest = make([]EventHandler, len(p.handlers))
	copy(dest, p.handlers)
	p.handlersLock.RUnlock()
	return dest
}

//复制并返回Pool中的supervisors
func (p *Pool) Supervisors() []Supervisor {
	p.svLock.RLock()
	var dest = make([]Supervisor, len(p.supervisors))
	copy(dest, p.supervisors)
	p.svLock.RUnlock()
	return dest
}

func (p *Pool) Instances() []Instance {
	p.inLock.RLock()
	var dest = make([]Instance, len(p.instances))
	copy(dest, p.instances)
	p.inLock.RUnlock()
	return dest
}

func (p *Pool) grabInstances() []Instance {
	p.inLock.Lock()
	var dest = p.instances
	p.instances = nil
	p.inLock.Unlock()
	return dest
}

//调用Pool中所有的Handler即Plugin的OnSpawned方法
func (p *Pool) OnSpawned(ctx context.Context, sv Instance) {
	for _, handler := range p.cloneHandlers() {
		handler.OnSpawned(ctx, sv)
	}
}

//调用Pool中所有的Handler即Plugin的OnStarted方法
func (p *Pool) OnStarted(ctx context.Context, sv Instance) {
	for _, handler := range p.cloneHandlers() {
		handler.OnStarted(ctx, sv)
	}
}

//调用Pool中所有的Handler即Plugin的OnStopped方法
func (p *Pool) OnStopped(ctx context.Context, sv Instance, err error) {
	for _, handler := range p.cloneHandlers() {
		handler.OnStopped(ctx, sv, err)
	}
}

//调用Pool中所有的Handler即Plugin的OnFinished方法
func (p *Pool) OnFinished(ctx context.Context, sv Instance) {
	for _, handler := range p.cloneHandlers() {
		handler.OnFinished(ctx, sv)
	}
}

func (p *Pool) doneChan() chan struct{} {
	p.doneInit.Do(func() {
		p.done = make(chan struct{}, 1)
	})
	return p.done
}

func (p *Pool) notifyDone() {
	close(p.doneChan())
}

func (p *Pool) Done() <-chan struct{} {
	return p.doneChan()
}

func (p *Pool) Terminate() {
	if p.terminating {
		return
	}
	p.terminating = true
	p.StopAll()
	p.notifyDone()
}
