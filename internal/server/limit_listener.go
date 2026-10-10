package server

import (
	"net"
	"sync"
)

// DefaultMaxConnections is the default cap on concurrently open
// connections to the main :8866 listener.
const DefaultMaxConnections = 64

// limitListener caps how many accepted connections may be open at once.
// Once the cap is reached Accept blocks until a connection is closed, so
// further clients wait in the kernel backlog instead of each costing a
// goroutine and buffers. The cap is global, not per source IP: behind a
// TCP passthrough proxy every connection shares the proxy's address.
type limitListener struct {
	net.Listener
	sem       chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func newLimitListener(ln net.Listener, n int) *limitListener {
	return &limitListener{
		Listener: ln,
		sem:      make(chan struct{}, n),
		done:     make(chan struct{}),
	}
}

// Accept waits for a free slot, then accepts the next connection. The
// slot is released when that connection is closed.
func (l *limitListener) Accept() (net.Conn, error) {
	select {
	case l.sem <- struct{}{}:
	case <-l.done:
		return nil, net.ErrClosed
	}
	conn, err := l.Listener.Accept()
	if err != nil {
		<-l.sem
		return nil, err
	}
	return &limitConn{
		Conn:    conn,
		release: sync.OnceFunc(func() { <-l.sem }),
	}, nil
}

// Close stops the underlying listener and unblocks a waiting Accept.
func (l *limitListener) Close() error {
	l.closeOnce.Do(func() { close(l.done) })
	return l.Listener.Close()
}

// limitConn releases its limitListener slot exactly once on Close.
type limitConn struct {
	net.Conn
	release func()
}

func (c *limitConn) Close() error {
	err := c.Conn.Close()
	c.release()
	return err
}
