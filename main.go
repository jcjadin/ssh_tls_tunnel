package main

import (
	"flag"
	"io/ioutil"
	stdlog "log"
	"os"
	"os/signal"
	"syscall"

	"github.com/nhooyr/log"
	"github.com/nhooyr/toml"
	"github.com/xenolf/lego/acme"
)

var logger *log.Logger

func init() {
	acme.Logger = stdlog.New(ioutil.Discard, "", 0)
}

func main() {
	configDir := flag.String("c", "/usr/local/etc/tlsmuxd", "path to the configuration directory")
	timestamps := flag.Bool("timestamps", false, "enable timestamps on log lines")
	flag.Parse()

	os.Chdir(*configDir)
	logger = log.New(os.Stderr, *timestamps)

	p := new(proxy)
	err := toml.UnmarshalFile("config.toml", &p)
	if err != nil {
		logger.Fatal(err)
	}
	if err = p.manager.CacheFile("letsencrypt.cache"); err != nil {
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
	logger.Fatal(p.listenAndServe())
}
