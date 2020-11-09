package plugins

import (
	"context"
	"fmt"
	mapset "github.com/deckarep/golang-set"
	"github.com/pkg/errors"
	"github.com/reddec/monexec/pool"
)

var MachineInfo *RegInfo

type RegInfo struct {
	Machine string `yaml:"machine"`
	Ip      string `yaml:"ip"`
	Users   []UserInfo
}

type UserInfo struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

func init() {
	registerPlugin("reginfo", func(file string) PluginConfigNG {
		return &RegInfo{}
	})
}

func (r *RegInfo) Prepare(ctx context.Context, pl *pool.Pool) error {
	MachineInfo = r
	fmt.Println("RegInfo调用Prepare")
	return nil
}

func (r *RegInfo) MergeFrom(other interface{}) error {
	b := other.(*RegInfo)
	if r.Machine == "" {
		r.Machine = b.Machine
	}
	if r.Machine != b.Machine {
		return errors.New("different machine name")
	}
	if r.Ip == "" {
		r.Ip = b.Ip
	}
	if r.Ip != b.Ip {
		return errors.New("different machine ip")
	}
	if len(r.Users) == 0 {
		r.Users = b.Users
	} else {
		s1 := mapset.NewThreadUnsafeSet()
		for _, user := range r.Users {
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
		r.Users = u
	}
	return nil
}

func (r *RegInfo) Close() error {
	return nil
}

func (r *RegInfo) OnSpawned(ctx context.Context, sv pool.Instance) {
	fmt.Println("RegInfo调用OnSpawned")
}

func (r *RegInfo) OnStarted(ctx context.Context, sv pool.Instance) {
	fmt.Println("RegInfo调用OnStarted")
}

func (r *RegInfo) OnStopped(ctx context.Context, sv pool.Instance, err error) {
	fmt.Println("RegInfo调用OnStopped")
}

func (r *RegInfo) OnFinished(ctx context.Context, sv pool.Instance) {
	fmt.Println("RegInfo调用OnFinished")
}
