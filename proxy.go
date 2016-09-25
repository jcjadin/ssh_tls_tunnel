package main

import (
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/nhooyr/log"

	"rsc.io/letsencrypt"
)

// TODO custom config file
type proxy struct {
	Hosts          []string `json:"hosts"`
	BindInterfaces []string `json:"bindInterfaces"`
	Email          string   `json:"email,omitempty"`
	Default        *struct {
		Fallback string            `json:"fallback"`
		Hosts    map[string]string `json:"hosts,omitempty"`
	} `json:"default"`
	Protos []struct {
		Name     string            `json:"name"`
		Fallback string            `json:"fallback"`
		Hosts    map[string]string `json:"hosts,omitempty"`
	} `json:"protos,omitempty"`

	backends map[string]map[string]*backend

	manager *letsencrypt.Manager
	config  *tls.Config
}

func (p *proxy) init() error {
	if len(p.BindInterfaces) == 0 {
		return errors.New("bindInterfaces is missing or empty")
	}

	p.manager = new(letsencrypt.Manager)
	err := p.manager.CacheFile("letsencrypt.cache")
	if err != nil {
		return err
	}

	if !p.manager.Registered() && p.Email != "" {
		err = p.manager.Register(p.Email, func(tosURL string) bool {
			return true
		})
		if err != nil {
			return err
		}
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

	// hosts are actually set at the bottom of the function
	// because others might be under a protocol.
	if len(p.Hosts) == 0 {
		return errors.New("hosts is empty or missing")
	}
	p.config = &tls.Config{
		GetCertificate: p.manager.GetCertificate,
	}
	for i, proto := range p.Protos {
		if proto.Name == "" {
			return fmt.Errorf("protos[%d].name is empty or missing", i)
		}
		p.backends[proto.Name] = make(map[string]*backend)
		if proto.Fallback == "" {
			// TODO inconsistent because we do not check if addresses below
			// are empty or not
			return fmt.Errorf("protos[%d].fallback is empty or missing", i)
		}
		p.backends[proto.Name][""] = &backend{
			fmt.Sprintf(`%q."": `, proto.Name),
			proto.Fallback,
		}
		for host, addr := range proto.Hosts {
			if host == "" {
				return fmt.Errorf("empty key in protos[%d].hosts", i)
			}
			p.backends[proto.Name][host] = &backend{
				fmt.Sprintf("%q.%q: ", proto.Name, host),
				addr,
			}
			if !contains(p.Hosts, host) {
				p.Hosts = append(p.Hosts, host)
			}
		}
		p.config.NextProtos = append(p.config.NextProtos, proto.Name)
	}
	p.manager.SetHosts(p.Hosts)
	return nil
}

func contains(strs []string, s1 string) bool {
	for _, s2 := range strs {
		if s1 == s2 {
			return true
		}
	}
	return false
}

// TODO maybe use tls.Listen
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
				log.Print("%v; retrying in %v", err, delay)
				time.Sleep(delay)
				continue
			}
			return err
		}
		delay = 0
		go p.handle(c)
	}
}

// TODO Cache on disk for restarts
func (p *proxy) rotateSessionTicketKeys(keys [][32]byte) {
	for {
		time.Sleep(9 * time.Hour)
		log.Println("rotating session ticket keys")
		if len(keys) < cap(keys) {
			keys = append(keys, [32]byte{})
		}
		for s1, s2 := len(keys)-2, len(keys)-1; s2 > 0; s1, s2 = s1-1, s2-1 {
			keys[s2] = keys[s1]
		}
		if _, err := rand.Read(keys[0][:]); err != nil {
			log.Fatalf("error rotating session ticket keys: %v", err)
		}
		p.config.SetSessionTicketKeys(keys)
	}
}

// TODO optimize.
var d = &net.Dialer{
	Timeout:   10 * time.Second,
	KeepAlive: 30 * time.Second,
	DualStack: true,
}

func (p *proxy) handle(c net.Conn) {
	raddr := c.RemoteAddr()
	tlc := tls.Server(c, p.config)
	err := tlc.Handshake()
	if err != nil {
		c.Close()
		log.Printf("TLS handshake error from %v: %v", raddr, err)
		return
	}
	cs := tlc.ConnectionState()
	log.Printf("accepted %v for %q.%q", raddr, cs.NegotiatedProtocol, cs.ServerName)
	defer log.Printf("disconnected %v", raddr)
	hosts, ok := p.backends[cs.NegotiatedProtocol]
	b, ok := hosts[cs.ServerName]
	if !ok {
		log.Printf(`%v: %q.%q not found; falling back to %q.""`, raddr, cs.NegotiatedProtocol, cs.ServerName, cs.NegotiatedProtocol)
		b = hosts[""]
	}
	b.handle(tlc)
}

type backend struct {
	name string
	addr string
}

// TODO What is the compare and swap stuff in tls.Conn.Close()?
func (b *backend) handle(c1 net.Conn) {
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
			b.log(err)
		}
		once.Do(func() {
			c2.Close()
			c1.Close()
		})
		close(done)
	}()
	_, err = io.Copy(c1, c2)
	if err != nil {
		b.log(err)
	}
	once.Do(func() {
		c1.Close()
		c2.Close()
	})
	<-done
}

func (b *backend) log(err error) {
	log.Print(b.name, err)
}
