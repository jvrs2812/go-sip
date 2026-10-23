package nat

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jvrs2812/go-sip/media"
	"github.com/pion/logging"
	"github.com/pion/turn/v4"
)

func TestICEHostCandidatesConnect(t *testing.T) {
	first, err := NewICEAgent(ICEConfig{HostOnly: true, IncludeLoopback: true})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = first.Close()
	})

	second, err := NewICEAgent(ICEConfig{HostOnly: true, IncludeLoopback: true})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = second.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	firstCandidates, err := first.Gather(ctx)
	if err != nil {
		t.Fatal(err)
	}

	secondCandidates, err := second.Gather(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(firstCandidates) == 0 || len(secondCandidates) == 0 {
		t.Fatalf("expected host candidates, got %d and %d", len(firstCandidates), len(secondCandidates))
	}

	firstCredentials, err := first.Credentials()
	if err != nil {
		t.Fatal(err)
	}

	secondCredentials, err := second.Credentials()
	if err != nil {
		t.Fatal(err)
	}

	type connectionResult struct {
		connection io.ReadWriteCloser
		err        error
	}

	controlledResult := make(chan connectionResult, 1)
	go func() {
		connection, connectErr := second.Connect(ctx, RemoteICE{
			Credentials: firstCredentials,
			Candidates:  firstCandidates,
		}, ICEControlled)

		controlledResult <- connectionResult{connection: connection, err: connectErr}
	}()

	controlling, err := first.Connect(ctx, RemoteICE{
		Credentials: secondCredentials,
		Candidates:  secondCandidates,
	}, ICEControlling)
	if err != nil {
		t.Fatal(err)
	}

	defer controlling.Close()

	controlled := <-controlledResult
	if controlled.err != nil {
		t.Fatal(controlled.err)
	}

	defer controlled.connection.Close()

	payload := []byte("secured media path")
	if _, err := controlling.Write(payload); err != nil {
		t.Fatal(err)
	}

	received := make([]byte, len(payload))
	if _, err := io.ReadFull(controlled.connection, received); err != nil {
		t.Fatal(err)
	}

	if string(received) != string(payload) {
		t.Fatalf("received %q, want %q", received, payload)
	}

	if _, ok := first.SelectedPair(); !ok {
		t.Fatal("expected a selected candidate pair")
	}
}

func TestTURNRequiresCredentials(t *testing.T) {
	_, err := NewICEAgent(ICEConfig{Servers: []ICEServer{{URL: "turn:turn.example.com:3478"}}})
	if err == nil {
		t.Fatal("expected missing TURN credentials to fail")
	}
}

func TestICEAgentCloseIsIdempotentAndStopsOperations(t *testing.T) {
	agent, err := NewICEAgent(ICEConfig{HostOnly: true, IncludeLoopback: true})
	if err != nil {
		t.Fatal(err)
	}

	if err := agent.Close(); err != nil {
		t.Fatal(err)
	}

	if err := agent.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}

	if _, err := agent.Credentials(); err != ErrClosed {
		t.Fatalf("Credentials() after Close error = %v", err)
	}

	if _, err := agent.Gather(context.Background()); err != ErrClosed {
		t.Fatalf("Gather() after Close error = %v", err)
	}
}

