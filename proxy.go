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

type config struct {
	Email    string `json:"email"`
	CacheDir string `json:"cacheDir"`
	Protos   []struct {
		Name  string            `json:"name"`
		Hosts map[string]string `json:"hosts"`
	} `json:"protos"`
	DefaultProto string `json:"defaultProto"`
}

// TODO custom config file
type proxy struct {
	// Map of protocol names to hostnames to backends.
	backends map[string]map[string]*backend
	manager  autocert.Manager
	config   *tls.Config
}

func newProxy(c *config) (*proxy, error) {
	p := new(proxy)
	p.backends = make(map[string]map[string]*backend)
	if c.DefaultProto == "" {
		return nil, errors.New("defaultProto is empty or missing")
	}
	var hosts []string
	for i, proto := range c.Protos {
		if proto.Name == "" {
			return nil, fmt.Errorf("protos[%d].name is empty or missing", i)
		}
		if len(proto.Hosts) == 0 {
			return nil, fmt.Errorf("protos[%d].hosts is empty or missing", i)
		}
		p.backends[proto.Name] = make(map[string]*backend)
		for host, addr := range proto.Hosts {
			if host == "" {
				return nil, fmt.Errorf("empty key in protos[%d].hosts", i)
			} else if addr == "" {
				return nil, fmt.Errorf("protos[%d].hosts.%q is empty", i, host)
			}
			p.backends[proto.Name][host] = &backend{
				log:  log.Make(fmt.Sprintf("%q.%q:", proto.Name, host)),
				addr: addr,
			}
			if !contains(hosts, host) {
				hosts = append(hosts, host)
			}
		}
		p.config.NextProtos = append(p.config.NextProtos, proto.Name)
	}
	var ok bool
	p.backends[""], ok = p.backends[c.DefaultProto]
	if !ok {
		return nil, fmt.Errorf("defaultProto (%q) is not defined in protos", c.DefaultProto)
	}

	if c.CacheDir == "" {
		return nil, errors.New("empty or missing cacheDir")
	}
	p.manager = autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      autocert.DirCache(c.CacheDir),
		HostPolicy: autocert.HostWhitelist(hosts...),
		Email:      c.Email,
		Client: &acme.Client{
			HTTPClient: &http.Client{
				Timeout: 15 * time.Second,
			},
		},
	}
	p.config = &tls.Config{
		GetCertificate: p.manager.GetCertificate,
		// See golang/go#12895 for why.
		PreferServerCipherSuites: true,
		MinVersion:               tls.VersionTLS12,
	}
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

func (p *proxy) serve(l net.Listener) error {
	defer l.Close()
	keys := make([][32]byte, 1, 96)
	_, err := rand.Read(keys[0][:])
	if err != nil {
		return fmt.Errorf("session ticket key generation failed: %v", err)
	}
	p.config.SetSessionTicketKeys(keys)
	go p.rotateSessionTicketKeys(keys)

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
		_, err := rand.Read(keys[0][:])
		if err != nil {
			log.Fatalf("error generating session ticket key: %v", err)
		}
		p.config.SetSessionTicketKeys(keys)
	}
}

func (p *proxy) handle(c net.Conn) {
	tlc := tls.Server(c, p.config)
	if err := tlc.Handshake(); err != nil {
		log.Printf("TLS handshake error from %v: %v", c.RemoteAddr(), err)
		c.Close()
		return
	}
	cs := tlc.ConnectionState()
	// Protocol is guaranteed to exist.
	b, ok := p.backends[cs.NegotiatedProtocol][cs.ServerName]
	if !ok {
		log.Printf("unable to find %q.%q for %v", cs.NegotiatedProtocol,
			cs.ServerName, c.RemoteAddr())
		c.Close()
		return
	}
	b.handle(tlc)
}

type backend struct {
	log  log.Logger
	addr string
}

var dialer = &net.Dialer{
	Timeout: 3 * time.Second,
	// No DualStack or KeepAlive because the dialer is used to
	// connect locally, not on the internet.
	// Thus there is no need to worry about broken IPv6
	// and KeepAlive is handled by incoming connection because
	// they are proxied.
}

var bufferPool = sync.Pool{
	New: func() interface{} {
		// TODO maybe different buffer size?
		// benchmark pls
		return make([]byte, 1<<15)
	},
}

func (b *backend) handle(c1 net.Conn) {
	b.log.Printf("accepted %v", c1.RemoteAddr())
	c2, err := dialer.Dial("tcp", b.addr)
	if err != nil {
		b.log.Print(err)
		c1.Close()
		b.log.Printf("disconnected %v", c1.RemoteAddr())
		return
	}
	first := make(chan<- struct{}, 1)
	cp := func(dst net.Conn, src net.Conn) {
		buf := bufferPool.Get().([]byte)
		defer bufferPool.Put(buf)
		// TODO use splice on linux
		// TODO needs some timeout to prevent torshammer ddos
		_, err := io.CopyBuffer(dst, src, buf)
		select {
		case first <- struct{}{}:
			if err != nil {
				b.log.Print(err)
			}
			dst.Close()
			src.Close()
			b.log.Printf("disconnected %v", c1.RemoteAddr())
		default:
		}
	}
	go cp(c1, c2)
	cp(c2, c1)
}
