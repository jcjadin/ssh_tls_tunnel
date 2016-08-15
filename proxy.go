package main

// func (b *backend) handle(c1 *tls.Conn, c *net.TCPConn) {
// 	c2, err := net.DialTCP("tcp", nil, b.raddr)
// 	if err != nil {
// 		c1.Close()
// 		log.Print(err)
// 		return
// 	}
// 	go func() {
// 		_, err := io.Copy(c2, c1)
// 		if err != nil {
// 			log.Print(err)
// 		}
// 		c2.CloseWrite()
// 		c.CloseRead()
// 	}()
// 	go func() {
// 		_, err = io.Copy(c1, c2)
// 		if err != nil {
// 			log.Print(err)
// 		}
// 		c.CloseWrite()
// 		c2.CloseRead()
// 	}()
// }
//
// func (p *proxy) listenAndServe(laddr *net.TCPAddr) error {
// 	l, err := net.ListenTCP("tcp", laddr)
// 	if err != nil {
// 		return err
// 	}
// 	for {
// 		c, err := l.AcceptTCP()
// 		if err != nil {
// 			return err
// 		}
// 		go p.route(c)
// 	}
// }
//
// func (p *proxy) route(c *net.TCPConn) {
// 	tc := tls.Server(c, p.config)
// 	err := tc.Handshake()
// 	if err != nil {
// 		c.Close()
// 		return
// 	}
// 	cs := tc.ConnectionState()
// 	protocols, ok := p.backends[cs.ServerName]
// 	if !ok {
// 		protocols, ok = p.backends[""]
// 		if !ok {
// 			tc.Close()
// 			return
// 		}
// 	}
// 	b, ok := protocols[cs.NegotiatedProtocol]
// 	if !ok {
// 		b, ok = protocols[""]
// 		if !ok {
// 			tc.Close()
// 			return
// 		}
// 	}
// 	b.handle(tc, c)
// }
