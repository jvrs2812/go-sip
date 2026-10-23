package client

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/jvrs2812/go-sip/sip"
)

func TestSubscriptionLifecycle(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer listener.Close()

	serverErrors := make(chan error, 1)

	go runSubscriptionServer(listener, serverErrors)

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

	subscription, err := sipClient.NewSubscription(ctx, "sip:presence@example.test", "presence", time.Second)
	if err != nil {
		t.Fatalf("NewSubscription() error = %v", err)
	}

	if subscription.Status() != SubscriptionPending && subscription.Status() != SubscriptionActive {
		t.Fatalf("initial status = %q", subscription.Status())
	}

	active := receiveNotification(t, ctx, subscription.Notifications())
	if active.SubscriptionState != "active;expires=2" || string(active.Body) != "active" {
		t.Fatalf("active notification = %#v", active)
	}

	if subscription.Status() != SubscriptionActive {
		t.Fatalf("status after NOTIFY = %q", subscription.Status())
	}

	if err := subscription.Refresh(ctx, time.Second); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	remaining := time.Until(subscription.ExpiresAt())
	if remaining < 2*time.Second || remaining > 4*time.Second {
		t.Fatalf("refresh expiration = %v, want about 3s", remaining)
	}

	if err := subscription.Cancel(ctx); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}

	terminated := receiveNotification(t, ctx, subscription.Notifications())
	if terminated.SubscriptionState != "terminated;reason=deactivated" {
		t.Fatalf("terminal notification = %#v", terminated)
	}

	if subscription.Status() != SubscriptionTerminated || subscription.Reason() != "deactivated" {
		t.Fatalf("terminal state = %q, reason = %q", subscription.Status(), subscription.Reason())
	}

	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestNotifyValidation(t *testing.T) {
	sipClient, err := NewClient(Config{ServerHost: "example.test", Username: "1001"})
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
	sipClient.subscriptions["subscription-call"] = &Subscription{
		client:        sipClient,
		event:         "presence",
		callID:        "subscription-call",
		localTag:      "local",
		remoteTag:     "remote",
		status:        SubscriptionPending,
		notifications: make(chan NotifyEvent, 1),
	}
	sipClient.mu.Unlock()

	wrongEvent := notifyForTest("subscription-call", "presence.winfo", "active;expires=60", "remote", "local")
	sipClient.handleNOTIFY(wrongEvent)

	if response := <-sent; response.StatusCode != 489 {
		t.Fatalf("wrong event response = %d", response.StatusCode)
	}

	wrongDialog := notifyForTest("subscription-call", "presence", "active;expires=60", "other", "local")
	sipClient.handleNOTIFY(wrongDialog)

	if response := <-sent; response.StatusCode != 481 {
		t.Fatalf("wrong dialog response = %d", response.StatusCode)
	}

	malformedState := notifyForTest("subscription-call", "presence", "invalid", "remote", "local")
	sipClient.handleNOTIFY(malformedState)

	if response := <-sent; response.StatusCode != 400 {
		t.Fatalf("malformed state response = %d", response.StatusCode)
	}

	unknown := notifyForTest("unknown-call", "presence", "active;expires=60", "remote", "local")
	sipClient.handleNOTIFY(unknown)

	if response := <-sent; response.StatusCode != 481 {
		t.Fatalf("unknown subscription response = %d", response.StatusCode)
	}
}

func TestSubscriptionExpirationCleansClientState(t *testing.T) {
	sipClient, err := NewClient(Config{ServerHost: "example.test", Username: "1001"})
	if err != nil {
		t.Fatal(err)
	}

	subscription := &Subscription{
		client:        sipClient,
		callID:        "expiring-subscription",
		status:        SubscriptionActive,
		notifications: make(chan NotifyEvent, 1),
	}

	sipClient.mu.Lock()
	sipClient.subscriptions[subscription.callID] = subscription
	sipClient.mu.Unlock()

	subscription.mu.Lock()
	subscription.scheduleExpiryLocked(10 * time.Millisecond)
	subscription.mu.Unlock()

	deadline := time.Now().Add(time.Second)
	for subscription.Status() != SubscriptionExpired && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	if subscription.Status() != SubscriptionExpired {
		t.Fatal("subscription did not expire")
	}

	sipClient.mu.Lock()
	_, tracked := sipClient.subscriptions[subscription.callID]
	sipClient.mu.Unlock()

	if tracked {
		t.Fatal("expired subscription remains tracked")
	}
}

