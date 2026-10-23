package client

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jvrs2812/go-sip/auth"
	"github.com/jvrs2812/go-sip/discovery"
	"github.com/jvrs2812/go-sip/sip"
	"github.com/jvrs2812/go-sip/transport"
	"github.com/jvrs2812/go-sip/types"
)

func TestRegisterAuthenticatesAndUnregisters(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")

	if err != nil {
		t.Fatal(err)
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
		first, parseErr := sip.ParseReader(reader, sip.DefaultLimits())

		if parseErr != nil {
			serverErrors <- parseErr

			return
		}

		if first.Method != "REGISTER" || first.Headers.Get("Authorization") != "" {
			serverErrors <- fmt.Errorf("unexpected initial REGISTER: %#v", first)

			return
		}

		challenge := responseForTest(first, 401, "Unauthorized")
		challenge.Headers.Add("WWW-Authenticate", `Digest realm="example.test", nonce="nonce-1", algorithm=SHA-256, qop="auth"`)

		if writeErr := writeMessage(connection, challenge); writeErr != nil {
			serverErrors <- writeErr

			return
		}

		authenticated, parseErr := sip.ParseReader(reader, sip.DefaultLimits())

		if parseErr != nil {
			serverErrors <- parseErr

			return
		}

		authorization := authenticated.Headers.Get("Authorization")

		if !strings.HasPrefix(authorization, "Digest ") || !strings.Contains(authorization, "algorithm=SHA-256") || strings.Contains(authorization, "password") {
			serverErrors <- fmt.Errorf("unexpected Authorization header: %s", authorization)

			return
		}

		registered := responseForTest(authenticated, 200, "OK")
		registered.Headers.Add("Expires", "3600")

		if writeErr := writeMessage(connection, registered); writeErr != nil {
			serverErrors <- writeErr

			return
		}

		unregister, parseErr := sip.ParseReader(reader, sip.DefaultLimits())

		if parseErr != nil {
			serverErrors <- parseErr

			return
		}

		if unregister.Headers.Get("Expires") != "0" {
			serverErrors <- fmt.Errorf("unregister Expires = %q", unregister.Headers.Get("Expires"))

			return
		}

		if writeErr := writeMessage(connection, responseForTest(unregister, 200, "OK")); writeErr != nil {
			serverErrors <- writeErr

			return
		}

		serverErrors <- nil
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())

	if err != nil {
		t.Fatal(err)
	}

	port, err := net.LookupPort("tcp", portText)

	if err != nil {
		t.Fatal(err)
	}

	sipClient, err := NewClient(Config{
		ServerHost:         host,
		ServerPort:         port,
		Username:           "1001",
		Password:           "secret",
		TransactionTimeout: time.Second,
	})

	if err != nil {
		t.Fatal(err)
	}

	defer sipClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := sipClient.Register(ctx); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if !sipClient.IsRegistered() {
		t.Fatal("client should be registered")
	}

	if err := sipClient.Unregister(ctx); err != nil {
		t.Fatalf("Unregister() error = %v", err)
	}

	if sipClient.IsRegistered() {
		t.Fatal("client should be unregistered")
	}

	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestRegisterUsesInjectedOAuthTokenProvider(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
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

		initial, parseErr := sip.ParseReader(reader, sip.DefaultLimits())
		if parseErr != nil {
			serverErrors <- parseErr

			return
		}

		challenge := responseForTest(initial, 401, "Unauthorized")
		challenge.Headers.Add("WWW-Authenticate", `Bearer realm="sip.example", scope="sip:register"`)

		if writeErr := writeMessage(connection, challenge); writeErr != nil {
			serverErrors <- writeErr

			return
		}

		authorized, parseErr := sip.ParseReader(reader, sip.DefaultLimits())
		if parseErr != nil {
			serverErrors <- parseErr

			return
		}

		if authorized.Headers.Get("Authorization") != "Bearer access-token" {
			serverErrors <- fmt.Errorf("Authorization = %q", authorized.Headers.Get("Authorization"))

			return
		}

		if writeErr := writeMessage(connection, responseForTest(authorized, 200, "OK")); writeErr != nil {
			serverErrors <- writeErr

			return
		}

		serverErrors <- nil
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	port, err := net.LookupPort("tcp", portText)
	if err != nil {
		t.Fatal(err)
	}

	sipClient, err := NewClient(Config{
		ServerHost: host,
		ServerPort: port,
		Username:   "1001",
		OAuthTokenProvider: auth.TokenProviderFunc(func(_ context.Context, challenge auth.BearerChallenge) (auth.Token, error) {
			if challenge.Realm != "sip.example" || challenge.Scope != "sip:register" {
				return auth.Token{}, fmt.Errorf("unexpected challenge: %#v", challenge)
			}

			return auth.Token{AccessToken: "access-token", ExpiresAt: time.Now().Add(time.Minute)}, nil
		}),
		TransactionTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	defer sipClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := sipClient.Register(ctx); err != nil {
		t.Fatal(err)
	}

	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizationPrefersAKAv2AndUsesProvider(t *testing.T) {
	providerCalls := 0
	sipClient := &Client{
		Ramal: "1001",
		AKAProvider: auth.AKAProviderFunc(func(_ context.Context, challenge auth.AKAChallenge) (auth.AKAResult, error) {
			providerCalls++

			if challenge.Realm != "ims.example" || challenge.Nonce != "aka-nonce" {
				return auth.AKAResult{}, fmt.Errorf("unexpected AKA challenge: %#v", challenge)
			}

			return auth.AKAResult{
				Response:     []byte{1, 2, 3, 4},
				IntegrityKey: []byte("0123456789abcdef"),
				CipherKey:    []byte("fedcba9876543210"),
			}, nil
		}),
	}

	request := sip.NewRequest("REGISTER", "sip:ims.example")
	authorization, err := sipClient.authorizationForChallenges(
		context.Background(),
		[]string{
			`Digest realm="ims.example", nonce="digest-nonce", algorithm=MD5`,
			`Digest realm="ims.example", nonce="aka-nonce", algorithm=AKAv2-MD5, qop="auth"`,
		},
		request,
		make(map[string]uint32),
	)
	if err != nil {
		t.Fatal(err)
	}

	if providerCalls != 1 || !strings.Contains(authorization, "algorithm=AKAv2-MD5") || !strings.Contains(authorization, `nonce="aka-nonce"`) {
		t.Fatalf("unexpected AKAv2 authorization: calls=%d header=%q", providerCalls, authorization)
	}
}

func TestUDPRegisterRetransmitsBeforeResponse(t *testing.T) {
	server, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})

	if err != nil {
		t.Fatal(err)
	}

	defer server.Close()

	serverErrors := make(chan error, 1)

	go func() {
		buffer := make([]byte, 64<<10)

		if err := server.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			serverErrors <- err

			return
		}

		firstCount, remote, readErr := server.ReadFromUDP(buffer)

		if readErr != nil {
			serverErrors <- readErr

			return
		}

		first, parseErr := sip.Parse(buffer[:firstCount])

		if parseErr != nil {
			serverErrors <- parseErr

			return
		}

		secondCount, secondRemote, readErr := server.ReadFromUDP(buffer)

		if readErr != nil {
			serverErrors <- readErr

			return
		}

		second, parseErr := sip.Parse(buffer[:secondCount])

		if parseErr != nil {
			serverErrors <- parseErr

			return
		}

		firstKey, _ := first.TransactionKey()
		secondKey, _ := second.TransactionKey()

		if firstKey != secondKey || remote.String() != secondRemote.String() {
			serverErrors <- fmt.Errorf("retransmission mismatch: %s != %s", firstKey, secondKey)

			return
		}

		response := responseForTest(second, 200, "OK")
		payload, marshalErr := response.MarshalBinary()

		if marshalErr != nil {
			serverErrors <- marshalErr

			return
		}

		if _, writeErr := server.WriteToUDP(payload, secondRemote); writeErr != nil {
			serverErrors <- writeErr

			return
		}

		serverErrors <- nil
	}()

	sipClient, err := NewClient(Config{
		ServerHost:         "127.0.0.1",
		ServerPort:         server.LocalAddr().(*net.UDPAddr).Port,
		Username:           "1001",
		Network:            transport.UDP,
		TransactionTimeout: 3 * time.Second,
	})

	if err != nil {
		t.Fatal(err)
	}

	defer sipClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := sipClient.Register(ctx); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestRegisterReconnectsAndRestoresRegistration(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")

	if err != nil {
		t.Fatal(err)
	}

	defer listener.Close()

	serverErrors := make(chan error, 1)
	firstRegistered := make(chan struct{})
	reconnected := make(chan struct{})
	releaseServer := make(chan struct{})

	go func() {
		firstConnection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErrors <- acceptErr

			return
		}

		firstReader := bufio.NewReader(firstConnection)
		firstRequest, parseErr := sip.ParseReader(firstReader, sip.DefaultLimits())
		if parseErr != nil {
			serverErrors <- parseErr

			return
		}

		if writeErr := writeMessage(firstConnection, responseForTest(firstRequest, 200, "OK")); writeErr != nil {
			serverErrors <- writeErr

			return
		}

		close(firstRegistered)
		_ = firstConnection.Close()

		secondConnection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErrors <- acceptErr

			return
		}

		defer secondConnection.Close()

		secondReader := bufio.NewReader(secondConnection)
		secondRequest, parseErr := sip.ParseReader(secondReader, sip.DefaultLimits())
		if parseErr != nil {
			serverErrors <- parseErr

			return
		}

		if secondRequest.Method != "REGISTER" {
			serverErrors <- fmt.Errorf("reconnection request method = %q", secondRequest.Method)

			return
		}

		if writeErr := writeMessage(secondConnection, responseForTest(secondRequest, 200, "OK")); writeErr != nil {
			serverErrors <- writeErr

			return
		}

		close(reconnected)
		<-releaseServer
		serverErrors <- nil
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	port, err := net.LookupPort("tcp", portText)
	if err != nil {
		t.Fatal(err)
	}

	sipClient, err := NewClient(Config{
		ServerHost:         host,
		ServerPort:         port,
		Username:           "1001",
		TransactionTimeout: time.Second,
		ReconnectMinDelay:  10 * time.Millisecond,
		ReconnectMaxDelay:  50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	defer sipClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sipClient.Register(ctx); err != nil {
		t.Fatal(err)
	}

	<-firstRegistered

	select {
	case <-reconnected:
	case <-ctx.Done():
		t.Fatal("client did not reconnect and restore registration")
	}

	deadline := time.Now().Add(time.Second)
	for !sipClient.IsRegistered() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	if !sipClient.IsRegistered() {
		t.Fatal("client did not mark restored registration as active")
	}

	close(releaseServer)

	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestOutboundInviteCancellationSendsCANCELAndACK(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
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
		invite, parseErr := sip.ParseReader(reader, sip.DefaultLimits())
		if parseErr != nil {
			serverErrors <- parseErr

			return
		}

		ringing := responseForTest(invite, 180, "Ringing")
		if writeErr := writeMessage(connection, ringing); writeErr != nil {
			serverErrors <- writeErr

			return
		}

		cancelRequest, parseErr := sip.ParseReader(reader, sip.DefaultLimits())
		if parseErr != nil {
			serverErrors <- parseErr

			return
		}

		inviteSequence, _, _ := invite.CSeq()
		cancelSequence, cancelMethod, _ := cancelRequest.CSeq()
		if cancelRequest.Method != "CANCEL" || cancelMethod != "CANCEL" || cancelSequence != inviteSequence || cancelRequest.Headers.Get("Via") != invite.Headers.Get("Via") {
			serverErrors <- fmt.Errorf("invalid CANCEL request: %#v", cancelRequest)

			return
		}

		if writeErr := writeMessage(connection, responseForTest(cancelRequest, 200, "OK")); writeErr != nil {
			serverErrors <- writeErr

			return
		}

		terminated := responseForTest(invite, 487, "Request Terminated")
		if writeErr := writeMessage(connection, terminated); writeErr != nil {
			serverErrors <- writeErr

			return
		}

		ack, parseErr := sip.ParseReader(reader, sip.DefaultLimits())
		if parseErr != nil {
			serverErrors <- parseErr

			return
		}

		ackSequence, ackMethod, _ := ack.CSeq()
		if ack.Method != "ACK" || ackMethod != "ACK" || ackSequence != inviteSequence {
			serverErrors <- fmt.Errorf("invalid ACK after cancellation: %#v", ack)

			return
		}

		serverErrors <- nil
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	port, err := net.LookupPort("tcp", portText)
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

	ctx, cancel := context.WithCancel(context.Background())
	sipClient.OnProvisionalResponse = func(_ *Client, response *sip.Message) {
		if response.StatusCode == 180 {
			cancel()
		}
	}

	_, err = sipClient.Invite(ctx, "sip:2002@example.test")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Invite() error = %v", err)
	}

	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestOutboundInviteAcknowledgesDuplicatesAndTerminatesExtraFork(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer listener.Close()

	serverErrors := make(chan error, 1)
	forkHandled := make(chan struct{})
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErrors <- acceptErr

			return
		}

		defer connection.Close()
		reader := bufio.NewReader(connection)
		invite, parseErr := sip.ParseReader(reader, sip.DefaultLimits())
		if parseErr != nil {
			serverErrors <- parseErr

			return
		}

		selected := responseForTest(invite, 200, "OK")
		selected.Headers.Set("To", invite.Headers.Get("To")+";tag=selected")
		selected.Headers.Add("Contact", "<sip:selected@127.0.0.1:5060>")
		selected.Headers.Add("Content-Type", "application/sdp")
		selected.Body = []byte("v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=test\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 40000 RTP/AVP 0\r\na=sendrecv\r\n")

		if writeErr := writeMessage(connection, selected); writeErr != nil {
			serverErrors <- writeErr

			return
		}

		firstACK, parseErr := sip.ParseReader(reader, sip.DefaultLimits())
		if parseErr != nil || firstACK.Method != "ACK" || headerTag(firstACK.Headers.Get("To")) != "selected" {
			serverErrors <- fmt.Errorf("selected ACK = %#v, error = %v", firstACK, parseErr)

			return
		}

		if writeErr := writeMessage(connection, selected); writeErr != nil {
			serverErrors <- writeErr

			return
		}

		duplicateACK, parseErr := sip.ParseReader(reader, sip.DefaultLimits())
		if parseErr != nil || duplicateACK.Method != "ACK" || headerTag(duplicateACK.Headers.Get("To")) != "selected" {
			serverErrors <- fmt.Errorf("duplicate ACK = %#v, error = %v", duplicateACK, parseErr)

			return
		}

		forked := responseForTest(invite, 200, "OK")
		forked.Headers.Set("To", invite.Headers.Get("To")+";tag=forked")
		forked.Headers.Add("Contact", "<sip:forked@127.0.0.1:5060>")
		forked.Headers.Add("Content-Type", "application/sdp")
		forked.Body = append([]byte(nil), selected.Body...)

		if writeErr := writeMessage(connection, forked); writeErr != nil {
			serverErrors <- writeErr

			return
		}

		forkACK, parseErr := sip.ParseReader(reader, sip.DefaultLimits())
		if parseErr != nil || forkACK.Method != "ACK" || headerTag(forkACK.Headers.Get("To")) != "forked" {
			serverErrors <- fmt.Errorf("fork ACK = %#v, error = %v", forkACK, parseErr)

			return
		}

		forkBYE, parseErr := sip.ParseReader(reader, sip.DefaultLimits())
		if parseErr != nil || forkBYE.Method != "BYE" || headerTag(forkBYE.Headers.Get("To")) != "forked" {
			serverErrors <- fmt.Errorf("fork BYE = %#v, error = %v", forkBYE, parseErr)

			return
		}

		if writeErr := writeMessage(connection, responseForTest(forkBYE, 200, "OK")); writeErr != nil {
			serverErrors <- writeErr

			return
		}

		close(forkHandled)

		selectedBYE, parseErr := sip.ParseReader(reader, sip.DefaultLimits())
		if parseErr != nil || selectedBYE.Method != "BYE" || headerTag(selectedBYE.Headers.Get("To")) != "selected" {
			serverErrors <- fmt.Errorf("selected BYE = %#v, error = %v", selectedBYE, parseErr)

			return
		}

		if writeErr := writeMessage(connection, responseForTest(selectedBYE, 200, "OK")); writeErr != nil {
			serverErrors <- writeErr

			return
		}

		serverErrors <- nil
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	port, err := net.LookupPort("tcp", portText)
	if err != nil {
		t.Fatal(err)
	}

	sipClient, err := NewClient(Config{
		ServerHost:         host,
		ServerPort:         port,
		Username:           "1001",
		TransactionTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	defer sipClient.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	call, err := sipClient.Invite(ctx, "sip:2002@example.test")
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-forkHandled:
	case <-ctx.Done():
		t.Fatal("extra fork was not acknowledged and terminated")
	}

	if err := call.Hangup(ctx); err != nil {
		t.Fatal(err)
	}

	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestInboundReliableCallFlow(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")

	if err != nil {
		t.Fatal(err)
	}

	defer listener.Close()

	serverConnection := make(chan net.Conn, 1)
	serverErrors := make(chan error, 1)
	acceptCall := make(chan struct{})

	go func() {
		connection, acceptErr := listener.Accept()

		if acceptErr != nil {
			serverErrors <- acceptErr

			return
		}

		serverConnection <- connection
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())

	if err != nil {
		t.Fatal(err)
	}

	port, err := net.LookupPort("tcp", portText)

	if err != nil {
		t.Fatal(err)
	}

	sipClient, err := NewClient(Config{
		ServerHost:         host,
		ServerPort:         port,
		Username:           "1001",
		RTPPort:            0,
		TransactionTimeout: 2 * time.Second,
	})

	if err != nil {
		t.Fatal(err)
	}

	defer sipClient.Close()

	inviteReceived := make(chan types.InviteData, 1)

	sipClient.OnInviteReceived = func(_ *Client, invite types.InviteData) {
		inviteReceived <- invite
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sipClient.Start(ctx); err != nil {
		t.Fatal(err)
	}

	connection := <-serverConnection
	defer connection.Close()

	go func() {
		reader := bufio.NewReader(connection)
		invite := sip.NewRequest("INVITE", "sip:1001@127.0.0.1")
		invite.Headers.Add("Via", "SIP/2.0/TCP 127.0.0.1:5060;branch=z9hG4bKinbound")
		invite.Headers.Add("From", "<sip:2002@example.test>;tag=remote")
		invite.Headers.Add("To", "<sip:1001@example.test>")
		invite.Headers.Add("Call-ID", "inbound-call")
		invite.Headers.Add("CSeq", "10 INVITE")
		invite.Headers.Add("Contact", "<sip:2002@127.0.0.1:5060>")
		invite.Headers.Add("Require", "100rel")
		invite.Headers.Add("Content-Type", "application/sdp")
		invite.Body = []byte("v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=test\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 40000 RTP/AVP 0 8\r\na=sendrecv\r\n")

		if writeErr := writeMessage(connection, invite); writeErr != nil {
			serverErrors <- writeErr

			return
		}

		ringing, parseErr := sip.ParseReader(reader, sip.DefaultLimits())

		if parseErr != nil {
			serverErrors <- parseErr

			return
		}

		if ringing.StatusCode != 180 || ringing.Headers.Get("Require") != "100rel" || ringing.Headers.Get("RSeq") == "" {
			serverErrors <- fmt.Errorf("unexpected reliable provisional response: %#v", ringing)

			return
		}

		if deadlineErr := connection.SetReadDeadline(time.Now().Add(time.Second)); deadlineErr != nil {
			serverErrors <- deadlineErr

			return
		}

		retransmittedRinging, parseErr := sip.ParseReader(reader, sip.DefaultLimits())
		if parseErr != nil || retransmittedRinging.StatusCode != 180 || retransmittedRinging.Headers.Get("RSeq") != ringing.Headers.Get("RSeq") {
			serverErrors <- fmt.Errorf("retransmitted provisional response = %#v, error = %v", retransmittedRinging, parseErr)

			return
		}

		if deadlineErr := connection.SetReadDeadline(time.Time{}); deadlineErr != nil {
			serverErrors <- deadlineErr

			return
		}

		prack := sip.NewRequest("PRACK", "sip:1001@127.0.0.1")
		prack.Headers.Add("Via", "SIP/2.0/TCP 127.0.0.1:5060;branch=z9hG4bKprack")
		prack.Headers.Add("From", invite.Headers.Get("From"))
		prack.Headers.Add("To", ringing.Headers.Get("To"))
		prack.Headers.Add("Call-ID", "inbound-call")
		prack.Headers.Add("CSeq", "11 PRACK")
		prack.Headers.Add("RAck", ringing.Headers.Get("RSeq")+" 10 INVITE")

		if writeErr := writeMessage(connection, prack); writeErr != nil {
			serverErrors <- writeErr

			return
		}

		prackResponse, parseErr := sip.ParseReader(reader, sip.DefaultLimits())

		if parseErr != nil || prackResponse.StatusCode != 200 {
			serverErrors <- fmt.Errorf("PRACK response = %#v, error = %v", prackResponse, parseErr)

			return
		}

		close(acceptCall)

		accepted, parseErr := sip.ParseReader(reader, sip.DefaultLimits())

		if parseErr != nil || accepted.StatusCode != 200 || accepted.Headers.Get("Content-Type") != "application/sdp" {
			serverErrors <- fmt.Errorf("INVITE final response = %#v, error = %v", accepted, parseErr)

			return
		}

		ack := sip.NewRequest("ACK", "sip:1001@127.0.0.1")
		ack.Headers.Add("Via", "SIP/2.0/TCP 127.0.0.1:5060;branch=z9hG4bKack")
		ack.Headers.Add("From", invite.Headers.Get("From"))
		ack.Headers.Add("To", accepted.Headers.Get("To"))
		ack.Headers.Add("Call-ID", "inbound-call")
		ack.Headers.Add("CSeq", "10 ACK")

		if writeErr := writeMessage(connection, ack); writeErr != nil {
			serverErrors <- writeErr

			return
		}

		bye := sip.NewRequest("BYE", "sip:1001@127.0.0.1")
		bye.Headers.Add("Via", "SIP/2.0/TCP 127.0.0.1:5060;branch=z9hG4bKbye")
		bye.Headers.Add("From", invite.Headers.Get("From"))
		bye.Headers.Add("To", accepted.Headers.Get("To"))
		bye.Headers.Add("Call-ID", "inbound-call")
		bye.Headers.Add("CSeq", "12 BYE")

		if writeErr := writeMessage(connection, bye); writeErr != nil {
			serverErrors <- writeErr

			return
		}

		byeResponse, parseErr := sip.ParseReader(reader, sip.DefaultLimits())

		if parseErr != nil || byeResponse.StatusCode != 200 {
			serverErrors <- fmt.Errorf("BYE response = %#v, error = %v", byeResponse, parseErr)

			return
		}

		serverErrors <- nil
	}()

	var invite types.InviteData

	select {
	case invite = <-inviteReceived:
	case <-ctx.Done():
		t.Fatal("timed out waiting for INVITE callback")
	}

	select {
	case <-acceptCall:
	case <-ctx.Done():
		t.Fatal("timed out waiting for PRACK")
	}

	sipClient.AcceptInvite(invite)

	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)

	for len(sipClient.Calls()) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if len(sipClient.Calls()) != 0 {
		t.Fatal("call was not removed after BYE")
	}
}

func responseForTest(request *sip.Message, statusCode int, reason string) *sip.Message {
	response := sip.NewResponse(statusCode, reason)

	for _, via := range request.Headers.Values("Via") {
		response.Headers.Add("Via", via)
	}

	response.Headers.Add("From", request.Headers.Get("From"))
	response.Headers.Add("To", request.Headers.Get("To")+";tag=server")
	response.Headers.Add("Call-ID", request.Headers.Get("Call-ID"))
	response.Headers.Add("CSeq", request.Headers.Get("CSeq"))

	return response
}

type addressTransport struct {
	local    net.Addr
	remote   net.Addr
	messages chan *sip.Message
	errors   chan error
	sent     chan *sip.Message
}

func (t *addressTransport) Send(_ context.Context, message *sip.Message) error {
	if t.sent != nil {
		t.sent <- message
	}

	return nil
}

func (t *addressTransport) Messages() <-chan *sip.Message {
	return t.messages
}

func (t *addressTransport) Errors() <-chan error {
	return t.errors
}

func (t *addressTransport) LocalAddr() net.Addr {
	return t.local
}

func (t *addressTransport) RemoteAddr() net.Addr {
	return t.remote
}

func (t *addressTransport) Close() error {
	return nil
}

func TestAdvertisedAddressDoesNotChangeRTPBindAddress(t *testing.T) {
	sipClient, err := NewClient(Config{
		ServerHost:        "sip.example.com",
		Username:          "1001",
		AdvertisedAddress: "[2001:db8::20]:5090",
	})
	if err != nil {
		t.Fatal(err)
	}

	sipClient.mu.Lock()
	sipClient.conn = &addressTransport{
		local:    &net.TCPAddr{IP: net.ParseIP("192.0.2.20"), Port: 42000},
		remote:   &net.TCPAddr{IP: net.ParseIP("192.0.2.30"), Port: 5060},
		messages: make(chan *sip.Message),
		errors:   make(chan error),
	}
	sipClient.mu.Unlock()

	host, port, err := sipClient.localEndpoint()
	if err != nil {
		t.Fatal(err)
	}

	if host != "2001:db8::20" || port != 5090 {
		t.Fatalf("advertised endpoint = %s:%d", host, port)
	}

	bindAddress, err := sipClient.mediaBindAddress(4000)
	if err != nil {
		t.Fatal(err)
	}

	if bindAddress != "192.0.2.20:4000" {
		t.Fatalf("RTP bind address = %q", bindAddress)
	}

	sipClient.LocalAddress = "[2001:db8::30]:0"
	bindAddress, err = sipClient.mediaBindAddress(4002)
	if err != nil {
		t.Fatal(err)
	}

	if bindAddress != "[2001:db8::30]:4002" {
		t.Fatalf("configured IPv6 RTP bind address = %q", bindAddress)
	}
}

type clientDNSLookup struct {
	records []*net.SRV
	count   int
}

func (l *clientDNSLookup) LookupNAPTR(context.Context, string) ([]discovery.NAPTR, error) {
	return nil, errors.New("not found")
}

func (l *clientDNSLookup) LookupSRV(context.Context, string, string, string) (string, []*net.SRV, error) {
	l.count++

	return "", l.records, nil
}

func TestDialSignalingFailsOverAndReusesDNSCache(t *testing.T) {
	lookup := &clientDNSLookup{records: []*net.SRV{
		{Target: "first.example.com.", Port: 5060, Priority: 10, Weight: 1},
		{Target: "second.example.com.", Port: 5060, Priority: 20, Weight: 1},
	}}
	var addresses []string
	dialer := func(_ context.Context, config transport.Config) (transport.Transport, error) {
		addresses = append(addresses, config.RemoteAddr)
		if strings.HasPrefix(config.RemoteAddr, "first.example.com:") {
			return nil, errors.New("first target unavailable")
		}

		return &addressTransport{
			local:    &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 42000},
			remote:   &net.TCPAddr{IP: net.ParseIP("192.0.2.20"), Port: 5060},
			messages: make(chan *sip.Message),
			errors:   make(chan error),
		}, nil
	}

	sipClient, err := NewClient(Config{
		ServerHost:                  "example.com",
		Username:                    "1001",
		Network:                     transport.TCP,
		UseDNSDiscovery:             true,
		DNSLookup:                   lookup,
		DNSCacheTTL:                 time.Minute,
		DisableDNSAddressResolution: true,
		DialTransport:               dialer,
	})
	if err != nil {
		t.Fatal(err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		connection, network, dialErr := sipClient.dialSignaling(context.Background())
		if dialErr != nil {
			t.Fatal(dialErr)
		}

		if network != transport.TCP {
			t.Fatalf("network = %q", network)
		}

		_ = connection.Close()
	}

	if lookup.count != 1 {
		t.Fatalf("DNS lookup count = %d, want 1", lookup.count)
	}

	want := []string{
		"first.example.com:5060",
		"second.example.com:5060",
		"first.example.com:5060",
		"second.example.com:5060",
	}
	if strings.Join(addresses, ",") != strings.Join(want, ",") {
		t.Fatalf("dialed addresses = %#v", addresses)
	}
}

func TestDialSignalingEnforcesTotalFailoverTimeout(t *testing.T) {
	lookup := &clientDNSLookup{records: []*net.SRV{
		{Target: "first.example.com.", Port: 5060, Priority: 10, Weight: 1},
		{Target: "second.example.com.", Port: 5060, Priority: 20, Weight: 1},
	}}
	dialCalls := 0
	dialer := func(ctx context.Context, _ transport.Config) (transport.Transport, error) {
		dialCalls++
		<-ctx.Done()

		return nil, ctx.Err()
	}

	sipClient, err := NewClient(Config{
		ServerHost:                  "example.com",
		Username:                    "1001",
		Network:                     transport.TCP,
		UseDNSDiscovery:             true,
		DNSLookup:                   lookup,
		DisableDNSAddressResolution: true,
		FailoverTimeout:             20 * time.Millisecond,
		DialTransport:               dialer,
	})
	if err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	_, _, err = sipClient.dialSignaling(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("dial error = %v", err)
	}

	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("failover exceeded total deadline: %v", elapsed)
	}

	if dialCalls != 1 {
		t.Fatalf("dial calls after deadline = %d, want 1", dialCalls)
	}
}

func TestInboundCANCELMustMatchInviteTransaction(t *testing.T) {
	sipClient, err := NewClient(Config{ServerHost: "example.com", Username: "1001"})
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

	invite := sip.NewRequest("INVITE", "sip:1001@example.com")
	invite.Headers.Add("Via", "SIP/2.0/TCP peer.example.com;branch=z9hG4bK-original")
	invite.Headers.Add("From", "<sip:2002@example.com>;tag=remote")
	invite.Headers.Add("To", "<sip:1001@example.com>")
	invite.Headers.Add("Call-ID", "cancel-call")
	invite.Headers.Add("CSeq", "42 INVITE")
	call := &Call{
		client:     sipClient,
		ID:         "cancel-call",
		LocalTag:   "local",
		state:      CallRinging,
		invite:     invite,
		inviteCSeq: 42,
	}

	sipClient.mu.Lock()
	sipClient.calls[call.ID] = call
	sipClient.mu.Unlock()

	wrong := sip.NewRequest("CANCEL", invite.RequestURI)
	wrong.Headers.Add("Via", "SIP/2.0/TCP peer.example.com;branch=z9hG4bK-wrong")
	wrong.Headers.Add("From", invite.Headers.Get("From"))
	wrong.Headers.Add("To", invite.Headers.Get("To"))
	wrong.Headers.Add("Call-ID", call.ID)
	wrong.Headers.Add("CSeq", "42 CANCEL")
	sipClient.handleCANCEL(wrong)

	if response := <-sent; response.StatusCode != 481 {
		t.Fatalf("mismatched CANCEL response = %d", response.StatusCode)
	}

	if len(sipClient.Calls()) != 1 {
		t.Fatal("mismatched CANCEL removed the call")
	}

	matching := sip.NewRequest("CANCEL", invite.RequestURI)
	matching.Headers.Add("Via", invite.Headers.Get("Via"))
	matching.Headers.Add("From", invite.Headers.Get("From"))
	matching.Headers.Add("To", invite.Headers.Get("To"))
	matching.Headers.Add("Call-ID", call.ID)
	matching.Headers.Add("CSeq", "42 CANCEL")
	sipClient.handleCANCEL(matching)

	if response := <-sent; response.StatusCode != 200 {
		t.Fatalf("matching CANCEL response = %d", response.StatusCode)
	}

	if response := <-sent; response.StatusCode != 487 || response.Headers.Get("CSeq") != "42 INVITE" {
		t.Fatalf("terminated INVITE response = %#v", response)
	}

	if len(sipClient.Calls()) != 0 || call.State() != CallEnded {
		t.Fatal("matching CANCEL did not terminate the call")
	}
}

func writeMessage(connection net.Conn, message *sip.Message) error {
	payload, err := message.MarshalBinary()

	if err != nil {
		return err
	}

	_, err = connection.Write(payload)

	return err
}
