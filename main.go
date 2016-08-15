package main

import (
	"crypto/tls"
	"errors"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/nhooyr/log"
	"github.com/nhooyr/toml"
)

type backend struct {
	Name string
	Addr string
}

type filteredBackend struct {
	*backend
	Protocols   []string `toml:"optional"`
	ServerNames []string `toml:"optional"`
}

func (b *filteredBackend) Init() error {
	if b.Protocols == nil && b.ServerNames == nil {
		return errors.New("missing protocols and serverNames; at least one must be present")
	}
	return nil
}

type proxy struct {
	Certs    [][]string
	Backends []*filteredBackend
	Fallback *backend `toml:"optional"`

	backends map[string]map[string]*backend
	config   *tls.Config
}

var logger *log.Logger

func main() {
	configDir := flag.String("c", "/usr/local/etc/tlsmuxd", "path to the configuration directory")
	timestamps := flag.Bool("timestamps", false, "enable timestamps on log lines")
	flag.Parse()

	logger = log.New(os.Stderr, *timestamps)

	p := new(proxy)
	err := toml.UnmarshalFile(*configDir+"/config.toml", &p)
	if err != nil {
		logger.Fatal(err)
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		logger.Print("terminating")
		os.Exit(0)
	}()

	logger.Print("initialized")
	logger.Print(p)
}
