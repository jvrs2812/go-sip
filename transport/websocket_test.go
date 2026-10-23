package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jvrs2812/go-sip/sip"
)

func TestWebSocketTransportNegotiatesSIPAndExchangesMessages(t *testing.T) {
	serverErrors := make(chan error, 1)
	upgrader := websocket.Upgrader{
		Subprotocols: []string{"sip"},
		CheckOrigin: func(_ *http.Request) bool {
			return true
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			serverErrors <- err

			return
		}

		defer connection.Close()

		messageType, payload, err := connection.ReadMessage()
		if err != nil {
			serverErrors <- err

			return
		}

		if messageType != websocket.TextMessage || connection.Subprotocol() != "sip" {
			serverErrors <- ErrMalformedWebSocketTestMessage

			return
		}

		requestMessage, err := sip.Parse(payload)
		if err != nil {
			serverErrors <- err

			return
		}

		response := sip.NewResponse(200, "OK")
		for _, header := range []string{"Via", "From", "To", "Call-ID", "CSeq"} {
			response.Headers.Add(header, requestMessage.Headers.Get(header))
		}

		responsePayload, err := response.MarshalBinary()
		if err != nil {
			serverErrors <- err

			return
		}

		if err := connection.WriteMessage(websocket.TextMessage, responsePayload); err != nil {
			serverErrors <- err

			return
		}

		serverErrors <- nil
	}))
	defer server.Close()

	remoteAddress := strings.TrimPrefix(server.URL, "http://")
	connection, err := Dial(context.Background(), Config{
		Network:       WS,
		RemoteAddr:    remoteAddress,
		WebSocketPath: "/sip",
		DialTimeout:   time.Second,
		WriteTimeout:  time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	defer connection.Close()

	request := sip.NewRequest("OPTIONS", "sip:service@example.test")
	request.Headers.Add("Via", "SIP/2.0/WS client.example.test;branch=z9hG4bKws")
	request.Headers.Add("From", "<sip:alice@example.test>;tag=1")
	request.Headers.Add("To", "<sip:service@example.test>")
	request.Headers.Add("Call-ID", "websocket-test")
	request.Headers.Add("CSeq", "1 OPTIONS")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := connection.Send(ctx, request); err != nil {
		t.Fatal(err)
	}

	select {
	case response := <-connection.Messages():
		if response == nil || response.StatusCode != 200 {
			t.Fatalf("unexpected WebSocket SIP response: %#v", response)
		}
	case err := <-connection.Errors():
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("timed out waiting for WebSocket SIP response")
	}

	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestSecureWebSocketTransportAndPing(t *testing.T) {
	pingReceived := make(chan struct{}, 1)
	serverErrors := make(chan error, 1)
	upgrader := websocket.Upgrader{
		Subprotocols: []string{"sip"},
		CheckOrigin: func(_ *http.Request) bool {
			return true
		},
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			serverErrors <- err

			return
		}

		defer connection.Close()

		connection.SetPingHandler(func(payload string) error {
			select {
			case pingReceived <- struct{}{}:
			default:
			}

			return connection.WriteControl(websocket.PongMessage, []byte(payload), time.Now().Add(time.Second))
		})

		_, _, err = connection.ReadMessage()
		serverErrors <- err
	}))
	defer server.Close()

	remoteAddress := strings.TrimPrefix(server.URL, "https://")
	tlsConfig := server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	connection, err := Dial(context.Background(), Config{
		Network:       WSS,
		RemoteAddr:    remoteAddress,
		TLSConfig:     tlsConfig,
		PingInterval:  10 * time.Millisecond,
		DialTimeout:   time.Second,
		WriteTimeout:  time.Second,
		WebSocketPath: "/sip",
	})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-pingReceived:
	case <-time.After(time.Second):
		t.Fatal("WSS ping was not received")
	}

	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}

	if err := <-serverErrors; err != nil && !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
		t.Fatal(err)
	}
}

func TestWebSocketRejectsMissingSIPSubprotocol(t *testing.T) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(_ *http.Request) bool {
			return true
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err == nil {
			defer connection.Close()
		}
	}))
	defer server.Close()

	_, err := Dial(context.Background(), Config{
		Network:      WS,
		RemoteAddr:   strings.TrimPrefix(server.URL, "http://"),
		DialTimeout:  time.Second,
		WriteTimeout: time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "sip subprotocol") {
		t.Fatalf("Dial() error = %v", err)
	}
}

func TestWebSocketEnforcesFrameLimit(t *testing.T) {
	upgrader := websocket.Upgrader{
		Subprotocols: []string{"sip"},
		CheckOrigin: func(_ *http.Request) bool {
			return true
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}

		defer connection.Close()

		_ = connection.WriteMessage(websocket.TextMessage, []byte(strings.Repeat("x", 1024)))
	}))
	defer server.Close()

	connection, err := Dial(context.Background(), Config{
		Network:      WS,
		RemoteAddr:   strings.TrimPrefix(server.URL, "http://"),
		DialTimeout:  time.Second,
		WriteTimeout: time.Second,
		Limits: sip.Limits{
			MaxStartLineBytes: 32,
			MaxHeaderBytes:    64,
			MaxHeaders:        8,
			MaxBodyBytes:      64,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	defer connection.Close()

	select {
	case err := <-connection.Errors():
		if err == nil || !strings.Contains(err.Error(), "read limit") {
			t.Fatalf("frame-limit error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for frame-limit error")
	}

}

var ErrMalformedWebSocketTestMessage = &webSocketTestError{}

type webSocketTestError struct{}

func (*webSocketTestError) Error() string {
	return "unexpected WebSocket message type or subprotocol"
}
