package main

import (
	"crypto/rand"
	"crypto/tls"
	"io"
	"net"
	"time"

	"github.com/nhooyr/log"
	"github.com/nhooyr/toml"

	"rsc.io/letsencrypt"
)

type proxy struct {
	// TODO should I have to mark these as map only?
	// TODO value tag for struct slices like Hosts for position
	// TODO map vs tablekey?
	Hosts []toml.NonEmptyString
	Email struct {
		Val toml.NonEmptyString
		*toml.Location
	} `toml:"optional"`
	Backends []*struct {
		Proto    string                         // TODO because of this, should not allow backends to not be tree
		Hosts    map[string]toml.NonEmptyString `toml:"optional"`
		Fallback toml.NonEmptyString
	} `toml:"optional"`
	Default struct {
		Hosts    map[string]toml.NonEmptyString `toml:"optional"`
		Fallback toml.NonEmptyString
	}

	// Map of protocols to serverNames to addresses
	backends map[string]map[string]*backend

	manager *letsencrypt.Manager
	config  *tls.Config
}

func (p *proxy) InitToml() error {
	p.manager = new(letsencrypt.Manager)
	p.config = &tls.Config{
		GetCertificate: p.manager.GetCertificate,
	}
	h := make([]string, len(p.Hosts))
	for i := range h {
		h[i] = string(p.Hosts[i])
	}
	p.manager.SetHosts(h)
	if p.Email == "" {
		err := p.manager.Register(string(p.Email.Val), func(_ string) bool {
			return true
		})
		if err != nil {
			return p.Email.WrapError(err)
		}
	}
	p.backends = make(map[string]map[string]*backend)
	p.backends[""] = make(map[string]*backend)
	for host, addr := range p.Default.Hosts {
		p.backends[""][host] = &backend{
			"default on " + host + ": ",
			string(addr),
		}
	}
	p.backends[""][""] = &backend{
		"default on fallback: ",
		string(p.Default.Fallback),
	}
	for _, b := range p.Backends {
		p.backends[b.Proto] = make(map[string]*backend)
		for host, addr := range b.Hosts {
			p.backends[b.Proto][host] = &backend{
				b.Proto + " on " + host,
				string(addr),
			}
		}
		p.backends[b.Proto][""] = &backend{
			b.Proto + " on fallback: ",
			string(b.Fallback),
		}

		p.config.NextProtos = append(p.config.NextProtos, string(b.Proto))
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
	keys := make([][32]byte, 1)
	if _, err := rand.Read(keys[0][:]); err != nil {
		return err
	}
	p.config.SetSessionTicketKeys(keys)
	go p.rotateSessionTicketKeys(keys)
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
		log.Printf("unknown server name %q for %v", cs.ServerName, raddr)
		b = hosts[""]
	}
	b.handle(tc, c)
}

type backend struct {
	name string
	addr string
}

// TODO What is the compare and swap stuff in tls.Conn.Close()?
func (b *backend) handle(tc1 *net.TCPConn, c1 *tls.Conn) {
	b.logf("accepted %v", tc1.RemoteAddr())
	c2, err := d.Dial("tcp", string(b.addr))
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
	log.Printf(b.name+format, v...)
}

func (b *backend) log(err error) {
	log.Print(b.name, err)
}