func runSubscriptionServer(listener net.Listener, result chan<- error) {
	connection, err := listener.Accept()
	if err != nil {
		result <- err

		return
	}

	defer connection.Close()

	reader := bufio.NewReader(connection)
	initial, err := sip.ParseReader(reader, sip.DefaultLimits())
	if err != nil {
		result <- err

		return
	}

	if initial.Method != "SUBSCRIBE" || initial.Headers.Get("Expires") != "1" {
		result <- fmt.Errorf("initial SUBSCRIBE = %#v", initial)

		return
	}

	intervalTooBrief := responseForTest(initial, 423, "Interval Too Brief")
	intervalTooBrief.Headers.Add("Min-Expires", "2")
	if err := writeMessage(connection, intervalTooBrief); err != nil {
		result <- err

		return
	}

	retry, err := sip.ParseReader(reader, sip.DefaultLimits())
	if err != nil {
		result <- err

		return
	}

	if retry.Headers.Get("Call-ID") != initial.Headers.Get("Call-ID") || retry.Headers.Get("Expires") != "2" {
		result <- fmt.Errorf("423 retry = %#v", retry)

		return
	}

	accepted := responseForTest(retry, 200, "OK")
	accepted.Headers.Set("Expires", "2")
	accepted.Headers.Add("Contact", "<sip:presence@127.0.0.1>")
	if err := writeMessage(connection, accepted); err != nil {
		result <- err

		return
	}

	active := notifyForTest(initial.Headers.Get("Call-ID"), "presence", "active;expires=2", "server", headerTag(initial.Headers.Get("From")))
	active.Headers.Set("To", initial.Headers.Get("From"))
	active.Body = []byte("active")
	if err := writeMessage(connection, active); err != nil {
		result <- err

		return
	}

	if err := expectResponse(reader, 200); err != nil {
		result <- err

		return
	}

	refresh, err := sip.ParseReader(reader, sip.DefaultLimits())
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

	refreshRetry, err := sip.ParseReader(reader, sip.DefaultLimits())
	if err != nil {
		result <- err

		return
	}

	if refreshRetry.Headers.Get("Expires") != "3" {
		result <- fmt.Errorf("refresh retry Expires = %q", refreshRetry.Headers.Get("Expires"))

		return
	}

	refreshAccepted := responseForTest(refreshRetry, 200, "OK")
	refreshAccepted.Headers.Set("Expires", "3")
	if err := writeMessage(connection, refreshAccepted); err != nil {
		result <- err

		return
	}

	unsubscribe, err := sip.ParseReader(reader, sip.DefaultLimits())
	if err != nil {
		result <- err

		return
	}

	if unsubscribe.Headers.Get("Expires") != "0" {
		result <- fmt.Errorf("unsubscribe Expires = %q", unsubscribe.Headers.Get("Expires"))

		return
	}

	if err := writeMessage(connection, responseForTest(unsubscribe, 200, "OK")); err != nil {
		result <- err

		return
	}

	terminated := notifyForTest(initial.Headers.Get("Call-ID"), "presence", "terminated;reason=deactivated", "server", headerTag(initial.Headers.Get("From")))
	terminated.Headers.Set("To", initial.Headers.Get("From"))
	terminated.Headers.Set("Via", "SIP/2.0/TCP server.example.test;branch=z9hG4bK-notify-terminal")
	terminated.Headers.Set("CSeq", "2 NOTIFY")
	if err := writeMessage(connection, terminated); err != nil {
		result <- err

		return
	}

	if err := expectResponse(reader, 200); err != nil {
		result <- err

		return
	}

	result <- nil
}

func notifyForTest(callID, event, state, fromTag, toTag string) *sip.Message {
	request := sip.NewRequest("NOTIFY", "sip:1001@example.test")
	request.Headers.Add("Via", "SIP/2.0/TCP server.example.test;branch=z9hG4bK-notify")
	request.Headers.Add("From", "<sip:presence@example.test>;tag="+fromTag)
	request.Headers.Add("To", "<sip:1001@example.test>;tag="+toTag)
	request.Headers.Add("Call-ID", callID)
	request.Headers.Add("CSeq", "1 NOTIFY")
	request.Headers.Add("Event", event)
	request.Headers.Add("Subscription-State", state)
	request.Headers.Add("Content-Type", "application/pidf+xml")

	return request
}

func expectResponse(reader *bufio.Reader, status int) error {
	response, err := sip.ParseReader(reader, sip.DefaultLimits())
	if err != nil {
		return err
	}

	if response.StatusCode != status {
		return fmt.Errorf("response status = %d, want %d", response.StatusCode, status)
	}

	return nil
}

func receiveNotification(t *testing.T, ctx context.Context, notifications <-chan NotifyEvent) NotifyEvent {
	t.Helper()

	select {
	case event := <-notifications:
		return event
	case <-ctx.Done():
		t.Fatal("timed out waiting for NOTIFY")

		return NotifyEvent{}
	}
}
