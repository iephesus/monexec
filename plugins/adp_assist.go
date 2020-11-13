package plugins

import (
	"context"
	mapset "github.com/deckarep/golang-set"
	"github.com/pkg/errors"
	"github.com/reddec/monexec/pool"
)

var AssistInfo *Assist

type Assist struct {
	Machine   string `yaml:"machine"`
	Ip        string `yaml:"ip"`
	HotReload bool   `yaml:"hotReload"`
	Users     []UserInfo
}

type UserInfo struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

func init() {
	registerPlugin("assist", func(file string) PluginConfigNG {
		return &Assist{}
	})
}

func (a *Assist) Prepare(ctx context.Context, pl *pool.Pool) error {
	AssistInfo = a
	//fmt.Println("Assist调用Prepare")
	return nil
}

func (a *Assist) MergeFrom(other interface{}) error {
	b := other.(*Assist)
	if a.Machine == "" {
		a.Machine = b.Machine
	}
	if a.Machine != b.Machine {
		return errors.New("different machine name")
	}
	if a.Ip == "" {
		a.Ip = b.Ip
	}
	if a.Ip != b.Ip {
		return errors.New("different machine ip")
	}
	//TODO 因为热重载的时候只能是单配置文件，此时是否需要合并配置的判断?
	//if a.HotReload && b.HotReload {
	//	a.HotReload = true
	//} else {
	//	a.HotReload = false
	//}
	if len(a.Users) == 0 {
		a.Users = b.Users
	} else {
		s1 := mapset.NewThreadUnsafeSet()
		for _, user := range a.Users {
			s1.Add(user)
		}
		for _, usero := range b.Users {
			s1.Add(usero)
		}
		var u []UserInfo
		s1.Each(func(i interface{}) bool {
			u = append(u, i.(UserInfo))
			return false
		})
		a.Users = u
	}
	return nil
}

func (a *Assist) Close() error {
	return nil
}

func (a *Assist) OnSpawned(ctx context.Context, sv pool.Instance) {
	//fmt.Println("Assist调用OnSpawned")
}

func (a *Assist) OnStarted(ctx context.Context, sv pool.Instance) {
	//fmt.Println("Assist调用OnStarted")
}

func (a *Assist) OnStopped(ctx context.Context, sv pool.Instance, err error) {
	//fmt.Println("Assist调用OnStopped")
}

func (a *Assist) OnFinished(ctx context.Context, sv pool.Instance) {
	//fmt.Println("Assist调用OnFinished")
}
