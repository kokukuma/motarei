package proxy

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"time"

	"github.com/kazeburo/motarei/discovery"
)

const (
	bufferSize = 0xFFFF
)

// Proxy proxy struct
type Proxy struct {
	listen  string
	port    uint16
	timeout time.Duration
	d       *discovery.Discovery
	done    chan struct{}
}

// NewProxy create new proxy
func NewProxy(listen string, port uint16, timeout time.Duration, d *discovery.Discovery) *Proxy {
	return &Proxy{
		listen:  listen,
		port:    port,
		timeout: timeout,
		d:       d,
		done:    make(chan struct{}),
	}
}

// Start start new proxy
func (p *Proxy) Start(ctx context.Context) error {
	addr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("%s:%d", p.listen, p.port))
	if err != nil {
		return err
	}
	log.Printf("Start listen %s:%d", p.listen, p.port)
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return err
	}

	go func() {
		select {
		case <-ctx.Done():
			log.Printf("Go shutdown %s:%d", p.listen, p.port)
			l.Close()
		}
	}()

	for {
		conn, err := l.AcceptTCP()
		if err != nil {
			return err
		}
		conn.SetNoDelay(true)
		go p.handleConn(ctx, conn)
	}
}

func (p *Proxy) handleConn(ctx context.Context, c net.Conn) error {
	backends, err := p.d.Get(ctx, p.port)
	if err != nil {
		log.Printf("Failed to get backends: %v", err)
		c.Close()
		return err
	}

	if len(backends) == 0 {
		log.Printf("Failed to get backends port")
		c.Close()
		return fmt.Errorf("Failed to get backends port")
	}
	var s net.Conn
	for _, backend := range backends {
		// log.Printf("Proxy %s:%d => 127.0.0.1:%d (%s)", p.listen, p.port, backend.PublicPort, c.RemoteAddr())
		s, err = net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", backend.PublicPort), p.timeout)
		if err == nil {
			break
		} else {
			log.Printf("Failed to connect backend: %v", err)
		}
	}

	if err != nil {
		log.Printf("Giveup to connect backends: %v", err)
		c.Close()
		return err
	}

	doneCh := make(chan bool)
	goClose := false

	// client => upstream
	go func() {
		defer func() { doneCh <- true }()
		_, err := io.Copy(s, c)
		if err != nil {
			if !goClose {
				log.Printf("Copy from client: %v", err)
				return
			}
		}
		return
	}()

	// upstream => client
	go func() {
		defer func() { doneCh <- true }()
		_, err := io.Copy(c, s)
		if err != nil {
			if !goClose {
				log.Printf("Copy from upstream: %v", err)
				return
			}
		}
		return
	}()

	<-doneCh
	goClose = true
	s.Close()
	c.Close()
	<-doneCh
	return nil
}
