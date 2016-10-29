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

func newProxy(pc *proxyConfig) (*proxy, error) {
	if pc.CacheDir == "" {
		return nil, errors.New("empty or missing cacheDir")
	}
	p := &proxy{
		backends: make(map[string]map[string]*backend),
		manager: autocert.Manager{
			Prompt: autocert.AcceptTOS,
			Cache:  autocert.DirCache(pc.CacheDir),
			Email:  pc.Email,
			Client: &acme.Client{
				HTTPClient: &http.Client{
					Timeout: 15 * time.Second,
				},
			},
		},
		config: &tls.Config{
			// See golang/go#12895 for why.
			PreferServerCipherSuites: true,
		},
	}
	p.config.GetCertificate = p.manager.GetCertificate

	if pc.DefaultProto == "" {
		return nil, errors.New("defaultProto is empty or missing")
	}
	var hosts []string
	for i, proto := range pc.Protos {
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
				addr: addr,
				log:  log.Make(fmt.Sprintf("%q.%q", proto.Name, host)),
			}
			if !contains(hosts, host) {
				hosts = append(hosts, host)
			}
		}
		p.config.NextProtos = append(p.config.NextProtos, proto.Name)
	}
	var ok bool
	if p.backends[""], ok = p.backends[pc.DefaultProto]; !ok {
		return nil, fmt.Errorf("defaultProto (%q) is not defined in protos", pc.DefaultProto)
	}
	p.manager.HostPolicy = autocert.HostWhitelist(hosts...)
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
	if err = tc.SetKeepAlive(true); err != nil {
		return
	}
	if err = tc.SetKeepAlivePeriod(time.Minute); err != nil {
		return
	}
	return tc, nil
}

func (p *proxy) serve(l net.Listener) error {
	defer l.Close()
	keys := make([][32]byte, 1, 96)
	if _, err := rand.Read(keys[0][:]); err != nil {
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
	// Protocol is guaranteed to exist.
	b, ok := p.backends[cs.NegotiatedProtocol][cs.ServerName]
	if !ok {
		log.Printf("unable to find %q.%q for %v", cs.NegotiatedProtocol,
			cs.ServerName, c.RemoteAddr())
		return
	}
	b.handle(tlc)
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
	if err = <-errc; err != nil {
		b.log.Print(err)
	}
}
