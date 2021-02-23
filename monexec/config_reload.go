package monexec

import (
	"github.com/fsnotify/fsnotify"
	"github.com/mitchellh/mapstructure"
	"github.com/reddec/monexec/plugins"
	"github.com/reddec/monexec/pool"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"path"
	"runtime"
)

/*
TODO 1、只有单一配置文件的情况下才支持热重载，当多配置文件时程序原逻辑需要对配置文件做合并再执行，如果不合并则可能会发生配置文件冲突，此时可能达不到修改配置所期望的需求
          同时程序此时是基于内存中的合并后的配置去执行的，并不是基于某一配置文件的即无法对下一次的变更做监听
	   2、已启动服务的配置无法变更（即关键的command,args无法变更），除非停止服务。
          同时已启动的服务是在goroutine中运行的，如何根据配置的变更定位到执行该服务的goroutine并执行相应的操作
       3、此热重载并不是简单的根据新配置来重载启动程序，而是需要鉴别出新增服务，被删除的服务。即如何界定何为新增的服务、被删除的服务
          关键点如何判断配置文件是否变更？
       4、因为配置文件解析(Unmarshal)就无法做唯一性判断，所以如果想在解析后做唯一性判断，会因为同参数配置导致无法选取具体是存留哪一个配置 --回到问题3
          假如按label做唯一性判断，即解析出两个相同label的服务，且command和args不同，那具体加载哪一个舍弃哪一个?
       5、热重载一般为配置形参数的热重载，非执行命令形参数热重载
*/

func init() {
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
		//ForceColors:   true,
	})
}

//初始化Viper实例,并开启新协程监听配置文件重载
func initViper(location, fileName string) {
	log.Info("--->  配置文件热重载初始化  <---")
	log.Info("Viper Start Loading Configuration...")
	viperCfg := viper.New()
	viperCfg.SetConfigName(fileName)
	viperCfg.AddConfigPath(location)
	viperCfg.SetConfigType("yaml")
	err := viperCfg.ReadInConfig()
	if err != nil {
		log.Fatal("Viper Failed to get the Configuration.")
	}

	go enableDynamicConfig(viperCfg, location, fileName)
}

//启用Viper监听配置文件改动 实现热重载
func enableDynamicConfig(viperCfg *viper.Viper, location, file string) {
	viperCfg.WatchConfig()

	//TODO bugs: 使用vi、atom、vscode等其他编辑器编辑配置文件时，会触发两次回调
	viperCfg.OnConfigChange(func(event fsnotify.Event) {
		gLock.Lock()
		defer gLock.Unlock()
		log.Infof("检测到配置文件改变 %s ", event.String())
		//每次监听配置改动的时候，都需要先判断配置中是否关闭了热重载参数
		if enable := viperCfg.GetBool("assist.configReload"); enable {
			var conf = DefaultConfig()
			fileName := path.Join(location, file)
			data, err := ioutil.ReadFile(fileName)
			err = yaml.Unmarshal(data, &conf)
			if err != nil {
				log.Infoln("读取最新配置文件出错...", err)
				return
			}


			needStartServ := getNewServices(globalConfig.Services, conf.Services)
			//启动新服务 不启动新协程
			startNewService(needStartServ)

			needLoadPlugins := getNewPlugins(globalConfig, &conf)
			//加载新插件 不启动新协程
			loadNewPlugin(fileName, needLoadPlugins)

			//TODO 是否启用重载插件不同参数
			//loadPluginsDiff(&conf)
		} else {
			pool.HotReloadCloseChan <- true
			log.Infoln("配置文件热重载关闭...")
			runtime.Goexit()
		}

	})
}

