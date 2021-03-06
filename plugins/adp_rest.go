package plugins

import (
	"context"
	"embed"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/reddec/monexec/pool"
	"github.com/reddec/monexec/ui"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"time"
)

const restApiStartupCheck = 1 * time.Second

//go:generate go-bindata -pkg plugins -prefix ../ui/dist/ ../ui/dist/
//go:generate go-bindata -debug -pkg plugins -prefix ../ui/dist/ ../ui/dist/
type RestPlugin struct {
	Listen string `yaml:"listen"`
	CORS   bool   `yaml:"cors"`
	server *http.Server
}

// 嵌入普通的静态资源
type StaticResource struct {
	staticFS embed.FS //静态资源
	path     string   //设置embed文件到静态资源的相对路径，也就是embed注释里的路径
}

// 静态资源被访问的核心逻辑
func (w *StaticResource) Open(name string) (fs.File, error) {
	var fullName string
	//if strings.Contains(name, `/`) {
	//	fullName = path.Join(w.path, "", name)
	//} else {
	//	fullName = path.Join(w.path, name)
	//}
	fullName = path.Join(w.path, name)
	file, err := w.staticFS.Open(fullName)
	return file, err
}

func (p *RestPlugin) Prepare(ctx context.Context, pl *pool.Pool) error {

	//是否启用production模式
	gin.SetMode(gin.ReleaseMode)

	router := gin.Default()
	if p.CORS {
		router.Use(CORSMiddleware())
	}
	as := &StaticResource{
		staticFS: ui.WebUI,
		path:     "dist",
	}
	// go1.16 embed方式
	router.StaticFS("/ui", http.FS(as))
	// 不使用go-bindata
	//router.StaticFS("/ui", http.Dir("./ui/dist"))
	// 使用go-bindata
	//router.StaticFS("/ui/", &assetfs.AssetFS{Asset: Asset, AssetDir: AssetDir, AssetInfo: AssetInfo, Prefix: ""})
	router.GET("/", func(gctx *gin.Context) {
		gctx.Redirect(http.StatusTemporaryRedirect, "ui")
	})
	router.GET("/supervisors", func(gctx *gin.Context) {
		var names = make([]string, 0)
		for _, sv := range pl.Supervisors() {
			names = append(names, sv.Config().Name)
		}
		gctx.JSON(http.StatusOK, names)
	})
	router.GET("/supervisor/:name", func(gctx *gin.Context) {
		name := gctx.Param("name")
		for _, sv := range pl.Supervisors() {
			if sv.Config().Name == name {
				gctx.JSON(http.StatusOK, sv.Config())
				return
			}
		}
		gctx.AbortWithStatus(http.StatusNotFound)
	})
	router.GET("/supervisor/:name/log", func(gctx *gin.Context) {
		name := gctx.Param("name")
		var f *os.File
		for _, sv := range pl.Supervisors() {
			if sv.Config().Name == name {
				if sv.Config().LogFile == "" {
					break
				}
				f, err := os.Open(sv.Config().LogFile)
				if err != nil {
					gctx.AbortWithError(http.StatusBadGateway, err)
					return
				}
				gctx.Header("Content-Type", "text/plain")
				gctx.Header("Content-Disposition", "attachment; filename=\""+sv.Config().Name+".log\"")
				gctx.AbortWithStatus(http.StatusOK)
				io.Copy(gctx.Writer, f)
				return
			}
		}
		if f != nil {
			defer f.Close()
		}
		gctx.AbortWithStatus(http.StatusNotFound)
	})
	router.POST("/supervisor/:name", func(gctx *gin.Context) {
		name := gctx.Param("name")
		for _, sv := range pl.Supervisors() {
			if sv.Config().Name == name {
				in := pl.Start(ctx, sv)
				gctx.JSON(http.StatusOK, in)
				return
			}
		}
		gctx.AbortWithStatus(http.StatusNotFound)
	})
	router.GET("/instances", func(gctx *gin.Context) {
		var names = make([]string, 0)
		for _, sv := range pl.Instances() {
			names = append(names, sv.Config().Name)
		}
		gctx.JSON(http.StatusOK, names)
	})

	router.GET("/instance/:name", func(gctx *gin.Context) {
		name := gctx.Param("name")
		for _, sv := range pl.Instances() {
			if sv.Config().Name == name {
				gctx.JSON(http.StatusOK, sv)
				return
			}
		}
		gctx.AbortWithStatus(http.StatusNotFound)
	})

	router.POST("/instance/:name", func(gctx *gin.Context) {
		name := gctx.Param("name")
		for _, sv := range pl.Instances() {
			if sv.Config().Name == name {
				pl.Stop(sv)
				gctx.AbortWithStatus(http.StatusCreated)
				return
			}
		}
		gctx.AbortWithStatus(http.StatusNotFound)
	})

	//返回机器标识信息
	router.GET("/info", func(gctx *gin.Context) {
		if AssistInfo == nil {
			gctx.AbortWithStatus(http.StatusBadGateway)
		}
		info := Assist{
			Machine: AssistInfo.Machine,
			Ip:      AssistInfo.Ip,
		}
		gctx.JSON(http.StatusOK, info)
	})

	//登录验证
	router.POST("/login", func(gctx *gin.Context) {
		if AssistInfo == nil || len(AssistInfo.Users) == 0 {
			gctx.AbortWithStatus(http.StatusBadGateway)
		}
		name := gctx.PostForm("name")
		password := gctx.PostForm("password")
		info := make(map[string]interface{})
		info["loginStatus"] = false
		info["info"] = "用户名或密码错误!"
		for _, v := range AssistInfo.Users {
			if v.Username == name && v.Password == password {
				info["loginStatus"] = true
				info["info"] = "登录成功!"
				break
			}
		}
		gctx.JSON(http.StatusOK, info)
	})

	p.server = &http.Server{Addr: p.Listen, Handler: router}
	fmt.Println("rest interface will be available on", p.Listen)
	start := make(chan error, 1)
	go func() {
		start <- p.server.ListenAndServe()
	}()
	select {
	case err := <-start:
		return err
	case <-time.After(restApiStartupCheck):
		return nil
	}
}

func (p *RestPlugin) OnSpawned(ctx context.Context, sv pool.Instance) {}

func (p *RestPlugin) OnStarted(ctx context.Context, sv pool.Instance) {}

func (p *RestPlugin) OnStopped(ctx context.Context, sv pool.Instance, err error) {}

func (p *RestPlugin) OnFinished(ctx context.Context, sv pool.Instance) {}

func (p *RestPlugin) MergeFrom(o interface{}) error {
	def := defaultRestPlugin()
	other := o.(*RestPlugin)
	if p.Listen == def.Listen {
		p.Listen = other.Listen
	} else if other.Listen != def.Listen && other.Listen != p.Listen {
		return errors.Errorf("unmatched Rest listen address %v != %v", p.Listen, other.Listen)
	}
	return nil
}

func (p *RestPlugin) Close() error {
	ctx, closer := context.WithTimeout(context.Background(), 1*time.Second)
	defer closer()
	return p.server.Shutdown(ctx)
}

func defaultRestPlugin() *RestPlugin {
	return &RestPlugin{
		Listen: "localhost:9900",
	}
}

func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}
func init() {
	registerPlugin("rest", func(file string) PluginConfigNG {
		return defaultRestPlugin()
	})
}
