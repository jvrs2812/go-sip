package transport

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"

	"github.com/jvrs2812/go-sip/sip"
)

func TestStreamTransportReceivesConcatenatedMessages(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	transport := NewFromConn(clientConn, true, sip.DefaultLimits())
	t.Cleanup(func() { _ = transport.Close() })
	go func() {
		_, _ = serverConn.Write([]byte("SIP/2.0 100 Trying\r\nCall-ID: call\r\nCSeq: 1 INVITE\r\nContent-Length: 0\r\n\r\nSIP/2.0 200 OK\r\nCall-ID: call\r\nCSeq: 1 INVITE\r\nContent-Length: 0\r\n\r\n"))
	}()

	for _, expected := range []int{100, 200} {
		select {
		case message := <-transport.Messages():
			if message.StatusCode != expected {
				t.Fatalf("StatusCode = %d, want %d", message.StatusCode, expected)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for message")
		}
	}
}

func TestStreamTransportSendsCompleteMessage(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	transport := NewFromConn(clientConn, true, sip.DefaultLimits())
	t.Cleanup(func() { _ = transport.Close() })
	request := sip.NewRequest("OPTIONS", "sip:example.com")
	request.Headers.Add("Call-ID", "call")
	request.Headers.Add("CSeq", "1 OPTIONS")

	errCh := make(chan error, 1)
	go func() { errCh <- transport.Send(context.Background(), request) }()
	parsed, err := sip.ParseReader(bufio.NewReader(serverConn), sip.DefaultLimits())
	if err != nil {
		t.Fatalf("ParseReader() error = %v", err)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if parsed.Method != "OPTIONS" {
		t.Fatalf("Method = %q", parsed.Method)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	transport := NewFromConn(clientConn, true, sip.DefaultLimits())
	defer serverConn.Close()
	if err := transport.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	if err := transport.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}
