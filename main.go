package main

import (
	"encoding/json"
	"flag"
	"net"
	"os"
	"runtime"

	"github.com/nhooyr/log"
)

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
		go func(host string) {
			l, err := net.Listen("tcp", net.JoinHostPort(host, "https"))
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("listening on %v", l.Addr())
			log.Fatal(p.serve(tcpKeepAliveListener{l.(*net.TCPListener)}))
		}(host)
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
