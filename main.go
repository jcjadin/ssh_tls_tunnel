package main

import (
	"crypto/rand"
	"encoding/json"
	"flag"
	"io/ioutil"
	stdlog "log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/nhooyr/log"
	"github.com/xenolf/lego/acme"
)

func init() {
	acme.Logger = stdlog.New(ioutil.Discard, "", 0)
}

func main() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		log.Print("terminating")
		os.Exit(0)
	}()

	configDir := flag.String("c", "/usr/local/etc/tlsmuxd", "path to the configuration directory")
	flag.Parse()
	err := os.Chdir(*configDir)
	if err != nil {
		log.Fatal(err)
	}

	p := new(proxy)
	f, err := os.Open("config.json")
	if err != nil {
		log.Fatal(err)
	}
	err = json.NewDecoder(f).Decode(&p)
	if err != nil {
		log.Fatal(err)
	}
	err = p.init()
	if err != nil {
		log.Fatal(err)
	}

	laddr, err := net.ResolveTCPAddr("tcp", ":https")
	if err != nil {
		log.Fatal(err)
	}
	l, err := net.ListenTCP("tcp", laddr)
	if err != nil {
		log.Fatal(err)
	}

	keys := make([][32]byte, 1)
	if _, err := rand.Read(keys[0][:]); err != nil {
		log.Fatal(err)
	}
	p.config.SetSessionTicketKeys(keys)
	go p.rotateSessionTicketKeys(keys)

	log.Print("initialized")
	log.Fatal(p.serve(l))
}
