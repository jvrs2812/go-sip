package client

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jvrs2812/go-sip/sip"
)

func TestTransferTracksReferNotifications(t *testing.T) {
	sipClient, call, sent := transferTestClient(t, "transfer-call")
	defer sipClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result := make(chan struct {
		transfer *Transfer
		err      error
	}, 1)

	go func() {
		transfer, err := call.Transfer(ctx, "sip:3003@example.test")
		result <- struct {
			transfer *Transfer
			err      error
		}{transfer: transfer, err: err}
	}()

	refer := <-sent
	if refer.Method != "REFER" || refer.Headers.Get("Refer-To") != "<sip:3003@example.test>" {
		t.Fatalf("REFER request = %#v", refer)
	}

	sipClient.dispatchResponse(responseForTest(refer, 202, "Accepted"))

	created := <-result
	if created.err != nil {
		t.Fatalf("Transfer() error = %v", created.err)
	}

	provisional := transferNotify("transfer-call", "1 NOTIFY", "z9hG4bK-refer-one", "active;expires=60", "SIP/2.0 100 Trying\r\n")
	sipClient.handleNOTIFY(provisional)

	if response := <-sent; response.StatusCode != 200 {
		t.Fatalf("provisional NOTIFY response = %d", response.StatusCode)
	}

	if created.transfer.Status() != TransferActive || created.transfer.ResponseCode() != 100 {
		t.Fatalf("provisional transfer state = %q, code = %d", created.transfer.Status(), created.transfer.ResponseCode())
	}

	completed := transferNotify("transfer-call", "2 NOTIFY", "z9hG4bK-refer-two", "terminated;reason=noresource", "SIP/2.0 200 OK\r\n")
	sipClient.handleNOTIFY(completed)

	if response := <-sent; response.StatusCode != 200 {
		t.Fatalf("terminal NOTIFY response = %d", response.StatusCode)
	}

	if created.transfer.Status() != TransferSucceeded || created.transfer.ResponseCode() != 200 {
		t.Fatalf("completed transfer state = %q, code = %d", created.transfer.Status(), created.transfer.ResponseCode())
	}

	sipClient.mu.Lock()
	_, tracked := sipClient.transfers[call.ID]
	sipClient.mu.Unlock()

	if tracked {
		t.Fatal("completed transfer remains tracked")
	}
}

func TestTransferRejectsMalformedNotification(t *testing.T) {
	sipClient, call, sent := transferTestClient(t, "malformed-transfer")
	defer sipClient.Close()
	transfer := newTransfer(call, "sip:3003@example.test")

	sipClient.mu.Lock()
	sipClient.transfers[call.ID] = transfer
	sipClient.mu.Unlock()

	wrongType := transferNotify(call.ID, "1 NOTIFY", "z9hG4bK-wrong-type", "active;expires=60", "SIP/2.0 100 Trying\r\n")
	wrongType.Headers.Set("Content-Type", "text/plain")
	sipClient.handleNOTIFY(wrongType)

	if response := <-sent; response.StatusCode != 415 {
		t.Fatalf("wrong content type response = %d", response.StatusCode)
	}

	malformed := transferNotify(call.ID, "2 NOTIFY", "z9hG4bK-malformed", "active;expires=60", "not a sipfrag")
	sipClient.handleNOTIFY(malformed)

	if response := <-sent; response.StatusCode != 400 {
		t.Fatalf("malformed sipfrag response = %d", response.StatusCode)
	}
}

func TestAttendedTransferAddsReplaces(t *testing.T) {
	sipClient, call, sent := transferTestClient(t, "attended-source")
	defer sipClient.Close()
	replaced := &Call{
		client:       sipClient,
		ID:           "replaced@dialog",
		LocalTag:     "local-replaced",
		RemoteTag:    "remote-replaced",
		RemoteTarget: "sip:4004@example.test",
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result := make(chan error, 1)
	go func() {
		_, err := call.AttendedTransfer(ctx, replaced)
		result <- err
	}()

	refer := <-sent
	referTo := refer.Headers.Get("Refer-To")
	if !strings.Contains(referTo, "Replaces=replaced%40dialog%3Bto-tag%3Dremote-replaced%3Bfrom-tag%3Dlocal-replaced") {
		t.Fatalf("attended Refer-To = %q", referTo)
	}

	sipClient.dispatchResponse(responseForTest(refer, 202, "Accepted"))

	if err := <-result; err != nil {
		t.Fatalf("AttendedTransfer() error = %v", err)
	}
}

func transferTestClient(t *testing.T, callID string) (*Client, *Call, chan *sip.Message) {
	t.Helper()

	sipClient, err := NewClient(Config{ServerHost: "example.test", Username: "1001"})
	if err != nil {
		t.Fatal(err)
	}

	sent := make(chan *sip.Message, 8)
	sipClient.mu.Lock()
	sipClient.conn = &addressTransport{
		local:    &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 5060},
		remote:   &net.TCPAddr{IP: net.ParseIP("192.0.2.20"), Port: 5060},
		messages: make(chan *sip.Message),
		errors:   make(chan error),
		sent:     sent,
	}
	sipClient.mu.Unlock()

	call := &Call{
		client:       sipClient,
		ID:           callID,
		LocalTag:     "local",
		RemoteTag:    "remote",
		LocalURI:     "<sip:1001@example.test>;tag=local",
		RemoteURI:    "<sip:2002@example.test>;tag=remote",
		RemoteTarget: "sip:2002@192.0.2.20",
	}

	return sipClient, call, sent
}

func transferNotify(callID, cseq, branch, state, body string) *sip.Message {
	request := notifyForTest(callID, "refer", state, "remote", "local")
	request.Headers.Set("Via", "SIP/2.0/TCP server.example.test;branch="+branch)
	request.Headers.Set("CSeq", cseq)
	request.Headers.Set("Content-Type", "message/sipfrag;version=2.0")
	request.Body = []byte(body)

	return request
}
