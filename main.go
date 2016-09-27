package main

import (
	"encoding/json"
	"flag"
	"io/ioutil"
	stdlog "log"
	"net"
	"os"
	"runtime"

	"github.com/nhooyr/log"
	"github.com/xenolf/lego/acme"
)

func init() {
	acme.Logger = stdlog.New(ioutil.Discard, "", 0)
}

func main() {
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
	var p *proxy
	err = json.NewDecoder(f).Decode(&p)
	if err != nil {
		log.Fatal(err)
	}
	f.Close()
	err = p.init()
	if err != nil {
		log.Fatal(err)
	}

	for _, host := range p.BindInterfaces {
		l, err := net.Listen("tcp", net.JoinHostPort(host, "https"))
		if err != nil {
			log.Fatal(err)
		}
		go func() {
			log.Printf("serving connections on %v", l.Addr())
			log.Fatal(p.serve(tcpKeepAliveListener{l.(*net.TCPListener)}))
		}()
	}
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
	tc.SetKeepAlivePeriod(d.KeepAlive)
	return tc, nil
}
