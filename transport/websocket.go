package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jvrs2812/go-sip/sip"
)

type webSocketTransport struct {
	connection   *websocket.Conn
	limits       sip.Limits
	writeTimeout time.Duration
	pingInterval time.Duration
	messages     chan *sip.Message
	errors       chan error
	done         chan struct{}
	writeMu      sync.Mutex
	closeOnce    sync.Once
}

func dialWebSocket(ctx context.Context, config Config, networkDialer *net.Dialer) (Transport, error) {
	path := strings.TrimSpace(config.WebSocketPath)
	if path == "" {
		path = "/"
	} else if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	scheme := string(config.Network)
	endpoint := url.URL{Scheme: scheme, Host: config.RemoteAddr, Path: path}

	tlsConfig := config.TLSConfig
	if config.Network == WSS {
		if tlsConfig == nil {
			tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		} else {
			tlsConfig = tlsConfig.Clone()
			if tlsConfig.MinVersion == 0 {
				tlsConfig.MinVersion = tls.VersionTLS12
			}
		}

		if tlsConfig.ServerName == "" {
			host, _, err := net.SplitHostPort(config.RemoteAddr)
			if err == nil && net.ParseIP(host) == nil {
				tlsConfig.ServerName = host
			}
		}
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: config.DialTimeout,
		Subprotocols:     []string{"sip"},
		TLSClientConfig:  tlsConfig,
		NetDialContext:   networkDialer.DialContext,
	}

	header := cloneHeader(config.WebSocketHeader)
	connection, response, err := dialer.DialContext(ctx, endpoint.String(), header)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}

	if err != nil {
		return nil, fmt.Errorf("dial SIP WebSocket: %w", err)
	}

	if connection.Subprotocol() != "sip" {
		_ = connection.Close()

		return nil, errors.New("transport: WebSocket peer did not negotiate the sip subprotocol")
	}

	transport := &webSocketTransport{
		connection:   connection,
		limits:       config.Limits,
		writeTimeout: config.WriteTimeout,
		pingInterval: config.PingInterval,
		messages:     make(chan *sip.Message, 64),
		errors:       make(chan error, 8),
		done:         make(chan struct{}),
	}

	go transport.readLoop()

	if transport.pingInterval > 0 {
		go transport.pingLoop()
	}

	return transport, nil
}

func (t *webSocketTransport) Send(ctx context.Context, message *sip.Message) error {
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

	if err := t.connection.SetWriteDeadline(deadline); err != nil {
		return err
	}

	if err := t.connection.WriteMessage(websocket.TextMessage, payload); err != nil {
		return fmt.Errorf("write SIP WebSocket message: %w", err)
	}

	return nil
}

func (t *webSocketTransport) Messages() <-chan *sip.Message {
	return t.messages
}

func (t *webSocketTransport) Errors() <-chan error {
	return t.errors
}

func (t *webSocketTransport) LocalAddr() net.Addr {
	return t.connection.LocalAddr()
}

func (t *webSocketTransport) RemoteAddr() net.Addr {
	return t.connection.RemoteAddr()
}

func (t *webSocketTransport) Close() error {
	var closeErr error

	t.closeOnce.Do(func() {
		close(t.done)

		t.writeMu.Lock()
		_ = t.connection.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second),
		)
		closeErr = t.connection.Close()
		t.writeMu.Unlock()
	})

	return closeErr
}

func (t *webSocketTransport) readLoop() {
	defer close(t.messages)
	defer close(t.errors)

	limits := t.limits
	if limits.MaxBodyBytes <= 0 {
		limits = sip.DefaultLimits()
	}

	maximum := int64(limits.MaxStartLineBytes + limits.MaxHeaderBytes + limits.MaxBodyBytes + 4)
	t.connection.SetReadLimit(maximum)

	for {
		messageType, payload, err := t.connection.ReadMessage()
		if err != nil {
			if !t.expectedClose(err) {
				t.report(fmt.Errorf("read SIP WebSocket message: %w", err))
			}

			return
		}

		if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
			continue
		}

		message, err := sip.Parse(payload)
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

func (t *webSocketTransport) pingLoop() {
	ticker := time.NewTicker(t.pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			deadline := time.Now().Add(5 * time.Second)
			if t.writeTimeout > 0 {
				deadline = time.Now().Add(t.writeTimeout)
			}

			t.writeMu.Lock()
			err := t.connection.WriteControl(websocket.PingMessage, nil, deadline)
			t.writeMu.Unlock()

			if err != nil {
				t.report(fmt.Errorf("write SIP WebSocket ping: %w", err))

				return
			}
		case <-t.done:
			return
		}
	}
}

func (t *webSocketTransport) report(err error) {
	select {
	case t.errors <- err:
	default:
	}
}

func (t *webSocketTransport) expectedClose(err error) bool {
	select {
	case <-t.done:
		return true
	default:
	}

	return websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || errors.Is(err, net.ErrClosed)
}

func cloneHeader(source http.Header) http.Header {
	if source == nil {
		return nil
	}

	return source.Clone()
}
