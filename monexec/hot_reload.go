package monexec

import (
	"github.com/fsnotify/fsnotify"
	"github.com/reddec/monexec/pool"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"log"
	"path"
	"reflect"
)

/*TODO 1、只有单一配置文件的情况下才支持热重载，当多配置文件时程序原逻辑需要对配置文件做合并再执行，如果不合并则可能会发生配置文件冲突，此时可能达不到修改配置所期望的需求
          同时程序此时是基于内存中的合并后的配置去执行的，并不是基于某一配置文件的即无法对下一次的变更做监听
	   2、已启动服务的配置无法变更（即关键的command,args无法变更），除非停止服务。
          同时已启动的服务是在goroutine中运行的，如何根据配置的变更定位到执行该服务的goroutine并执行相应的操作
       3、此热重载并不是简单的根据新配置来重载启动程序，而是需要鉴别出新增服务，被删除的服务。即如何界定何为新增的服务、被删除的服务
          关键点如何判断配置文件是否变更？

*/

var viperCfg *viper.Viper

//初始化viper实例
func initConfig(location, fileName string) {
	log.Println("Viper Start Loading Configuration...")
	viperCfg = viper.New()
	viperCfg.SetConfigName(fileName)
	viperCfg.AddConfigPath(location)
	viperCfg.SetConfigType("yaml")
	err := viperCfg.ReadInConfig()
	if err != nil {
		log.Println("Viper Failed to get the Configuration.")
	}
	go enableDynamicConfig(location, fileName)
}

//启用配置文件热重载
func enableDynamicConfig(location, file string) {
	viperCfg.WatchConfig()
	viperCfg.OnConfigChange(func(event fsnotify.Event) {
		log.Printf("检测到配置文件改变 %s \n", event.String())
		var conf = DefaultConfig()

		fileName := path.Join(location, file)
		data, err := ioutil.ReadFile(fileName)
		err = yaml.Unmarshal(data, &conf)
		if err != nil {
			log.Println("读取最新配置文件失败...", err)
			return
		}

		needStartServ := getNewServices(globalConfig.Services, conf.Services)
		//启动新服务
		if needStartServ != nil {
			log.Printf("需要新执行的服务: %+v \n", needStartServ)
			for i := range needStartServ {
				exec := needStartServ[i]
				//globalConfig中的服务始终与实际运行状态保持统一，保证下一次热重载能正确区分配置差异
				globalConfig.Services = append(globalConfig.Services, exec)
				globalPool.Add(&exec)
				//通过chan传递需要新启动的服务
				pool.NewServChan <- &exec
			}
		}

		needLoadPlugins := getNewPlugins(globalConfig, &conf)
		//加载新插件
		if len(needLoadPlugins) != 0 {
			log.Printf("需要新加载的插件: %+v \n", needLoadPlugins)
			var tempConf = DefaultConfig()
			tempConf.Plugins = needLoadPlugins
			tempConf.loadAllPlugins(fileName)
			globalConfig.mergePluginsFrom(&tempConf)

			// Initialize plugins
			//-- prepare and add newer plugins
			for pluginName := range needLoadPlugins {
				if pluginInstance, exist := globalConfig.loadedPlugins[pluginName]; exist {
					err := pluginInstance.Prepare(*globalCtx, globalPool)
					if err != nil {
						log.Println("Failed prepare plugin", pluginName, "-", err)
					} else {
						log.Println("------> Hot-Reload Plugin", pluginName, "Ready  <------")
						globalPool.Watch(pluginInstance)
					}
				}
			}
		}

	})
}

// 获取更改配置后所有需要新执行的服务
func getNewServices(oldServ, newServ []pool.Executable) []pool.Executable {
	//TODO 1、如何判断是否为新增服务? 即根据服务的哪些参数项来判断，
	//       实际上同label、command、args的服务可以重复启动 即判逻辑上判断为已经存在的旧服务，但实际可能为新增加的需要启动的同名服务
	var needToStart []pool.Executable
	for _, v := range newServ {
		// 判断某个新服务是否与已存在的所有服务都不相同
		unsameNum := 0
		for _, v1 := range oldServ {
			if isSameService(v, v1) {
				break
			} else {
				unsameNum++
			}
		}
		// 只有与所有旧服务都不相同才为需要启动的新服务
		if unsameNum == len(oldServ) {
			needToStart = append(needToStart, v)
		}
	}
	return needToStart
}

//判断是否为同一个服务 暂定为只有Command和Args都相同才为同一个服务
func isSameService(a, b pool.Executable) (same bool) {
	if a.Command == b.Command && reflect.DeepEqual(a.Args, b.Args) {
		same = true
	} else {
		same = false
	}
	return
}

//获取更改配置后所有需要新加载的插件
func getNewPlugins(oldConf, newConf *Config) map[string]interface{} {
	var needLoadPlugins = make(map[string]interface{})
	for pNameNew, plugin := range newConf.Plugins {
		// 判断某个新插件是否与已存在的所有插件都不相同
		unsameNum := 0
		//旧配置中的插件肯定都是已经加载的插件，所以是与loadedPlugins比较
		for pNameOld := range oldConf.loadedPlugins {
			if pNameOld == pNameNew {
				break
			} else {
				unsameNum++
			}
		}
		if unsameNum == len(oldConf.loadedPlugins) {
			needLoadPlugins[pNameNew] = plugin
		}
	}
	return needLoadPlugins
}
