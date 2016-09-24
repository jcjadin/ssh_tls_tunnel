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

func init() {
	acme.loggger = stdlog.New(ioutil.Discard, "", 0)
}

func main() {
	configDir := flag.String("c", "/usr/local/etc/tlsmuxd", "path to the configuration directory")
	flag.Parse()
	os.Chdir(*configDir)

	p := new(proxy)
	err := toml.UnmarshalFile("config.toml", &p)
	if err != nil {
		log.Fatal(err)
	}
	err = p.manager.CacheFile("letsencrypt.cache")
	if err != nil {
		log.Fatal(err)
	}

	// TODO where to put this, before or here?
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		log.Print("terminating")
		os.Exit(0)
	}()

	log.Print("initialized")
	log.Fatal(p.listenAndServe())
}
