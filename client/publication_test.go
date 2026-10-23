package client

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/jvrs2812/go-sip/presence"
	"github.com/jvrs2812/go-sip/sip"
)

func TestPublicationLifecycleAndVersioning(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer listener.Close()

	serverErrors := make(chan error, 1)

	go runPublicationServer(listener, serverErrors)

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
		Username:           "1001",
		TransactionTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	defer sipClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	publication, err := sipClient.NewPublication(
		ctx,
		"sip:presence@example.test",
		"presence",
		presence.PIDFContentType,
		[]byte("initial"),
		time.Second,
	)
	if err != nil {
		t.Fatalf("NewPublication() error = %v", err)
	}

	if publication.Status() != PublicationActive || publication.EntityTag() != "version-1" {
		t.Fatalf("initial publication = %q, tag = %q", publication.Status(), publication.EntityTag())
	}

	if err := publication.Refresh(ctx, time.Second); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	if publication.EntityTag() != "version-2" {
		t.Fatalf("entity tag after refresh = %q", publication.EntityTag())
	}

	err = publication.Update(ctx, presence.PIDFContentType, []byte("stale update"), 4*time.Second)
	if !errors.Is(err, ErrPublicationVersionMismatch) {
		t.Fatalf("stale Update() error = %v", err)
	}

	if publication.EntityTag() != "version-2" {
		t.Fatalf("entity tag changed after 412: %q", publication.EntityTag())
	}

	if err := publication.Update(ctx, presence.PIDFContentType, []byte("current update"), 4*time.Second); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if publication.EntityTag() != "version-3" {
		t.Fatalf("entity tag after update = %q", publication.EntityTag())
	}

	if err := publication.Remove(ctx); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	if publication.Status() != PublicationRemoved || !publication.ExpiresAt().IsZero() {
		t.Fatalf("removed publication = %q, expires = %v", publication.Status(), publication.ExpiresAt())
	}

	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestPublicationExpirationCleansClientState(t *testing.T) {
	sipClient, err := NewClient(Config{ServerHost: "example.test", Username: "1001"})
	if err != nil {
		t.Fatal(err)
	}

	publication := &Publication{
		client: sipClient,
		status: PublicationActive,
	}

	sipClient.mu.Lock()
	sipClient.publications[publication] = struct{}{}
	sipClient.mu.Unlock()

	publication.mu.Lock()
	publication.scheduleExpiryLocked(10 * time.Millisecond)
	publication.mu.Unlock()

	deadline := time.Now().Add(time.Second)
	for publication.Status() != PublicationExpired && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	if publication.Status() != PublicationExpired {
		t.Fatal("publication did not expire")
	}

	sipClient.mu.Lock()
	_, tracked := sipClient.publications[publication]
	sipClient.mu.Unlock()

	if tracked {
		t.Fatal("expired publication remains tracked by client")
	}
}

func runPublicationServer(listener net.Listener, result chan<- error) {
	connection, err := listener.Accept()
	if err != nil {
		result <- err

		return
	}

	defer connection.Close()

	reader := bufio.NewReader(connection)
	initial, err := expectPublish(reader, "", "1", "initial")
	if err != nil {
		result <- err

		return
	}

	intervalTooBrief := responseForTest(initial, 423, "Interval Too Brief")
	intervalTooBrief.Headers.Add("Min-Expires", "2")
	if err := writeMessage(connection, intervalTooBrief); err != nil {
		result <- err

		return
	}

	retry, err := expectPublish(reader, "", "2", "initial")
	if err != nil {
		result <- err

		return
	}

	if err := writePublicationResponse(connection, retry, "version-1", "2"); err != nil {
		result <- err

		return
	}

	refresh, err := expectPublish(reader, "version-1", "1", "")
	if err != nil {
		result <- err

		return
	}

	refreshRejected := responseForTest(refresh, 423, "Interval Too Brief")
	refreshRejected.Headers.Add("Min-Expires", "3")
	if err := writeMessage(connection, refreshRejected); err != nil {
		result <- err

		return
	}

	refreshRetry, err := expectPublish(reader, "version-1", "3", "")
	if err != nil {
		result <- err

		return
	}

	if err := writePublicationResponse(connection, refreshRetry, "version-2", "3"); err != nil {
		result <- err

		return
	}

	stale, err := expectPublish(reader, "version-2", "4", "stale update")
	if err != nil {
		result <- err

		return
	}

	if err := writeMessage(connection, responseForTest(stale, 412, "Conditional Request Failed")); err != nil {
		result <- err

		return
	}

	current, err := expectPublish(reader, "version-2", "4", "current update")
	if err != nil {
		result <- err

		return
	}

	if err := writePublicationResponse(connection, current, "version-3", "4"); err != nil {
		result <- err

		return
	}

	remove, err := expectPublish(reader, "version-3", "0", "")
	if err != nil {
		result <- err

		return
	}

	if err := writeMessage(connection, responseForTest(remove, 200, "OK")); err != nil {
		result <- err

		return
	}

	result <- nil
}

func expectPublish(reader *bufio.Reader, entityTag, expires, body string) (*sip.Message, error) {
	request, err := sip.ParseReader(reader, sip.DefaultLimits())
	if err != nil {
		return nil, err
	}

	if request.Method != "PUBLISH" {
		return nil, fmt.Errorf("method = %q, want PUBLISH", request.Method)
	}

	if request.Headers.Get("Event") != "presence" {
		return nil, fmt.Errorf("Event = %q", request.Headers.Get("Event"))
	}

	if request.Headers.Get("SIP-If-Match") != entityTag {
		return nil, fmt.Errorf("SIP-If-Match = %q, want %q", request.Headers.Get("SIP-If-Match"), entityTag)
	}

	if request.Headers.Get("Expires") != expires {
		return nil, fmt.Errorf("Expires = %q, want %q", request.Headers.Get("Expires"), expires)
	}

	if string(request.Body) != body {
		return nil, fmt.Errorf("body = %q, want %q", request.Body, body)
	}

	return request, nil
}

func writePublicationResponse(connection net.Conn, request *sip.Message, entityTag, expires string) error {
	response := responseForTest(request, 200, "OK")
	response.Headers.Add("SIP-ETag", entityTag)
	response.Headers.Add("Expires", expires)

	return writeMessage(connection, response)
}
