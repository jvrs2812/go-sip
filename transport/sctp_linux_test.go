//go:build linux && !386

package transport

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ishidawataru/sctp"
	"github.com/jvrs2812/go-sip/sip"
)

func TestSCTPTransportExchangesSIPWhenKernelSupportsIt(t *testing.T) {
	listener, err := sctp.ListenSCTP("sctp", &sctp.SCTPAddr{
		IPAddrs: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}},
		Port:    0,
	})
	if err != nil {
		t.Skipf("SCTP is not available in this test environment: %v", err)
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

		request, parseErr := sip.ParseReader(bufio.NewReader(connection), sip.DefaultLimits())
		if parseErr != nil {
			serverErrors <- parseErr

			return
		}

		response := sip.NewResponse(200, "OK")
		response.Headers.Add("Call-ID", request.Headers.Get("Call-ID"))
		response.Headers.Add("CSeq", request.Headers.Get("CSeq"))
		payload, marshalErr := response.MarshalBinary()
		if marshalErr != nil {
			serverErrors <- marshalErr

			return
		}

		_, writeErr := connection.Write(payload)
		serverErrors <- writeErr
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	connection, err := Dial(ctx, Config{
		Network:      SCTP,
		RemoteAddr:   listener.Addr().String(),
		DialTimeout:  time.Second,
		WriteTimeout: time.Second,
	})
	if err != nil {
		if sctpUnavailable(err) {
			t.Skipf("SCTP association is not available in this test environment: %v", err)
		}

		t.Fatal(err)
	}

	defer connection.Close()

	request := sip.NewRequest("OPTIONS", "sip:sctp.example.test")
	request.Headers.Add("Call-ID", "sctp-test")
	request.Headers.Add("CSeq", "1 OPTIONS")

	if err := connection.Send(ctx, request); err != nil {
		if sctpUnavailable(err) {
			t.Skipf("SCTP data transfer is not available in this test environment: %v", err)
		}

		t.Fatal(err)
	}

	select {
	case response := <-connection.Messages():
		if response == nil || response.StatusCode != 200 {
			t.Fatalf("unexpected SCTP SIP response: %#v", response)
		}
	case err := <-connection.Errors():
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("timed out waiting for SCTP SIP response")
	}

	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func sctpUnavailable(err error) bool {
	return errors.Is(err, syscall.EOPNOTSUPP) ||
		errors.Is(err, syscall.EPROTONOSUPPORT) ||
		strings.Contains(strings.ToLower(err.Error()), "operation not supported")
}
