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

// TODO custom config file
type proxy struct {
	Hosts          []string `json:"hosts"`
	BindInterfaces []string `json:"bindInterfaces"`
	Email          string   `json:"email"`
	Default        *struct {
		Fallback string            `json:"fallback"`
		Hosts    map[string]string `json:"hosts"`
	} `json:"default"`
	Protos []struct {
		Name     string            `json:"name"`
		Fallback string            `json:"fallback"`
		Hosts    map[string]string `json:"hosts"`
	} `json:"protos"`

	// Map of protocol names to hostnames to backends.
	backends map[string]map[string]*backend
	manager  autocert.Manager
	config   *tls.Config
}

func (p *proxy) init() error {
	if len(p.Hosts) == 0 {
		return errors.New("hosts is empty or missing")
	}

	if len(p.BindInterfaces) == 0 {
		return errors.New("bindInterfaces is empty or missing")
	}

	if p.Default == nil {
		return errors.New("missing default")
	}
	if p.Default.Fallback == "" {
		return errors.New("default.fallback is empty or missing")
	}
	p.backends = make(map[string]map[string]*backend)
	p.backends[""] = make(map[string]*backend)
	p.backends[""][""] = &backend{
		`""."": `,
		p.Default.Fallback,
	}
	for host, addr := range p.Default.Hosts {
		if host == "" {
			return errors.New("empty key in default.hosts")
		}
		p.backends[""][host] = &backend{
			fmt.Sprintf(`"".%q: `, host),
			addr,
		}
	}

	hosts := append([]string(nil), p.Hosts...)
	p.config = &tls.Config{
		GetCertificate: p.manager.GetCertificate,
	}
	// TODO Is this how priority should work?
	for i, proto := range p.Protos {
		if proto.Name == "" {
			return fmt.Errorf("protos[%d].name is empty or missing", i)
		}
		p.backends[proto.Name] = make(map[string]*backend)
		if proto.Fallback == "" {
			// TODO inconsistent because we do not check if addresses below
			// are empty or not. Fix with new configuration file library.
			return fmt.Errorf("protos[%d].fallback is empty or missing", i)
		}
		p.backends[proto.Name][""] = &backend{
			fmt.Sprintf(`%q."": `, proto.Name),
			proto.Fallback,
		}
		for _, host := range p.Hosts {
			p.backends[proto.Name][host] = p.backends[proto.Name][""]
		}
		for host, addr := range proto.Hosts {
			if host == "" {
				return fmt.Errorf("empty key in protos[%d].hosts", i)
			}
			p.backends[proto.Name][host] = &backend{
				fmt.Sprintf("%q.%q: ", proto.Name, host),
				addr,
			}
			for _, host2 := range hosts {
				if host == host2 {
					continue
				}
			}
			hosts = append(hosts, host)
		}
		p.config.NextProtos = append(p.config.NextProtos, proto.Name)
	}

	p.manager = autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(hosts...),
		Cache:      autocert.DirCache("crypto"),
		Email:      p.Email,
		Client: &acme.Client{
			HTTPClient: &http.Client{
				Timeout: 10 * time.Second,
			},
		},
	}

	keys := make([][32]byte, 1, 96)
	_, err := rand.Read(keys[0][:])
	if err != nil {
		return fmt.Errorf("session ticket key generation failed: %v", err)
	}
	p.config.SetSessionTicketKeys(keys)
	go p.rotateSessionTicketKeys(keys)
	return nil
}

func (p *proxy) rotateSessionTicketKeys(keys [][32]byte) {
	for {
		// TODO test
		time.Sleep(1 * time.Hour)
		log.Print("rotating session ticket keys")
		if len(keys) < cap(keys) {
			keys = keys[:len(keys)+1]
		}
		for s1, s2 := len(keys)-2, len(keys)-1; s2 > 0; s1, s2 = s1-1, s2-1 {
			keys[s2] = keys[s1]
		}
		_, err := rand.Read(keys[0][:])
		if err != nil {
			log.Fatalf("error generating session ticket key: %v", err)
		}
		p.config.SetSessionTicketKeys(keys)
	}
}

func (p *proxy) serve(l net.Listener) error {
	var delay time.Duration
	for {
		c, err := l.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				if delay == 0 {
					delay = 5 * time.Millisecond
				} else {
					delay *= 2
				}
				if delay > time.Second {
					delay = time.Second
				}
				log.Printf("%v; retrying in %v", err, delay)
				time.Sleep(delay)
				continue
			}
			// TODO test
			return err
		}
		delay = 0
		go p.handle(c)
	}
}

func (p *proxy) handle(c net.Conn) {
	tlc := tls.Server(c, p.config)
	err := tlc.Handshake()
	if err != nil {
		c.Close()
		// TODO further evalute based on logs
		log.Printf("TLS handshake error from %v: %v", c.RemoteAddr(), err)
		return
	}
	cs := tlc.ConnectionState()
	hosts := p.backends[cs.NegotiatedProtocol]
	b, ok := hosts[cs.ServerName]
	if !ok {
		b = hosts[""]
	}
	b.handle(tlc)
}

type backend struct {
	name string
	addr string
}

var d = &net.Dialer{
	Timeout:   3 * time.Second,
	KeepAlive: 30 * time.Second,
	DualStack: true,
}

func (b *backend) handle(c1 net.Conn) {
	raddr := c1.RemoteAddr()
	b.logf("accepted %v", raddr)
	defer b.logf("disconnected %v", raddr)
	c2, err := d.Dial("tcp", b.addr)
	if err != nil {
		c1.Close()
		b.log(err)
		return
	}
	done := make(chan struct{})
	var once sync.Once
	go func() {
		_, err := io.Copy(c2, c1)
		if err != nil {
			b.logf("error copying %v to %v: %v", raddr, c2.RemoteAddr(), err)
		}
		once.Do(func() {
			c2.Close()
			c1.Close()
		})
		close(done)
	}()
	_, err = io.Copy(c1, c2)
	if err != nil {
		b.logf("error copying %v to %v: %v", c2.RemoteAddr(), raddr, err)
	}
	once.Do(func() {
		c1.Close()
		c2.Close()
	})
	<-done
}

func (b *backend) logf(format string, v ...interface{}) {
	log.Printf(b.name+format, v...)
}

func (b *backend) log(err error) {
	log.Print(b.name, err)
}
