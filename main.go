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
	"runtime"
	"syscall"
	"time"

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
	f, err := os.Open("config.json")
	if err != nil {
		log.Fatal(err)
	}

	p := new(proxy)
	err = json.NewDecoder(f).Decode(&p)
	if err != nil {
		log.Fatal(err)
	}
	err = p.init()
	if err != nil {
		log.Fatal(err)
	}

	// TODO disk caching
	keys := make([][32]byte, 1)
	_, err = rand.Read(keys[0][:])
	if err != nil {
		log.Fatal(err)
	}
	p.config.SetSessionTicketKeys(keys)
	go p.rotateSessionTicketKeys(keys)

	for _, host := range p.bindInterfaces {
		laddr, err := net.ResolveTCPAddr("tcp", net.JoinHostPort(host, "https"))
		if err != nil {
			log.Fatal(err)
		}
		l, err := net.ListenTCP("tcp", laddr)
		if err != nil {
			log.Fatal(err)
		}
		go log.Fatal(p.serve(tcpKeepAliveListener{l}))
	}

	log.Print("initialized")
	runtime.Goexit()
}

type tcpKeepAliveListener struct {
	*net.TCPListener
}

func (ln tcpKeepAliveListener) Accept() (c net.Conn, err error) {
	tc, err := ln.AcceptTCP()
	if err != nil {
		return
	}
	tc.SetKeepAlive(true)
	tc.SetKeepAlivePeriod(30 * time.Second)
	return tc, nil
}
