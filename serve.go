package main

import (
	"crypto/tls"
	"io"
	"net"
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
	addr string
}

func (b *backend) handle(c1 net.Conn) {
	c2, err := net.Dial("tcp", b.addr)
	if err != nil {
		c1.Close()
		return
	}
	go copyClose(c1, c2)
	copyClose(c2, c1)
}

func copyClose(dst io.WriteCloser, src io.ReadCloser) {
	io.Copy(dst, src)
	dst.Close()
	src.Close()
}

func (s *server) serve(l net.Listener) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

func (s *server) handle(c net.Conn) {
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
	b.handle(tc)
}
