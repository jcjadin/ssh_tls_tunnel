package main

import (
	"crypto/tls"
	"io"
	"net"

	"github.com/nhooyr/log"
)

type server struct {
	name  string
	bind  string
	certs [][]string
	// Backends is a map of hostnames to a map of protocols to backends
	backends map[string]map[string]*backend
	config   *tls.Config
}

type backend struct {
	name string
	addr *net.TCPAddr
}

func (b *backend) handle(c1 *tls.Conn, c *net.TCPConn) {
	c2, err := net.DialTCP("tcp", nil, b.addr)
	if err != nil {
		c1.Close()
		log.Print(err)
		return
	}
	go func() {
		_, err := io.Copy(c2, c1)
		if err != nil {
			log.Print(err)
		}
		c2.CloseWrite()
		c.CloseRead()
	}()
	_, err = io.Copy(c1, c2)
	if err != nil {
		log.Print(err)
	}
	c.CloseWrite()
	c2.CloseRead()
}

func (s *server) serve(l net.TCPListener) {
	for {
		c, err := l.AcceptTCP()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

func (s *server) handle(c *net.TCPConn) {
	tc := tls.Server(c, s.config)
	err := tc.Handshake()
	if err != nil {
		c.Close()
		return
	}
	cs := tc.ConnectionState()
	protocols, ok := s.backends[cs.ServerName]
	if !ok {
		protocols, ok = s.backends[""]
		if !ok {
			tc.Close()
			return
		}
	}
	b, ok := protocols[cs.NegotiatedProtocol]
	if !ok {
		b, ok = protocols[""]
		if !ok {
			tc.Close()
			return
		}
	}
	b.handle(tc, c)
}
