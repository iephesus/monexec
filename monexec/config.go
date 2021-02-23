package monexec

import (
	"context"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"errors"
	"github.com/Pallinder/go-randomdata"
	"github.com/mitchellh/mapstructure"
	"github.com/reddec/monexec/plugins"
	"github.com/reddec/monexec/pool"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	"reflect"
)

type Config struct {
	Services      []pool.Executable                 `yaml:"services"`
	Plugins       map[string]interface{}            `yaml:",inline"` // all unparsed means plugins
	loadedPlugins map[string]plugins.PluginConfigNG `yaml:"-"`
}

var (
	globalConfig *Config //保存全局Config 为了config_reload
	gLock       sync.Mutex
	globalPool   *pool.Pool       //保存全局Pool 为了config_reload
	globalCtx    *context.Context //保存全局Context
)

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

// 执行Config 即加载Plugin 运行Executable
func (config *Config) Run(ctx context.Context, p *pool.Pool) error {
	// 初始化插件
	// 准备并添加所有插件
	for pluginName, pluginInstance := range config.loadedPlugins {
		err := pluginInstance.Prepare(ctx, p)
		if err != nil {
			log.Infoln("Failed prepare plugin", pluginName, "-", err)
		} else {
			log.Infof("Plugin [%v] ready", pluginName)
			p.Watch(pluginInstance)
		}
	}

	//如果GlobalConfig存在则证明启用了热重载，另存储一份当前运行池Pool和Context
	if globalConfig != nil {
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
	// 合并后的总配置文件
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
				//TODO 暂时的逻辑，启用热重载前必须先判断是否是单一配置文件，目前只有单一配置文件才能做热重载
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

				err = aggregationConfig.mergeConfigFrom(&conf)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	//只有单一配置文件时才进行热重载判断
	if reloadFile != nil {
		//必须启用assist插件，因为配置文件热重载参数在此插件中
		if plugin, ok := aggregationConfig.loadedPlugins["assist"]; ok {
			a := plugin.(*plugins.Assist) //Assist指针才实现了PluginConfigNG接口
			//只有首次读取配置的时候启用了热重载才会加载热重载模块，**如果首次没加载则以后都不会加载了
			if a.ConfigReload {
				globalConfig = aggregationConfig
				go initViper(reloadLocation, reloadFile.Name())
			}
		}
	}

	return aggregationConfig, nil
}

//  load all plugins for current config
//  读取当前配置文件中的所有插件,并将配置中的参数映射到插件实例 即PluginConfigNG对象
func (config *Config) loadAllPlugins(fileName string) {
	for pluginName, description := range config.Plugins {
		pluginInstance, found := plugins.BuildPlugin(pluginName, fileName)
		if !found {
			log.Infoln("Plugin -->", pluginName, "<-- Not Found")
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
			log.Infoln("Failed load plugin", pluginName, "-", err)
			continue
		}
		config.loadedPlugins[pluginName] = pluginInstance
	}
}

//合并配置文件
func (config *Config) mergeConfigFrom(other *Config) error {
	config.mergeServicesFrom(other)
	err := config.mergePluginsFrom(other)
	return err
}

// 合并服务
func (config *Config) mergeServicesFrom(other *Config) {
	config.Services = append(config.Services, other.Services...)
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
		err := plugin.Close()
		if err != nil {
			log.Panicln(err)
		}
	}
}
