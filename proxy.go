package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"rsc.io/letsencrypt"
)

type proxy struct {
	Hostnames []string
	Email     string `toml:"optional"`
	Fallback  *backend
	Backends  []*filteredBackend `toml:"optional"`

	// Map of protocols to servernames
	protocols map[string]map[string]*backend
	config    *tls.Config
	manager   *letsencrypt.Manager
}

func (p *proxy) InitHostnames() error {
	p.manager = new(letsencrypt.Manager)
	p.manager.SetHosts(p.Hostnames)
	p.config = &tls.Config{
		GetCertificate: p.manager.GetCertificate,
	}
	return nil
}

func (p *proxy) InitEmail() error {
	p.manager.Register(p.Email, func(_ string) bool {
		return true
	})
	return nil
}

func (p *proxy) InitFallback() error {
	p.protocols = make(map[string]map[string]*backend)
	p.protocols[""] = make(map[string]*backend)
	p.protocols[""][""] = p.Fallback
	return nil
}

type filteredBackend struct {
	*backend
	Protocols   []string `toml:"optional"`
	ServerNames []string `toml:"optional"`
}

func (fb *filteredBackend) Init() error {
	if len(fb.Protocols) == 0 && len(fb.ServerNames) == 0 {
		return errors.New("at least protocols or serverNames must be present")
	}
	if len(fb.Protocols) == 0 {
		fb.Protocols = append(fb.Protocols, "")
	} else if len(fb.ServerNames) == 0 {
		fb.ServerNames = append(fb.ServerNames, "")
	}
	return nil
}

func (p *proxy) InitBackends() error {
	for _, fb := range p.Backends {
		for _, proto := range fb.Protocols {
			servers, ok := p.protocols[proto]
			if !ok {
				servers = make(map[string]*backend)
				p.protocols[proto] = servers
			}
			for _, name := range fb.ServerNames {
				servers[name] = fb.backend
			}
		}
	}
	for proto, servers := range p.protocols {
		if proto != "" {
			p.config.NextProtos = append(p.config.NextProtos, proto)
		}
		found := false
		for name, _ := range servers {
			if name == "" {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("no default server backend for protocol %s", proto)
		}
	}
	return nil
}

func (p *proxy) listenAndServe() error {
	laddr, err := net.ResolveTCPAddr("tcp", ":https")
	if err != nil {
		return err
	}
	l, err := net.ListenTCP("tcp", laddr)
	if err != nil {
		return err
	}
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
				logger.Print("%v; retrying in %v", err, tempDelay)
				time.Sleep(tempDelay)
				continue
			}
			return err
		}
		tempDelay = 0
		go p.handle(c)
	}
}

func (p *proxy) handle(tc *net.TCPConn) {
	raddr := tc.RemoteAddr()
	tc.SetKeepAlive(true)
	tc.SetKeepAlivePeriod(30 * time.Second)
	c := tls.Server(tc, p.config)
	err := c.Handshake()
	if err != nil {
		tc.Close()
		logger.Printf("TLS handshake error from %v: %v", raddr, err)
		return
	}
	cs := c.ConnectionState()
	logger.Printf("accepted %v for protocol %q on server %q", raddr, cs.NegotiatedProtocol, cs.ServerName)
	defer logger.Printf("disconnected %v", raddr)
	servers, ok := p.protocols[cs.NegotiatedProtocol]
	if !ok {
		logger.Printf("unknown protocol %q for %v", cs.NegotiatedProtocol, raddr)
		servers = p.protocols[""]
	}
	b, ok := servers[cs.ServerName]
	if !ok {
		logger.Printf("unknown server name %q for %v", cs.ServerName, raddr)
		b = servers[""]
	}
	b.handle(c, tc)
}

type backend struct {
	Name string
	Addr string
}

func (b *backend) InitName() error {
	b.Name += ": "
	return nil
}

// TODO optimize.
var d = &net.Dialer{
	Timeout:   10 * time.Second,
	KeepAlive: 30 * time.Second,
	DualStack: true,
}

// TODO What is the compare and swap stuff in tls.Conn.Close()?
func (b *backend) handle(c1 *tls.Conn, tc1 *net.TCPConn) {
	b.logf("accepted %v", tc1.RemoteAddr())
	c2, err := d.Dial("tcp", b.Addr)
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
	logger.Printf(b.Name+format, v...)
}

func (b *backend) log(err error) {
	logger.Print(b.Name, err)
}
