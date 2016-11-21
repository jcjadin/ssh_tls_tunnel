package main

import (
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"

	"github.com/nhooyr/log"
)

type proxyConfig struct {
	Email    string `json:"email"`
	CacheDir string `json:"cacheDir"`
	Hosts    map[string][]struct {
		Name string `json:"name"`
		Addr string `json:"addr"`
	} `json:"hosts"`
}

type host struct {
	protos map[string]*backend
	config *tls.Config
}

type proxy struct {
	hosts   map[string]*host
	manager *autocert.Manager
	config  *tls.Config
}

func newProxy(pc *proxyConfig) (*proxy, error) {
	if pc.CacheDir == "" {
		return nil, errors.New("empty or missing cacheDir")
	}
	m := &autocert.Manager{
		Prompt: autocert.AcceptTOS,
		Cache:  autocert.DirCache(pc.CacheDir),
		Email:  pc.Email,
		Client: &acme.Client{
			HTTPClient: &http.Client{
				Timeout: 15 * time.Second,
			},
		},
	}
	p := &proxy{
		hosts:   make(map[string]*host),
		manager: m,
		config: &tls.Config{
			// See golang/go#12895 for why.
			PreferServerCipherSuites: true,
			GetCertificate:           m.GetCertificate,
		},
	}
	keys := make([][32]byte, 1, 96)
	if _, err := rand.Read(keys[0][:]); err != nil {
		return nil, fmt.Errorf("session ticket key generation failed: %v", err)
	}
	p.config.SetSessionTicketKeys(keys)
	go p.rotateSessionTicketKeys(keys)
	p.config.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		if h, ok := p.hosts[hello.ServerName]; ok {
			return h.config, nil
		}
		return nil, fmt.Errorf("unknown host %s", hello.ServerName)
	}

	var hostnameList []string
	for hostname, protos := range pc.Hosts {
		if hostname == "" {
			return nil, fmt.Errorf("empty key in hosts")
		}
		hostnameList = append(hostnameList, hostname)
		if len(protos) == 0 {
			return nil, fmt.Errorf("hosts.%s is missing protocols", hostname)
		}
		h := &host{
			protos: make(map[string]*backend),
			config: p.config.Clone(),
		}
		for i, proto := range protos {
			h.config.NextProtos = append(h.config.NextProtos, proto.Name)
			if proto.Addr == "" {
				return nil, fmt.Errorf("hosts.%s[%d].addr is empty", hostname, i)
			}
			h.protos[proto.Name] = &backend{
				addr: proto.Addr,
				log:  log.Make(fmt.Sprintf("%s.%s", hostname, proto.Name)),
			}
		}
		p.hosts[hostname] = h
	}
	p.manager.HostPolicy = autocert.HostWhitelist(hostnameList...)

	return p, nil
}

func contains(strs []string, s1 string) bool {
	for _, s2 := range strs {
		if s1 == s2 {
			return true
		}
	}
	return false
}

func (p *proxy) listenAndServe() error {
	l, err := net.Listen("tcp", ":https")
	if err != nil {
		return err
	}
	log.Printf("listening on %v", l.Addr())
	return p.serve(tcpKeepAliveListener{l.(*net.TCPListener)})
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

func (p *proxy) serve(l net.Listener) error {
	defer l.Close()
	var delay time.Duration
	for {
		c, err := l.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				if delay == 0 {
					delay = 5 * time.Millisecond
				} else {
					delay *= 2
					if delay > time.Second {
						delay = time.Second
					}
				}
				log.Printf("%v; retrying in %v", err, delay)
				time.Sleep(delay)
				continue
			}
			return err
		}
		delay = 0
		go p.handle(c)
	}
}

func (p *proxy) rotateSessionTicketKeys(keys [][32]byte) {
	for {
		time.Sleep(1 * time.Hour)
		if len(keys) < cap(keys) {
			keys = keys[:len(keys)+1]
		}
		copy(keys[1:], keys)
		if _, err := rand.Read(keys[0][:]); err != nil {
			log.Fatalf("error generating session ticket key: %v", err)
		}
		p.config.SetSessionTicketKeys(keys)
	}
}

func (p *proxy) handle(c net.Conn) {
	log.Printf("accepted %v", c.RemoteAddr())
	defer log.Printf("disconnected %v", c.RemoteAddr())
	defer c.Close()
	tlc := tls.Server(c, p.config)
	if err := tlc.Handshake(); err != nil {
		// TODO should the TLS library handle prefix?
		log.Printf("TLS handshake error from %v: %v", c.RemoteAddr(), err)
		return
	}
	cs := tlc.ConnectionState()
	p.hosts[cs.ServerName].protos[cs.NegotiatedProtocol].handle(tlc)
}

type backend struct {
	addr string
	log  log.Logger
}

var dialer = &net.Dialer{
	Timeout:   3 * time.Second,
	KeepAlive: time.Minute,
}

var bufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 1<<16)
	},
}

func (b *backend) handle(tlc *tls.Conn) {
	b.log.Printf("accepted %v", tlc.RemoteAddr())
	c2, err := dialer.Dial("tcp", b.addr)
	if err != nil {
		b.log.Print(err)
		return
	}
	defer c2.Close()
	errc := make(chan error, 2)
	cp := func(w io.Writer, r io.Reader) {
		buf := bufferPool.Get().([]byte)
		_, err := io.CopyBuffer(w, r, buf)
		errc <- err
		bufferPool.Put(buf)
	}
	go cp(struct{ io.Writer }{c2}, tlc)
	go cp(tlc, struct{ io.Reader }{c2})
	err = <-errc
	if err != nil {
		b.log.Print(err)
	}
}
