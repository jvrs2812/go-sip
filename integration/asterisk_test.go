//go:build integration

package integration

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jvrs2812/go-sip/client"
	"github.com/jvrs2812/go-sip/transport"
)

func TestAsteriskRegistrationLifecycle(t *testing.T) {
	host, port := integrationAddress(t)

	sipClient, err := client.NewClient(client.Config{
		ServerHost:          host,
		ServerPort:          port,
		Username:            "1001",
		Password:            "secret",
		Network:             transport.UDP,
		RegistrationExpires: 2 * time.Minute,
		TransactionTimeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	defer sipClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := sipClient.Register(ctx); err != nil {
		t.Fatalf("register with Asterisk: %v", err)
	}

	if !sipClient.IsRegistered() {
		t.Fatal("client did not enter registered state")
	}

	if err := sipClient.Unregister(ctx); err != nil {
		t.Fatalf("unregister from Asterisk: %v", err)
	}

	if sipClient.IsRegistered() {
		t.Fatal("client remained registered after unregister")
	}
}

func TestAsteriskOutboundCallLifecycle(t *testing.T) {
	host, port := integrationAddress(t)

	sipClient, err := client.NewClient(client.Config{
		ServerHost:         host,
		ServerPort:         port,
		Username:           "anonymous",
		Network:            transport.UDP,
		TransactionTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	defer sipClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	call, err := sipClient.Invite(ctx, "sip:600@"+host+":"+strconv.Itoa(port))
	if err != nil {
		t.Fatalf("place call through Asterisk: %v", err)
	}

	if call.State() != client.CallActive {
		t.Fatalf("call state = %q, want %q", call.State(), client.CallActive)
	}

	if err := call.Hangup(ctx); err != nil {
		t.Fatalf("hang up Asterisk call: %v", err)
	}

	if call.State() != client.CallEnded {
		t.Fatalf("call state after BYE = %q, want %q", call.State(), client.CallEnded)
	}
}

func integrationAddress(t *testing.T) (string, int) {
	t.Helper()

	host := environmentOrDefault("SIP_INTEGRATION_HOST", "127.0.0.1")
	port, err := strconv.Atoi(environmentOrDefault("SIP_INTEGRATION_PORT", "15060"))
	if err != nil {
		t.Fatal(err)
	}

	return host, port
}

func environmentOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}

	return fallback
}