func TestICETURNRelayConnectsThroughAuthenticatedLocalServer(t *testing.T) {
	packetConnection, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	const username = "relay-user"
	const password = "relay-password"
	const realm = "go-sip.test"
	authKey := turn.GenerateAuthKey(username, realm, password)

	server, err := turn.NewServer(turn.ServerConfig{
		Realm: realm,
		AuthHandler: func(candidateUsername, _ string, _ net.Addr) ([]byte, bool) {
			if candidateUsername != username {
				return nil, false
			}

			return authKey, true
		},
		PacketConnConfigs: []turn.PacketConnConfig{{
			PacketConn: packetConnection,
			RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{
				RelayAddress: net.ParseIP("127.0.0.1"),
				Address:      "127.0.0.1",
			},
		}},
		LoggerFactory: logging.NewDefaultLoggerFactory(),
	})
	if err != nil {
		_ = packetConnection.Close()
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = server.Close()
	})

	turnURL := fmt.Sprintf("turn:%s?transport=udp", packetConnection.LocalAddr().String())
	newAgent := func() *ICEAgent {
		agent, agentErr := NewICEAgent(ICEConfig{
			Servers: []ICEServer{{
				URL:        turnURL,
				Username:   username,
				Credential: password,
			}},
			IncludeLoopback: true,
			RelayOnly:       true,
		})
		if agentErr != nil {
			t.Fatal(agentErr)
		}

		t.Cleanup(func() {
			_ = agent.Close()
		})

		return agent
	}

	first := newAgent()
	second := newAgent()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	firstCandidates, err := first.Gather(ctx)
	if err != nil {
		t.Fatal(err)
	}

	secondCandidates, err := second.Gather(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for _, candidates := range [][]string{firstCandidates, secondCandidates} {
		if len(candidates) == 0 || !strings.Contains(candidates[0], " typ relay ") {
			t.Fatalf("expected relay-only candidate, got %#v", candidates)
		}
	}

	firstCredentials, err := first.Credentials()
	if err != nil {
		t.Fatal(err)
	}

	secondCredentials, err := second.Credentials()
	if err != nil {
		t.Fatal(err)
	}

	controlledResult := make(chan connectionResult, 1)
	go func() {
		connection, connectErr := second.Connect(ctx, RemoteICE{
			Credentials: firstCredentials,
			Candidates:  firstCandidates,
		}, ICEControlled)

		controlledResult <- connectionResult{connection: connection, err: connectErr}
	}()

	controlling, err := first.Connect(ctx, RemoteICE{
		Credentials: secondCredentials,
		Candidates:  secondCandidates,
	}, ICEControlling)
	if err != nil {
		t.Fatal(err)
	}
	defer controlling.Close()

	controlled := <-controlledResult
	if controlled.err != nil {
		t.Fatal(controlled.err)
	}
	defer controlled.connection.Close()

	payload := []byte("TURN relayed media")
	if _, err := controlling.Write(payload); err != nil {
		t.Fatal(err)
	}

	received := make([]byte, len(payload))
	if _, err := io.ReadFull(controlled.connection, received); err != nil {
		t.Fatal(err)
	}

	if string(received) != string(payload) {
		t.Fatalf("received %q", received)
	}

	pair, ok := first.SelectedPair()
	if !ok || !strings.Contains(pair.Local, " typ relay ") {
		t.Fatalf("selected pair = %#v", pair)
	}

	firstPacketConn, err := media.NewConnectedPacketConn(controlling)
	if err != nil {
		t.Fatal(err)
	}

	secondPacketConn, err := media.NewConnectedPacketConn(controlled.connection)
	if err != nil {
		t.Fatal(err)
	}

	firstIdentity, err := media.NewDTLSIdentity()
	if err != nil {
		t.Fatal(err)
	}

	secondIdentity, err := media.NewDTLSIdentity()
	if err != nil {
		t.Fatal(err)
	}

	type secureResult struct {
		session *media.SecureRTP
		err     error
	}

	secureServerResult := make(chan secureResult, 1)
	go func() {
		session, establishErr := media.EstablishDTLSSRTP(ctx, secondPacketConn, controlled.connection.RemoteAddr(), media.DTLSSRTPConfig{
			Identity:          secondIdentity,
			RemoteFingerprint: firstIdentity.Fingerprint(),
			Role:              media.DTLSServer,
			FlightInterval:    20 * time.Millisecond,
		})

		secureServerResult <- secureResult{session: session, err: establishErr}
	}()

	secureClient, err := media.EstablishDTLSSRTP(ctx, firstPacketConn, controlling.RemoteAddr(), media.DTLSSRTPConfig{
		Identity:          firstIdentity,
		RemoteFingerprint: secondIdentity.Fingerprint(),
		Role:              media.DTLSClient,
		FlightInterval:    20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	defer secureClient.Close()

	secureServer := <-secureServerResult
	if secureServer.err != nil {
		t.Fatal(secureServer.err)
	}

	defer secureServer.session.Close()

	rtpPacket := media.RTPPacket{
		Version:        2,
		PayloadType:    0,
		SequenceNumber: 1,
		Timestamp:      160,
		SSRC:           10,
		Payload:        []byte("protected TURN media"),
	}

	plainRTP, err := rtpPacket.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	if err := secureClient.WriteRTP(plainRTP); err != nil {
		t.Fatal(err)
	}

	readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
	defer readCancel()

	receivedRTP, _, err := secureServer.session.ReadRTP(readCtx, nil)
	if err != nil {
		t.Fatal(err)
	}

	if string(receivedRTP) != string(plainRTP) {
		t.Fatal("DTLS-SRTP payload changed across the TURN relay")
	}
}

func TestTURNExpiredCredentialsCannotGatherRelay(t *testing.T) {
	packetConnection, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	const realm = "go-sip.test"
	server, err := turn.NewServer(turn.ServerConfig{
		Realm: realm,
		AuthHandler: func(username, _ string, _ net.Addr) ([]byte, bool) {
			expiresAt, parseErr := time.Parse(time.RFC3339, username)
			if parseErr != nil || !expiresAt.After(time.Now()) {
				return nil, false
			}

			return turn.GenerateAuthKey(username, realm, "temporary-secret"), true
		},
		PacketConnConfigs: []turn.PacketConnConfig{{
			PacketConn: packetConnection,
			RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{
				RelayAddress: net.ParseIP("127.0.0.1"),
				Address:      "127.0.0.1",
			},
		}},
		LoggerFactory: logging.NewDefaultLoggerFactory(),
	})
	if err != nil {
		_ = packetConnection.Close()
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = server.Close()
	})

	agent, err := NewICEAgent(ICEConfig{
		Servers: []ICEServer{{
			URL:        fmt.Sprintf("turn:%s?transport=udp", packetConnection.LocalAddr()),
			Username:   time.Now().Add(-time.Minute).Format(time.RFC3339),
			Credential: "temporary-secret",
		}},
		IncludeLoopback: true,
		RelayOnly:       true,
	})
	if err != nil {
		t.Fatal(err)
	}

	defer agent.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	candidates, gatherErr := agent.Gather(ctx)
	if gatherErr == nil && len(candidates) != 0 {
		t.Fatalf("expired TURN credentials gathered candidates: %#v", candidates)
	}
}

type connectionResult struct {
	connection net.Conn
	err        error
}
