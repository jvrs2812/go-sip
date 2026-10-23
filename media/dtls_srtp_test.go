package media

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestDTLSSRTPHandshakeAndPacketProtection(t *testing.T) {
	clientIdentity, err := NewDTLSIdentity()
	if err != nil {
		t.Fatal(err)
	}

	serverIdentity, err := NewDTLSIdentity()
	if err != nil {
		t.Fatal(err)
	}

	clientPacketConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	serverPacketConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		_ = clientPacketConn.Close()
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	type secureResult struct {
		session *SecureRTP
		err     error
	}

	serverResult := make(chan secureResult, 1)
	go func() {
		session, establishErr := EstablishDTLSSRTP(ctx, serverPacketConn, clientPacketConn.LocalAddr(), DTLSSRTPConfig{
			Identity:          serverIdentity,
			RemoteFingerprint: clientIdentity.Fingerprint(),
			Role:              DTLSServer,
			FlightInterval:    20 * time.Millisecond,
		})

		serverResult <- secureResult{session: session, err: establishErr}
	}()

	clientSession, err := EstablishDTLSSRTP(ctx, clientPacketConn, serverPacketConn.LocalAddr(), DTLSSRTPConfig{
		Identity:          clientIdentity,
		RemoteFingerprint: serverIdentity.Fingerprint(),
		Role:              DTLSClient,
		FlightInterval:    20 * time.Millisecond,
	})
	if err != nil {
		_ = serverPacketConn.Close()
		t.Fatal(err)
	}

	server := <-serverResult
	if server.err != nil {
		_ = clientSession.Close()
		t.Fatal(server.err)
	}

	t.Cleanup(func() {
		_ = clientSession.Close()
		_ = server.session.Close()
	})

	packet := RTPPacket{
		Version:        2,
		PayloadType:    0,
		SequenceNumber: 7,
		Timestamp:      160,
		SSRC:           42,
		Payload:        []byte("G711 payload must not be visible"),
	}

	plaintext, err := packet.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	ciphertext, err := clientSession.ProtectRTP(plaintext)
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(ciphertext, plaintext) || bytes.Contains(ciphertext, packet.Payload) {
		t.Fatal("SRTP packet exposed the RTP plaintext")
	}

	decrypted, err := server.session.UnprotectRTP(ciphertext)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypted RTP differs from input:\n got %x\nwant %x", decrypted, plaintext)
	}

	reverseCiphertext, err := server.session.ProtectRTP(plaintext)
	if err != nil {
		t.Fatal(err)
	}

	reversePlaintext, err := clientSession.UnprotectRTP(reverseCiphertext)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(reversePlaintext, plaintext) {
		t.Fatal("reverse SRTP direction did not use matching keys")
	}

	if err := clientSession.Close(); err != nil {
		t.Fatal(err)
	}

	if err := clientSession.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}

	if _, err := clientSession.ProtectRTP(plaintext); !errors.Is(err, ErrSecureRTPClosed) {
		t.Fatalf("ProtectRTP() after Close error = %v", err)
	}
}

func TestFingerprintVerifierRejectsMismatch(t *testing.T) {
	identity, err := NewDTLSIdentity()
	if err != nil {
		t.Fatal(err)
	}

	verify, err := fingerprintVerifier(identity.Fingerprint())
	if err != nil {
		t.Fatal(err)
	}

	if err := verify([][]byte{[]byte("different certificate")}, nil); err != ErrFingerprintMismatch {
		t.Fatalf("expected fingerprint mismatch, got %v", err)
	}
}

func TestDTLSSRTPHandshakeRejectsWrongFingerprint(t *testing.T) {
	clientIdentity, err := NewDTLSIdentity()
	if err != nil {
		t.Fatal(err)
	}

	serverIdentity, err := NewDTLSIdentity()
	if err != nil {
		t.Fatal(err)
	}

	clientPacketConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer clientPacketConn.Close()

	serverPacketConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer serverPacketConn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	serverResult := make(chan error, 1)
	go func() {
		_, establishErr := EstablishDTLSSRTP(ctx, serverPacketConn, clientPacketConn.LocalAddr(), DTLSSRTPConfig{
			Identity:          serverIdentity,
			RemoteFingerprint: clientIdentity.Fingerprint(),
			Role:              DTLSServer,
			FlightInterval:    20 * time.Millisecond,
		})

		serverResult <- establishErr
	}()

	wrongFingerprint := strings.Repeat("00:", 31) + "00"
	_, clientErr := EstablishDTLSSRTP(ctx, clientPacketConn, serverPacketConn.LocalAddr(), DTLSSRTPConfig{
		Identity:          clientIdentity,
		RemoteFingerprint: wrongFingerprint,
		Role:              DTLSClient,
		FlightInterval:    20 * time.Millisecond,
	})
	serverErr := <-serverResult

	if clientErr == nil && serverErr == nil {
		t.Fatal("DTLS handshake accepted a mismatched fingerprint")
	}

	combined := errors.Join(clientErr, serverErr)
	if !errors.Is(combined, ErrFingerprintMismatch) && !strings.Contains(combined.Error(), ErrFingerprintMismatch.Error()) {
		t.Fatalf("fingerprint mismatch error = %v", combined)
	}
}
