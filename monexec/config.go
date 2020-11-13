package monexec

import (
	"context"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"errors"
	"github.com/Pallinder/go-randomdata"
	"github.com/mitchellh/mapstructure"
	"github.com/reddec/monexec/plugins"
	"github.com/reddec/monexec/pool"
	"gopkg.in/yaml.v2"
	"reflect"
)

type Config struct {
	Services      []pool.Executable                 `yaml:"services"`
	Plugins       map[string]interface{}            `yaml:",inline"` // all unparsed means plugins
	loadedPlugins map[string]plugins.PluginConfigNG `yaml:"-"`
}

var (
	globalConfig *Config          //保存全局Config 为了hot_reload
	globalPool   *pool.Pool       //保存全局Pool 为了hot_reload
	globalCtx    *context.Context //保存全局Context
)

//合并配置文件
func (config *Config) mergeFrom(other *Config) error {
	config.mergeServicesFrom(other)
	config.mergePluginsFrom(other)
	return nil
}

// 合并服务
func (config *Config) mergeServicesFrom(other *Config) error {
	config.Services = append(config.Services, other.Services...)
	return nil
}

// 合并插件
func (config *Config) mergePluginsFrom(other *Config) error {
	for otherPluginName, otherPluginInstance := range other.loadedPlugins {
		if ownPlugin, needMerge := config.loadedPlugins[otherPluginName]; needMerge {
			//如果有相同的插件，则调用各插件自己的MergeFrom方法进行合并
			err := ownPlugin.MergeFrom(otherPluginInstance)
			if err != nil {
				return errors.New("merge " + otherPluginName + ": " + err.Error())
			}
		} else { // new one - just copy
			config.loadedPlugins[otherPluginName] = otherPluginInstance
		}
	}
	return nil
}

func (config *Config) ClosePlugins() {
	for _, plugin := range config.loadedPlugins {
		plugin.Close()
	}
}

//生成一个空Config，并初始化loadedPlugins为空映射
func DefaultConfig() Config {
	config := Config{}

	config.loadedPlugins = make(map[string]plugins.PluginConfigNG)
	return config
}

func FillDefaultExecutable(exec *pool.Executable) {
	if exec.RestartTimeout == 0 {
		exec.RestartTimeout = 6 * time.Second
	}
	if exec.Restart == 0 {
		exec.Restart = -1
	}
	if exec.StopTimeout == 0 {
		exec.StopTimeout = 3 * time.Second
	}
	if exec.Name == "" {
		exec.Name = randomdata.Noun() + "-" + randomdata.Adjective()
	}
}

//执行config 即Plugin
func (config *Config) Run(p *pool.Pool, ctx context.Context) error {
	// Initialize plugins
	//-- prepare and add all plugins
	for pluginName, pluginInstance := range config.loadedPlugins {
		err := pluginInstance.Prepare(ctx, p)
		if err != nil {
			log.Println("failed prepare plugin", pluginName, "-", err)
		} else {
			log.Println("plugin", pluginName, "ready")
			p.Watch(pluginInstance)
		}
	}

	//如果存在globalConfig则证明需要热重载，另存储一份当前状态池Pool和Context
	if globalConfig != nil {
		p.EnableHotReload()
		globalPool = p
		globalCtx = &ctx
	}

	// Run
	for i := range config.Services {
		exec := config.Services[i]
		FillDefaultExecutable(&exec)
		p.Add(&exec)
	}

	p.StartAll(ctx)
	return nil
}

//LoadConfig读取一个或多个配置文件
func LoadConfig(locations ...string) (*Config, error) {
	c := DefaultConfig()
	aggregationConfig := &c
	var files []os.FileInfo
	var reloadLocation string
	var reloadFile os.FileInfo

	for _, location := range locations {
		if stat, err := os.Stat(location); err != nil {
			return nil, err
		} else if stat.IsDir() {
			fs, err := ioutil.ReadDir(location)
			if err != nil {
				return nil, err
			}
			files = fs
		} else {
			location = filepath.Dir(location)
			files = []os.FileInfo{stat}
		}

		for _, info := range files {
			if strings.HasSuffix(info.Name(), ".yml") || strings.HasSuffix(info.Name(), ".yaml") {
				if len(locations) == 1 && len(files) == 1 {
					reloadLocation = location
					reloadFile = info
				}
				fileName := path.Join(location, info.Name())
				data, err := ioutil.ReadFile(fileName)
				if err != nil {
					return nil, err
				}
				var conf = DefaultConfig()
				err = yaml.Unmarshal(data, &conf)
				if err != nil {
					return nil, err
				}

				// -- load all plugins for current config here
				conf.loadAllPlugins(fileName)

				err = aggregationConfig.mergeFrom(&conf)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	//只有单一文件才进行配置文件热重载
	if reloadFile != nil {
		for k, v := range aggregationConfig.loadedPlugins {
			if k == "assist" {
				a := v.(*plugins.Assist) // Assist指针才实现了PluginConfigNG接口
				if a.HotReload {         //如果配置中启动了热重载
					globalConfig = aggregationConfig
					initConfig(reloadLocation, reloadFile.Name())
				}
			}
		}
	}

	return aggregationConfig, nil
}

//  load all plugins for current config
//  读取当前配置文件中的所有插件,并将配置中参数映射到插件实例即PluginConfigNG对象
func (config *Config) loadAllPlugins(fileName string) {
	for pluginName, description := range config.Plugins {
		pluginInstance, found := plugins.BuildPlugin(pluginName, fileName)
		if !found {
			log.Println("plugin", pluginName, "not found")
			continue
		}

		var wrap = description
		refVal := reflect.ValueOf(wrap)
		if wrap != nil && refVal.Type().Kind() == reflect.Slice {
			wrap = map[string]interface{}{
				"<ITEMS>": description,
			}
		}

		c := &mapstructure.DecoderConfig{
			Metadata:   nil,
			Result:     pluginInstance,
			DecodeHook: mapstructure.StringToTimeDurationHookFunc(),
		}

		decoder, err := mapstructure.NewDecoder(c)
		if err != nil {
			panic(err) // failed to initialize decoder - something really wrong
		}

		//解析
		err = decoder.Decode(wrap)
		if err != nil {
			log.Println("failed load plugin", pluginName, "-", err)
			continue
		}
		config.loadedPlugins[pluginName] = pluginInstance
	}
}
