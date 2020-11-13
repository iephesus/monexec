package pool

import (
	"context"
	"sync"
	"time"
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
	isHotReload bool //是否启用热重载
}

// 全局信道 传递需要新启动的服务
var NewServChan = make(chan Supervisor)

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

	if p.isHotReload {
		// 启动新Goroutine监听是否有新服务需要启动
		go StartNewServ(NewServChan, p, &wg, ctx)
	}

	wg.Wait()
}

//在原Pool和WaitGroup中启动新服务
func StartNewServ(c chan Supervisor, p *Pool, wg *sync.WaitGroup, ctx context.Context) {
	for sv := range c {
		wg.Add(1)
		go func(sv Supervisor) {
			defer wg.Done()
			p.Start(ctx, sv)
		}(sv)

		//10秒轮询一次，防止占用过多资源
		time.Sleep(10 * time.Second)
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

func (p *Pool) Add(sv Supervisor) {
	if p.terminating {
		return
	}
	p.svLock.Lock()
	defer p.svLock.Unlock()
	p.supervisors = append(p.supervisors, sv)
}

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

func (p *Pool) EnableHotReload() {
	p.isHotReload = true
}

func (p *Pool) DisableHotReload() {
	p.isHotReload = false
}