// 获取更改配置后所有需要新启动的服务
func getNewServices(oldServ, newServ []pool.Executable) []pool.Executable {
	//TODO old: 如何判断是否为新增服务?实际上同label、command、args的服务可以重复启动 即判逻辑上判断为已经存在的旧服务，但实际可能为新增加的需要启动的同名服务
	//TODO new: 只用label来判断，由编写配置文件的人员自行把控，因为存在需要启动 同command和args服务的情况 同时只关心新增服务的情况
	var needToStart []pool.Executable
	servMap := make(map[string]pool.Executable)
	for _, v1 := range oldServ {
		servMap[v1.Name] = v1
	}
	for _, v := range newServ {
		if _, ok := servMap[v.Name]; !ok {
			needToStart = append(needToStart, v)
		}
	}
	return needToStart
}

// 启动新服务
func startNewService(needStartServ []pool.Executable) {
	if needStartServ != nil {
		log.Infof("---> 开始服务热重载...  新增服务数(%v) <---", len(needStartServ))
		for i := range needStartServ {
			exec := needStartServ[i]
			FillDefaultExecutable(&exec)

			log.Infof("正在启动第%v个服务:  >>>%v<<<", i+1, exec.Name)
			//1、 globalConfig中的服务数要始终与实际运行状态保持统一
			//2、 因为获取新增服务都是与globalConfig做比较，保证globalConfig中服务数正确才能在下一次热重载时获取到新增服务
			globalConfig.Services = append(globalConfig.Services, exec)
			globalPool.Add(&exec)

			//通过chan传递需要新启动的服务
			pool.NewServChan <- &exec
		}
	} else {
		log.Infoln("没有新增服务需要启动...")
	}
}

//获取更改配置后所有需要新加载的插件
func getNewPlugins(oldConf, newConf *Config) map[string]interface{} {
	var needLoadPlugins = make(map[string]interface{})
	var pluginMap = make(map[string]byte)
	//旧配置中的插件肯定都是已经加载的插件，所以是与loadedPlugins比较
	for pNameOld := range oldConf.loadedPlugins {
		pluginMap[pNameOld] = 1
	}
	for pNameNew, plugin := range newConf.Plugins {
		if _, ok := pluginMap[pNameNew]; !ok {
			needLoadPlugins[pNameNew] = plugin
		}
	}

	return needLoadPlugins
}

//加载新插件
func loadNewPlugin(fileName string, needLoadPlugins map[string]interface{}) {
	if len(needLoadPlugins) != 0 {
		log.Infof("---> 开始加载新插件...  新增插件数(%v) <---", len(needLoadPlugins))

		//将插件加载到loadedPlugins映射中
		var tempConf = DefaultConfig()
		tempConf.Plugins = needLoadPlugins
		tempConf.loadAllPlugins(fileName)
		err := globalConfig.mergePluginsFrom(&tempConf)
		if err != nil {
			log.Errorln(err)
		}

		for pluginName := range needLoadPlugins {
			if pluginInstance, exist := globalConfig.loadedPlugins[pluginName]; exist {
				err := pluginInstance.Prepare(*globalCtx, globalPool)
				if err != nil {
					log.Infoln("Failed prepare plugin", pluginName, "-", err)
				} else {
					log.Infoln("---> 插件", pluginName, "就绪 <---")
					globalPool.Watch(pluginInstance)
				}
			}
		}
	}
}

//TODO
//重新加载修改过的插件数据
func loadPluginsDiff(newConf *Config) {
	//TODO 如果旧配置中的参数 新配置中不存在，是把此参数做删除处理还是保持原参数不变
	//     1、如果删除的话，即旧配置完全按新配置覆盖
	//     2、如果保持不变的话，即分别判断每一项，用新配置中存在的参数项替换旧配置对应的参数项
	for pluginName, newPluginIns := range newConf.Plugins {
		switch pluginName {
		case "assist":
			var npi plugins.Assist
			mapstructure.Decode(newPluginIns, &npi)
			plugins.AssistInfo.Machine = npi.Machine
			plugins.AssistInfo.Ip = npi.Ip
			plugins.AssistInfo.ConfigReload = npi.ConfigReload
		}
		//TODO 其他插件配置修改
	}
}
