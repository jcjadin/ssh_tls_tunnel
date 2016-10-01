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
	configPath := flag.String("c", "", "path to configuration file")
	flag.Parse()

	f, err := os.Open(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	var p *proxy
	err = json.NewDecoder(f).Decode(&p)
	if err != nil {
		log.Fatalf("error decoding config.json: %v", err)
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

func (l tcpKeepAliveListener) Accept() (c net.Conn, err error) {
	tc, err := l.AcceptTCP()
	if err != nil {
		return
	}
	tc.SetKeepAlive(true)
	tc.SetKeepAlivePeriod(d.KeepAlive)
	return tc, nil
}
