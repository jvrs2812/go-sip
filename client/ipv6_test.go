package client

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jvrs2812/go-sip/media"
	"github.com/jvrs2812/go-sip/sdp"
	"github.com/jvrs2812/go-sip/sip"
)

func TestIPv6SignalingSDPAndRTP(t *testing.T) {
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback is unavailable: %v", err)
	}

	defer listener.Close()

	serverErrors := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErrors <- acceptErr

			return
		}

		defer connection.Close()

		reader := bufio.NewReader(connection)
		register, parseErr := sip.ParseReader(reader, sip.DefaultLimits())
		if parseErr != nil {
			serverErrors <- parseErr

			return
		}

		if !strings.Contains(register.Headers.Get("Via"), "[::1]") || !strings.Contains(register.Headers.Get("Contact"), "[::1]") {
			serverErrors <- &ipv6AssertionError{via: register.Headers.Get("Via"), contact: register.Headers.Get("Contact")}

			return
		}

		if writeErr := writeMessage(connection, responseForTest(register, 200, "OK")); writeErr != nil {
			serverErrors <- writeErr

			return
		}

		unregister, parseErr := sip.ParseReader(reader, sip.DefaultLimits())
		if parseErr != nil {
			serverErrors <- parseErr

			return
		}

		serverErrors <- writeMessage(connection, responseForTest(unregister, 200, "OK"))
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}

	sipClient, err := NewClient(Config{
		ServerHost:         host,
		ServerPort:         port,
		LocalAddress:       "[::1]:0",
		Username:           "1001",
		TransactionTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	defer sipClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := sipClient.Register(ctx); err != nil {
		t.Fatalf("IPv6 Register() error = %v", err)
	}

	if err := sipClient.Unregister(ctx); err != nil {
		t.Fatalf("IPv6 Unregister() error = %v", err)
	}

	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}

	first, err := media.NewRTPSession(media.RTPConfig{LocalAddr: "[::1]:0", SSRC: 1})
	if err != nil {
		t.Fatalf("open first IPv6 RTP session: %v", err)
	}

	defer first.Close()

	second, err := media.NewRTPSession(media.RTPConfig{LocalAddr: "[::1]:0", SSRC: 2})
	if err != nil {
		t.Fatalf("open second IPv6 RTP session: %v", err)
	}

	defer second.Close()

	if err := first.SetRemote(second.LocalAddr().String()); err != nil {
		t.Fatal(err)
	}

	if err := first.Send(ctx, []byte("ipv6"), true); err != nil {
		t.Fatal(err)
	}

	select {
	case packet := <-second.Packets():
		if string(packet.Payload) != "ipv6" {
			t.Fatalf("IPv6 RTP payload = %q", packet.Payload)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for IPv6 RTP")
	}

	description := sdp.Description{
		OriginID:      1,
		OriginVersion: 1,
		SessionName:   "ipv6",
		Connection:    net.ParseIP("::1"),
		Media: []sdp.Media{{
			Type:       "audio",
			Port:       4000,
			Protocol:   "RTP/AVP",
			Formats:    []uint8{0},
			Connection: net.ParseIP("::1"),
			Direction:  sdp.SendRecv,
			Codecs: map[uint8]sdp.Codec{
				0: {PayloadType: 0, Name: "PCMU", ClockRate: 8000, Channels: 1},
			},
		}},
	}

	encoded, err := description.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(encoded), "IN IP6 ::1") {
		t.Fatalf("IPv6 SDP = %q", encoded)
	}

	parsed, err := sdp.Parse(encoded)
	if err != nil || !parsed.Connection.Equal(net.ParseIP("::1")) {
		t.Fatalf("parsed IPv6 SDP = %#v, error = %v", parsed, err)
	}
}

type ipv6AssertionError struct {
	via     string
	contact string
}

func (e *ipv6AssertionError) Error() string {
	return "IPv6 Via/Contact were not bracketed: Via=" + strconv.Quote(e.via) + ", Contact=" + strconv.Quote(e.contact)
}
