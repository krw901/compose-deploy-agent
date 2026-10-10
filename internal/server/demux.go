package server

import (
	"bufio"
	"net"
	"sync"
	"time"
)

// tlsRecordTypeHandshake is the first byte of every TLS record that
// starts a handshake (RFC 8446 section 5.1). Any connection whose first
// byte is not this is treated as plain HTTP.
const tlsRecordTypeHandshake = 0x16

// sniffTimeout bounds how long protoDemux waits for a connection's first
// byte before giving up on it, so a client that opens a TCP connection and
// never sends anything cannot tie up a goroutine forever.
const sniffTimeout = 10 * time.Second

// protoDemux splits the single :8866 listener into two protocol streams
// so mTLS deploy traffic and unauthenticated `GET /v1/version` queries
// can share one port: pipelines need to
// learn the agent version, and thus which client binary to download,
// before they can possibly hold a client certificate for it. Detection
// peeks at each connection's first byte: TLS ClientHello records start
// with tlsRecordTypeHandshake; anything else is handed to the plain-HTTP
// sub-listener, whose router (publicRouter) exposes nothing but
// /v1/version.
type protoDemux struct {
	tls   *subListener
	plain *subListener
}

// newProtoDemux starts accepting from ln in the background and returns
// immediately; ln.Accept() errors (including from ln.Close()) close both
// sub-listeners so the callers' http.Server.Serve loops exit.
func newProtoDemux(ln net.Listener) *protoDemux {
	d := &protoDemux{
		tls:   newSubListener(ln.Addr()),
		plain: newSubListener(ln.Addr()),
	}
	go d.run(ln)
	return d
}

func (d *protoDemux) run(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			d.tls.Close()
			d.plain.Close()
			return
		}
		go d.dispatch(conn)
	}
}

// dispatch peeks at conn's first byte and routes it to the matching
// sub-listener. The peeked byte is preserved via peekedConn so the actual
// TLS handshake or HTTP request parsing downstream sees it again.
func (demux *protoDemux) dispatch(conn net.Conn) {
	if err := conn.SetReadDeadline(time.Now().Add(sniffTimeout)); err != nil {
		conn.Close()
		return
	}
	br := bufio.NewReader(conn)
	first, err := br.Peek(1)
	if err != nil {
		conn.Close()
		return
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		conn.Close()
		return
	}

	pc := &peekedConn{
		Conn: conn,
		r:    br,
	}
	if first[0] == tlsRecordTypeHandshake {
		demux.tls.deliver(pc)
	} else {
		demux.plain.deliver(pc)
	}
}

// peekedConn is a net.Conn whose first byte(s) were already consumed into
// a bufio.Reader during protocol sniffing; reads must go through that
// buffer so no bytes are lost.
type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// subListener is a net.Listener whose connections come from protoDemux
// instead of a real socket, so it can be handed directly to
// http.Server.Serve (and, for the TLS side, wrapped in tls.NewListener).
type subListener struct {
	addr   net.Addr
	connCh chan net.Conn
	stopCh chan struct{}
	once   sync.Once
}

func newSubListener(addr net.Addr) *subListener {
	return &subListener{
		addr:   addr,
		connCh: make(chan net.Conn),
		stopCh: make(chan struct{}),
	}
}

// deliver hands conn to a pending Accept call, or closes it if the
// listener has already been closed.
func (l *subListener) deliver(conn net.Conn) {
	select {
	case l.connCh <- conn:
	case <-l.stopCh:
		conn.Close()
	}
}

// Accept returns the next connection protoDemux routed to this listener.
func (l *subListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.connCh:
		return conn, nil
	case <-l.stopCh:
		return nil, net.ErrClosed
	}
}

// Close unblocks any in-flight Accept call. It is safe to call more than
// once (http.Server.Shutdown closes its listener, and protoDemux.run also
// closes both sub-listeners once the real listener errors out).
func (l *subListener) Close() error {
	l.once.Do(func() { close(l.stopCh) })
	return nil
}

// Addr reports the address of the real listener protoDemux is splitting.
func (l *subListener) Addr() net.Addr { return l.addr }
