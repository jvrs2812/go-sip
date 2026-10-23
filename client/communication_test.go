package client

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jvrs2812/go-sip/sip"
)

func TestInboundMessageValidation(t *testing.T) {
	sipClient, err := NewClient(Config{
		ServerHost:          "example.test",
		Username:            "1001",
		MaxMessageBodyBytes: 4,
		AcceptedMessageTypes: []string{
			"text/plain",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	sent := make(chan *sip.Message, 4)
	sipClient.mu.Lock()
	sipClient.conn = &addressTransport{
		local:    &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 5060},
		remote:   &net.TCPAddr{IP: net.ParseIP("192.0.2.20"), Port: 5060},
		messages: make(chan *sip.Message),
		errors:   make(chan error),
		sent:     sent,
	}
	sipClient.mu.Unlock()

	tooLarge := communicationRequest("MESSAGE", "message-large")
	tooLarge.Headers.Add("Content-Type", "text/plain")
	tooLarge.Body = []byte("large")
	sipClient.handleMESSAGE(tooLarge)

	if response := <-sent; response.StatusCode != 413 {
		t.Fatalf("large MESSAGE response = %d", response.StatusCode)
	}

	unsupported := communicationRequest("MESSAGE", "message-unsupported")
	unsupported.Headers.Add("Content-Type", "application/octet-stream")
	unsupported.Body = []byte("data")
	sipClient.handleMESSAGE(unsupported)

	if response := <-sent; response.StatusCode != 415 || response.Headers.Get("Accept") != "text/plain" {
		t.Fatalf("unsupported MESSAGE response = %#v", response)
	}

	withoutHandler := communicationRequest("MESSAGE", "message-unhandled")
	withoutHandler.Headers.Add("Content-Type", "text/plain; charset=utf-8")
	withoutHandler.Body = []byte("ok")
	sipClient.handleMESSAGE(withoutHandler)

	if response := <-sent; response.StatusCode != 501 {
		t.Fatalf("unhandled MESSAGE response = %d", response.StatusCode)
	}

	received := make(chan MessageEvent, 1)
	sipClient.OnInstantMessage = func(_ *Client, event MessageEvent) {
		received <- event
	}

	accepted := communicationRequest("MESSAGE", "message-accepted")
	accepted.Headers.Add("Content-Type", "text/plain; charset=utf-8")
	accepted.Body = []byte("ok")
	sipClient.handleMESSAGE(accepted)

	if response := <-sent; response.StatusCode != 200 {
		t.Fatalf("accepted MESSAGE response = %d", response.StatusCode)
	}

	select {
	case event := <-received:
		if string(event.Body) != "ok" {
			t.Fatalf("MESSAGE callback body = %q", event.Body)
		}
	case <-time.After(time.Second):
		t.Fatal("MESSAGE callback was not invoked")
	}
}

func TestInboundInfoValidatesDialogAndMediaType(t *testing.T) {
	sipClient, err := NewClient(Config{
		ServerHost:        "example.test",
		Username:          "1001",
		AcceptedInfoTypes: []string{"application/dtmf-relay"},
	})
	if err != nil {
		t.Fatal(err)
	}

	sent := make(chan *sip.Message, 2)
	sipClient.mu.Lock()
	sipClient.conn = &addressTransport{
		local:    &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 5060},
		remote:   &net.TCPAddr{IP: net.ParseIP("192.0.2.20"), Port: 5060},
		messages: make(chan *sip.Message),
		errors:   make(chan error),
		sent:     sent,
	}
	sipClient.calls["info-call"] = &Call{
		client:    sipClient,
		ID:        "info-call",
		LocalTag:  "local",
		RemoteTag: "remote",
	}
	sipClient.mu.Unlock()

	sipClient.OnInfo = func(_ *Client, _ InfoEvent) {}

	request := communicationRequest("INFO", "info-call")
	request.Headers.Set("From", "<sip:2002@example.test>;tag=remote")
	request.Headers.Set("To", "<sip:1001@example.test>;tag=local")
	request.Headers.Add("Content-Type", "application/media_control+xml")
	sipClient.handleINFO(request)

	response := <-sent
	if response.StatusCode != 415 || response.Headers.Get("Accept") != "application/dtmf-relay" {
		t.Fatalf("unsupported INFO response = %#v", response)
	}
}

func TestSendDTMFInfo(t *testing.T) {
	sipClient, err := NewClient(Config{ServerHost: "example.test", Username: "1001"})
	if err != nil {
		t.Fatal(err)
	}

	sent := make(chan *sip.Message, 1)
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
		ID:           "dtmf-info-call",
		LocalTag:     "local",
		RemoteTag:    "remote",
		LocalURI:     "<sip:1001@example.test>;tag=local",
		RemoteURI:    "<sip:2002@example.test>;tag=remote",
		RemoteTarget: "sip:2002@192.0.2.20",
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result := make(chan error, 1)
	go func() {
		result <- call.SendDTMFInfo(ctx, '#', 250*time.Millisecond)
	}()

	request := <-sent
	if request.Method != "INFO" || request.Headers.Get("Content-Type") != "application/dtmf-relay" {
		t.Fatalf("DTMF INFO request = %#v", request)
	}

	if string(request.Body) != "Signal=#\r\nDuration=250\r\n" {
		t.Fatalf("DTMF INFO body = %q", request.Body)
	}

	sipClient.dispatchResponse(responseForTest(request, 200, "OK"))

	if err := <-result; err != nil {
		t.Fatalf("SendDTMFInfo() error = %v", err)
	}

	if err := call.SendDTMFInfo(ctx, 'Z', 250*time.Millisecond); err == nil || !strings.Contains(err.Error(), "invalid DTMF") {
		t.Fatalf("invalid DTMF error = %v", err)
	}
}

func TestInboundPublishVersionLifecycle(t *testing.T) {
	sipClient, err := NewClient(Config{ServerHost: "example.test", Username: "1001"})
	if err != nil {
		t.Fatal(err)
	}

	sent := make(chan *sip.Message, 5)
	sipClient.mu.Lock()
	sipClient.conn = &addressTransport{
		local:    &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 5060},
		remote:   &net.TCPAddr{IP: net.ParseIP("192.0.2.20"), Port: 5060},
		messages: make(chan *sip.Message),
		errors:   make(chan error),
		sent:     sent,
	}
	sipClient.mu.Unlock()

	initial := publishRequest("publish-initial", "", "60", []byte("presence"))
	sipClient.handlePUBLISH(initial)

	created := <-sent
	entityTag := created.Headers.Get("SIP-ETag")
	if created.StatusCode != 200 || entityTag == "" || created.Headers.Get("Expires") != "60" {
		t.Fatalf("initial PUBLISH response = %#v", created)
	}

	stale := publishRequest("publish-stale", "stale-version", "60", nil)
	sipClient.handlePUBLISH(stale)

	if response := <-sent; response.StatusCode != 412 {
		t.Fatalf("stale PUBLISH response = %d", response.StatusCode)
	}

	refresh := publishRequest("publish-refresh", entityTag, "120", nil)
	sipClient.handlePUBLISH(refresh)

	refreshed := <-sent
	refreshedTag := refreshed.Headers.Get("SIP-ETag")
	if refreshed.StatusCode != 200 || refreshedTag == "" || refreshedTag == entityTag {
		t.Fatalf("refresh PUBLISH response = %#v", refreshed)
	}

	remove := publishRequest("publish-remove", refreshedTag, "0", nil)
	sipClient.handlePUBLISH(remove)

	if response := <-sent; response.StatusCode != 200 || response.Headers.Get("Expires") != "0" {
		t.Fatalf("remove PUBLISH response = %#v", response)
	}

	sipClient.mu.Lock()
	remaining := len(sipClient.serverPublications)
	sipClient.mu.Unlock()

	if remaining != 0 {
		t.Fatalf("server publications after remove = %d", remaining)
	}

	invalid := publishRequest("publish-invalid", "", "60", nil)
	sipClient.handlePUBLISH(invalid)

	if response := <-sent; response.StatusCode != 400 {
		t.Fatalf("bodyless initial PUBLISH response = %d", response.StatusCode)
	}
}

func communicationRequest(method, callID string) *sip.Message {
	request := sip.NewRequest(method, "sip:1001@example.test")
	request.Headers.Add("Via", "SIP/2.0/TCP peer.example.test;branch=z9hG4bK-"+callID)
	request.Headers.Add("From", "<sip:2002@example.test>;tag=remote")
	request.Headers.Add("To", "<sip:1001@example.test>;tag=local")
	request.Headers.Add("Call-ID", callID)
	request.Headers.Add("CSeq", "1 "+method)

	return request
}

func publishRequest(callID, entityTag, expires string, body []byte) *sip.Message {
	request := communicationRequest("PUBLISH", callID)
	request.RequestURI = "sip:presence@example.test"
	request.Headers.Add("Event", "presence")
	request.Headers.Add("Expires", expires)

	if entityTag != "" {
		request.Headers.Add("SIP-If-Match", entityTag)
	}

	if len(body) > 0 {
		request.Headers.Add("Content-Type", "application/pidf+xml")
		request.Body = append([]byte(nil), body...)
	}

	return request
}
