package plugins

import (
	"github.com/reddec/monexec/pool"
	"io"
	"context"
)

//  factories of plugins
//  K:插件名 V:插件的工厂方法
var plugins = make(map[string]func(fileName string) PluginConfigNG)

//  Register one plugin factory. File name not for parsing!
//  注册插件的工厂方法
//  当程序启动时，调用所有插件的init方法注册
func registerPlugin(name string, factory func(fileName string) PluginConfigNG) {
	plugins[name] = factory
}

//  Build but not fill one config
//  按照插件工厂方法注册插件
func BuildPlugin(name string, file string) (PluginConfigNG, bool) {
	if pluginFac, ok := plugins[name]; ok {
		return pluginFac(file), true
	}
	return nil, false
}

// Base interface for any future plugins
type PluginConfigNG interface {
	// Must handle events
	pool.EventHandler
	// Closable
	io.Closer
	// Merge change from other instance. Other is always has same type as original
	MergeFrom(other interface{}) error
	// Prepare internal state
	Prepare(ctx context.Context, pl *pool.Pool) error
}
