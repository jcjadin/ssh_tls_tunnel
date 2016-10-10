package main

import (
	"encoding/json"
	"flag"
	"net"
	"os"
	"runtime"
	"time"

	"github.com/nhooyr/log"
)

func main() {
	configPath := flag.String("c", "", "path to configuration file")
	flag.Parse()

	f, err := os.Open(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	var p *proxy
	if err = json.NewDecoder(f).Decode(&p); err != nil {
		log.Fatalf("error decoding config.json: %v", err)
	}
	f.Close()
	if err = p.init(); err != nil {
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

func (l tcpKeepAliveListener) Accept() (c net.Conn, err error) {
	tc, err := l.AcceptTCP()
	if err != nil {
		return
	}
	err = tc.SetKeepAlive(true)
	if err != nil {
		return
	}
	err = tc.SetKeepAlivePeriod(time.Minute)
	if err != nil {
		return
	}
	return tc, nil
}
