package plugins

import (
	"context"
	"errors"
	"github.com/reddec/monexec/pool"
	"log"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
)

type Email struct {
	Smtp         string   `yaml:"smtp"`
	From         string   `yaml:"from"`
	Password     string   `yaml:"password"`
	To           []string `yaml:"to"`
	Services     []string `yaml:"services"`
	withTemplate `mapstructure:",squash" yaml:",inline"`

	log         *log.Logger
	hostname    string
	servicesSet map[string]bool
	workDir     string
}

func (e *Email) renderAndSend(message string) {
	e.log.Println(message)
	host, _, _ := net.SplitHostPort(e.Smtp)
	auth := smtp.PlainAuth("", e.From, e.Password, host)
	err := smtp.SendMail(e.Smtp, auth, e.From, e.To, []byte(message))
	if err != nil {
		e.log.Println("failed send mail:", err)
	} else {
		e.log.Println("sent")
	}
}

func (e *Email) OnSpawned(ctx context.Context, sv pool.Instance) {}

func (e *Email) OnStarted(ctx context.Context, sv pool.Instance) {
	label := sv.Config().Name
	if e.servicesSet[label] {
		content, renderErr := e.renderDefault("spawned", label, label, nil, e.log)
		if renderErr != nil {
			e.log.Println("failed render:", renderErr)
		} else {
			e.renderAndSend(content)
		}
	}
}

func (e *Email) OnStopped(ctx context.Context, sv pool.Instance, err error) {
	label := sv.Config().Name
	if e.servicesSet[label] {
		content, renderErr := e.renderDefault("stopped", label, label, err, e.log)
		if renderErr != nil {
			e.log.Println("failed render:", renderErr)
		} else {
			e.renderAndSend(content)
		}
	}
}

func (e *Email) OnFinished(ctx context.Context, sv pool.Instance) {}

func (e *Email) Prepare(ctx context.Context, pl *pool.Pool) error {
	e.servicesSet = makeSet(e.Services)
	e.log = log.New(os.Stderr, "[email] ", log.LstdFlags)
	e.hostname, _ = os.Hostname()
	return nil
}

func (e *Email) MergeFrom(other interface{}) error {
	b := other.(*Email)
	if e.From == "" {
		e.From = b.From
	}
	if e.From != b.From {
		return errors.New("different from address")
	}
	if e.Smtp == "" {
		e.Smtp = b.Smtp
	}
	if e.Smtp != b.Smtp {
		return errors.New("different smtp servers")
	}
	if e.Template == "" {
		e.Template = b.Template
	}
	if e.Template != b.Template {
		return errors.New("different templates")
	}
	e.TemplateFile = realPath(e.TemplateFile, e.workDir)
	b.TemplateFile = realPath(b.TemplateFile, b.workDir)
	if e.TemplateFile == "" {
		e.TemplateFile = b.TemplateFile
	}
	if e.TemplateFile != b.TemplateFile {
		return errors.New("different template files")
	}
	if e.Password == "" {
		e.Password = b.Password
	}
	if e.Password != b.Password {
		return errors.New("different password")
	}
	e.To = unique(append(e.To, b.To...))
	e.Services = append(e.Services, b.Services...)
	return nil
}
func (e *Email) Close() error { return nil }
func init() {
	registerPlugin("email", func(file string) PluginConfigNG {
		return &Email{workDir: filepath.Dir(file)}
	})
}
