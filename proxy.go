package main

import (
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/nhooyr/log"

	"rsc.io/letsencrypt"
)

// TODO custom config file
type proxy struct {
	Hosts  []string `json:"hosts"`
	Email  string   `json:"email"`
	Protos []*struct {
		Name  string            `json:"name"`
		Hosts map[string]string `json:"hosts"`
	} `json:"protos"`

	backends map[string]map[string]*backend

	manager *letsencrypt.Manager
	config  *tls.Config
}

func (p *proxy) init() error {
	p.manager = new(letsencrypt.Manager)
	err := p.manager.CacheFile("letsencrypt.cache")
	if err != nil {
		log.Fatal(err)
	}
	p.manager.SetHosts(p.Hosts)
	if p.Hosts == nil {
		return errors.New("empty hosts")
	}
	if p.Email != "" {
		err = p.manager.Register(p.Email, func(tosURL string) bool {
			return true
		})
		if err != nil && err.Error() != "already registered" {
			return err
		}
	}
	if p.Protos == nil {
		return errors.New("missing protos")
	}
	p.config = &tls.Config{
		GetCertificate: p.manager.GetCertificate,
	}
	p.backends = make(map[string]map[string]*backend)
	for _, proto := range p.Protos {
		p.backends[proto.Name] = make(map[string]*backend)
		for host, addr := range proto.Hosts {
			p.backends[proto.Name][host] = &backend{
				Name: fmt.Sprintf("%q.%q", proto.Name, host),
				Addr: addr,
			}
		}
		if _, ok := p.backends[proto.Name][""]; !ok {
			return fmt.Errorf("missing empty host in proto %q", proto.Name)
		}
		if proto.Name != "" {
			p.config.NextProtos = append(p.config.NextProtos, proto.Name)
		}
	}
	if _, ok := p.backends[""]; !ok {
		return fmt.Errorf("missing empty protocol")
	}
	return nil
}

func (p *proxy) serve(l *net.TCPListener) error {
	var tempDelay time.Duration
	for {
		c, err := l.AcceptTCP()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if tempDelay > time.Second {
					tempDelay = time.Second
				}
				log.Print("%v; retrying in %v", err, tempDelay)
				time.Sleep(tempDelay)
				continue
			}
			return err
		}
		tempDelay = 0
		go p.handle(c)
	}
}

// TODO Cache on disk for restarts
func (p *proxy) rotateSessionTicketKeys(keys [][32]byte) {
	for i := 0; i < 2; i++ {
		time.Sleep(9 * time.Hour)
		log.Println("rotating session ticket keys")
		keys = append(keys, [32]byte{})
		for s1, s2 := len(keys)-1, len(keys)-2; s1 > 0; s1, s2 = s1-1, s2-1 {
			keys[s1] = keys[s2]
		}
		if _, err := rand.Read(keys[0][:]); err != nil {
			log.Fatalf("error rotating session ticket keys: %v", err)
		}
	}
	for {
		time.Sleep(9 * time.Hour)
		log.Println("rotating session ticket keys")
		keys[2] = keys[1]
		keys[1] = keys[0]
		if _, err := rand.Read(keys[0][:]); err != nil {
			log.Fatal(err)
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

func (p *proxy) handle(tc *net.TCPConn) {
	raddr := tc.RemoteAddr()
	tc.SetKeepAlive(true)
	tc.SetKeepAlivePeriod(30 * time.Second)
	c := tls.Server(tc, p.config)
	err := c.Handshake()
	if err != nil {
		tc.Close()
		log.Printf("TLS handshake error from %v: %v", raddr, err)
		return
	}
	cs := c.ConnectionState()
	log.Printf("accepted %v for protocol %s on server %q", raddr, cs.NegotiatedProtocol, cs.ServerName)
	defer log.Printf("disconnected %v", raddr)
	hosts, ok := p.backends[cs.NegotiatedProtocol]
	b, ok := hosts[cs.ServerName]
	if !ok {
		log.Printf("unknown server %q for %v; falling back to default", cs.ServerName, raddr)
		b = hosts[""]
	}
	b.handle(tc, c)
}

type backend struct {
	Name string
	Addr string
}

// TODO What is the compare and swap stuff in tls.Conn.Close()?
func (b *backend) handle(tc1 *net.TCPConn, c1 *tls.Conn) {
	b.logf("accepted %v", tc1.RemoteAddr())
	c2, err := d.Dial("tcp", string(b.Addr))
	if err != nil {
		tc1.Close()
		b.log(err)
		return
	}
	tc2 := c2.(*net.TCPConn)
	done := make(chan struct{})
	go func() {
		_, err := io.Copy(c2, c1)
		if err != nil {
			b.log(err)
		}
		tc2.CloseWrite()
		tc1.CloseRead()
		close(done)
	}()
	_, err = io.Copy(c1, c2)
	if err != nil {
		b.log(err)
	}
	tc1.CloseWrite()
	tc2.CloseRead()
	<-done
}

func (b *backend) logf(format string, v ...interface{}) {
	log.Printf(b.Name+format, v...)
}

func (b *backend) log(err error) {
	log.Print(b.Name, err)
}
