package main

import (
	"crypto/tls"
	"io"
	"net"
	"time"

	"github.com/nhooyr/toml"

	"rsc.io/letsencrypt"
)

// TODO renaming
type proxy struct {
	Hostnames []toml.NonEmptyString
	Email     toml.NonEmptyString `toml:"optional"`
	Fallback  *proto
	Backends  map[string]*backend
	Protos    map[string]*proto `toml:"optional"`

	config  *tls.Config
	manager *letsencrypt.Manager
	// Map of protocols to serverNames to backends
	protos map[string]map[string]*backend
}

type proto struct {
	Hosts   map[string]*backend `toml:"optional"`
	Default *backend
}

func (p *proxy) InitHostnames() error {
	p.manager = new(letsencrypt.Manager)
	h := make([]string, len(p.Hostnames))
	for i := range h {
		h[i] = string(p.Hostnames[i])
	}
	p.manager.SetHosts(h)
	p.config = &tls.Config{
		GetCertificate: p.manager.GetCertificate,
	}
	return nil
}

func (p *proxy) InitEmail() error {
	p.manager.Register(string(p.Email), func(_ string) bool {
		return true
	})
	return nil
}

func (p *proxy) InitFallback() error {
	p.protos = make(map[string]map[string]*backend)
	p.protos[""] = make(map[string]*backend)
	for host, b := range p.Fallback.Hosts {
		p.protos[""][host] = b
	}
	p.protos[""][""] = p.Fallback.Default
	return nil
}

func (p *proxy) InitProtos() error {
	for name, proto := range p.Protos {
		p.protos[name] = make(map[string]*backend)
		for host, b := range proto.Hosts {
			p.protos[name][host] = b
		}
		p.protos[name][""] = proto.Default
		p.config.NextProtos = append(p.config.NextProtos, string(name))
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
	servers, ok := p.protos[cs.NegotiatedProtocol]
	if !ok {
		logger.Printf("unknown protocol %q for %v", cs.NegotiatedProtocol, raddr)
		servers = p.protos[""]
	}
	b, ok := servers[cs.ServerName]
	if !ok {
		logger.Printf("unknown server name %q for %v", cs.ServerName, raddr)
		b = servers[""]
	}
	b.handle(c, tc)
}

type backend struct {
	Name toml.NonEmptyString
	Addr toml.NonEmptyString
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
	logger.Printf(string(b.Name)+format, v...)
}

func (b *backend) log(err error) {
	logger.Print(b.Name, err)
}
