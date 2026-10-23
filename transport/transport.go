package transport

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jvrs2812/go-sip/sip"
)

type Network string

const (
	TCP  Network = "tcp"
	UDP  Network = "udp"
	TLS  Network = "tls"
	WS   Network = "ws"
	WSS  Network = "wss"
	SCTP Network = "sctp"
)

type Config struct {
	Network         Network
	RemoteAddr      string
	LocalAddr       string
	TLSConfig       *tls.Config
	WebSocketPath   string
	WebSocketHeader http.Header
	PingInterval    time.Duration
	DialTimeout     time.Duration
	WriteTimeout    time.Duration
	Limits          sip.Limits
}

type Transport interface {
	Send(context.Context, *sip.Message) error
	Messages() <-chan *sip.Message
	Errors() <-chan error
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
	Close() error
}

type connection struct {
	conn         net.Conn
	stream       bool
	limits       sip.Limits
	writeTimeout time.Duration
	messages     chan *sip.Message
	errors       chan error
	writeMu      sync.Mutex
	closeOnce    sync.Once
	done         chan struct{}
}

func Dial(ctx context.Context, config Config) (Transport, error) {
	if config.RemoteAddr == "" {
		return nil, errors.New("transport: remote address is required")
	}

	network := config.Network
	if network == "" {
		network = TCP
	}

	dialTimeout := config.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = 10 * time.Second
	}

	config.DialTimeout = dialTimeout

	dialer := &net.Dialer{Timeout: dialTimeout}
	if config.LocalAddr != "" && network != SCTP {
		localAddr, err := resolveLocalAddr(network, config.LocalAddr)
		if err != nil {
			return nil, err
		}

		dialer.LocalAddr = localAddr
	}

	var conn net.Conn
	var err error
	switch network {
	case TCP:
		conn, err = dialer.DialContext(ctx, "tcp", config.RemoteAddr)
	case UDP:
		conn, err = dialer.DialContext(ctx, "udp", config.RemoteAddr)
	case TLS:
		tlsConfig := config.TLSConfig
		if tlsConfig == nil {
			tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		} else {
			tlsConfig = tlsConfig.Clone()
			if tlsConfig.MinVersion == 0 {
				tlsConfig.MinVersion = tls.VersionTLS12
			}
		}

		if tlsConfig.ServerName == "" {
			host, _, splitErr := net.SplitHostPort(config.RemoteAddr)
			if splitErr == nil && net.ParseIP(host) == nil {
				tlsConfig.ServerName = host
			}
		}

		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", config.RemoteAddr)
	case WS, WSS:
		return dialWebSocket(ctx, config, dialer)
	case SCTP:
		return dialSCTP(ctx, config)
	default:
		return nil, fmt.Errorf("transport: unsupported network %q", network)
	}

	if err != nil {
		return nil, err
	}

	return newConnection(conn, network != UDP, config), nil
}

func NewFromConn(conn net.Conn, stream bool, limits sip.Limits) Transport {
	return newConnection(conn, stream, Config{Limits: limits})
}

func newConnection(conn net.Conn, stream bool, config Config) *connection {
	transport := &connection{
		conn:         conn,
		stream:       stream,
		limits:       config.Limits,
		writeTimeout: config.WriteTimeout,
		messages:     make(chan *sip.Message, 64),
		errors:       make(chan error, 8),
		done:         make(chan struct{}),
	}

	go transport.readLoop()
	return transport
}

func (t *connection) Send(ctx context.Context, message *sip.Message) error {
	payload, err := message.MarshalBinary()
	if err != nil {
		return err
	}

	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	select {
	case <-t.done:
		return net.ErrClosed
	default:
	}

	deadline := time.Time{}
	if contextDeadline, ok := ctx.Deadline(); ok {
		deadline = contextDeadline
	} else if t.writeTimeout > 0 {
		deadline = time.Now().Add(t.writeTimeout)
	}

	if err := t.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}

	if t.stream {
		for written := 0; written < len(payload); {
			count, writeErr := t.conn.Write(payload[written:])
			if writeErr != nil {
				return writeErr
			}

			if count == 0 {
				return io.ErrShortWrite
			}

			written += count
		}

		return nil
	}

	written, err := t.conn.Write(payload)
	if err != nil {
		return err
	}

	if written != len(payload) {
		return io.ErrShortWrite
	}

	return nil
}

func (t *connection) Messages() <-chan *sip.Message {
	return t.messages
}

func (t *connection) Errors() <-chan error {
	return t.errors
}

func (t *connection) LocalAddr() net.Addr {
	return t.conn.LocalAddr()
}

func (t *connection) RemoteAddr() net.Addr {
	return t.conn.RemoteAddr()
}

func (t *connection) Close() error {
	var err error
	t.closeOnce.Do(func() {
		close(t.done)
		err = t.conn.Close()
	})
	return err
}

func (t *connection) readLoop() {
	defer close(t.messages)
	defer close(t.errors)

	if t.stream {
		t.readStream()

		return
	}

	t.readDatagrams()
}

func (t *connection) readStream() {
	reader := bufio.NewReader(t.conn)
	for {
		message, err := sip.ParseReader(reader, t.limits)
		if err != nil {
			if !isExpectedClose(err, t.done) {
				t.report(err)
			}

			return
		}

		select {
		case t.messages <- message:
		case <-t.done:
			return
		}
	}
}

func (t *connection) readDatagrams() {
	limits := t.limits
	if limits.MaxBodyBytes <= 0 {
		limits = sip.DefaultLimits()
	}

	maximum := limits.MaxStartLineBytes + limits.MaxHeaderBytes + limits.MaxBodyBytes + 4
	buffer := make([]byte, maximum+1)
	for {
		count, err := t.conn.Read(buffer)
		if err != nil {
			if !isExpectedClose(err, t.done) {
				t.report(err)
			}

			return
		}

		if count > maximum {
			t.report(sip.ErrMessageTooLarge)
			continue
		}

		message, err := sip.Parse(buffer[:count])
		if err != nil {
			t.report(err)
			continue
		}

		select {
		case t.messages <- message:
		case <-t.done:
			return
		}
	}
}

func (t *connection) report(err error) {
	select {
	case t.errors <- err:
	default:
	}
}

func resolveLocalAddr(network Network, address string) (net.Addr, error) {
	switch network {
	case UDP:
		return net.ResolveUDPAddr("udp", address)
	case TCP, TLS:
		return net.ResolveTCPAddr("tcp", address)
	case WS, WSS:
		return net.ResolveTCPAddr("tcp", address)
	case SCTP:
		return nil, errors.New("transport: SCTP local address is resolved by the platform backend")
	default:
		return nil, fmt.Errorf("transport: unsupported network %q", network)
	}
}

func isExpectedClose(err error, done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
	}

	return errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || strings.Contains(strings.ToLower(err.Error()), "closed network connection")
}
