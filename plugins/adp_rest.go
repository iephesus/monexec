package plugins

import (
	"context"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/reddec/monexec/pool"
	"io"
	"net/http"
	"os"
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

func (p *RestPlugin) Prepare(ctx context.Context, pl *pool.Pool) error {

	router := gin.Default()
	if p.CORS {
		router.Use(CORSMiddleware())
	}
	router.StaticFS("/ui", http.Dir("./ui/dist"))
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
			gctx.AbortWithStatus(http.StatusNotFound)
		}
		info := Assist{
			Machine: AssistInfo.Machine,
			Ip:      AssistInfo.Ip,
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
