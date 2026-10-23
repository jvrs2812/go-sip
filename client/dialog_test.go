package client

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/jvrs2812/go-sip/media"
	"github.com/jvrs2812/go-sip/sdp"
	"github.com/jvrs2812/go-sip/sip"
)

func TestOutboundEarlyMediaBeforeFinalResponse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer listener.Close()

	remoteMedia, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer remoteMedia.Close()

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

		answer := audioOffer(t, remoteMedia.LocalAddr().(*net.UDPAddr).Port, sdp.SendRecv)
		progress := responseForTest(invite, 183, "Session Progress")
		progress.Headers.Add("Contact", "<sip:2002@127.0.0.1>")
		progress.Headers.Add("Content-Type", "application/sdp")
		progress.Body = answer
		if writeErr := writeMessage(connection, progress); writeErr != nil {
			serverErrors <- writeErr

			return
		}

		if deadlineErr := remoteMedia.SetReadDeadline(time.Now().Add(time.Second)); deadlineErr != nil {
			serverErrors <- deadlineErr

			return
		}

		buffer := make([]byte, 1500)
		count, _, readErr := remoteMedia.ReadFrom(buffer)
		if readErr != nil {
			serverErrors <- readErr

			return
		}

		packet, parseErr := media.ParseRTPPacket(buffer[:count])
		if parseErr != nil || string(packet.Payload) != "early" {
			serverErrors <- fmt.Errorf("early RTP packet = %#v, error = %v", packet, parseErr)

			return
		}

		accepted := responseForTest(invite, 200, "OK")
		accepted.Headers.Add("Contact", "<sip:2002@127.0.0.1>")
		accepted.Headers.Add("Content-Type", "application/sdp")
		accepted.Body = answer
		if writeErr := writeMessage(connection, accepted); writeErr != nil {
			serverErrors <- writeErr

			return
		}

		ack, parseErr := sip.ParseReader(reader, sip.DefaultLimits())
		if parseErr != nil || ack.Method != "ACK" {
			serverErrors <- fmt.Errorf("ACK = %#v, error = %v", ack, parseErr)

			return
		}

		bye, parseErr := sip.ParseReader(reader, sip.DefaultLimits())
		if parseErr != nil || bye.Method != "BYE" {
			serverErrors <- fmt.Errorf("BYE = %#v, error = %v", bye, parseErr)

			return
		}

		serverErrors <- writeMessage(connection, responseForTest(bye, 200, "OK"))
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
		Username:           "1001",
		TransactionTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	defer sipClient.Close()

	earlyState := make(chan CallState, 1)
	earlyErrors := make(chan error, 1)
	sipClient.OnEarlyMedia = func(_ *Client, call *Call, _ *sip.Message) {
		earlyState <- call.State()

		sendCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		earlyErrors <- call.SendAudio(sendCtx, []byte("early"), true)
	}

	established := make(chan *Call, 1)
	sipClient.OnCallEstablished = func(_ *Client, call *Call) {
		established <- call
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	call, err := sipClient.Invite(ctx, "sip:2002@example.test")
	if err != nil {
		t.Fatalf("Invite() error = %v", err)
	}

	if state := <-earlyState; state != CallEarlyMedia {
		t.Fatalf("early call state = %q", state)
	}

	if err := <-earlyErrors; err != nil {
		t.Fatalf("send early media: %v", err)
	}

	if notified := <-established; notified != call || call.State() != CallActive {
		t.Fatalf("established call = %p, returned = %p, state = %q", notified, call, call.State())
	}

	if err := call.Hangup(ctx); err != nil {
		t.Fatalf("Hangup() error = %v", err)
	}

	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestInboundReinviteAndOfferCollision(t *testing.T) {
	rtpSession, err := media.NewRTPSession(media.RTPConfig{LocalAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}

	defer rtpSession.Close()

	remoteMedia, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer remoteMedia.Close()

	remotePort := remoteMedia.LocalAddr().(*net.UDPAddr).Port
	sipClient, err := NewClient(Config{ServerHost: "example.test", Username: "1001"})
	if err != nil {
		t.Fatal(err)
	}

	sent := make(chan *sip.Message, 4)
	sipClient.mu.Lock()
	sipClient.conn = &addressTransport{
		local:    &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5060},
		remote:   &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5060},
		messages: make(chan *sip.Message),
		errors:   make(chan error),
		sent:     sent,
	}
	call := &Call{
		client:       sipClient,
		ID:           "reinvite-call",
		LocalTag:     "local",
		RemoteTag:    "remote",
		LocalURI:     "<sip:1001@example.test>;tag=local",
		RemoteURI:    "<sip:2002@example.test>;tag=remote",
		RemoteTarget: "sip:2002@127.0.0.1",
		state:        CallActive,
		remoteCSeq:   1,
		media:        rtpSession,
	}
	sipClient.calls[call.ID] = call
	sipClient.mu.Unlock()

	offer := audioOffer(t, remotePort, sdp.SendOnly)
	reinvite := dialogRequest("INVITE", call.ID, 2, offer)
	sipClient.handleInvite(reinvite)

	response := <-sent
	if response.StatusCode != 200 || response.Headers.Get("Content-Type") != "application/sdp" {
		t.Fatalf("re-INVITE response = %#v", response)
	}

	answer, err := sdp.Parse(response.Body)
	if err != nil {
		t.Fatal(err)
	}

	if answer.Media[0].Direction != sdp.RecvOnly {
		t.Fatalf("answer direction = %q", answer.Media[0].Direction)
	}

	overlap := dialogRequest("INVITE", call.ID, 3, offer)
	sipClient.handleInvite(overlap)

	if response := <-sent; response.StatusCode != 491 {
		t.Fatalf("overlapping offer response = %d", response.StatusCode)
	}

	ack := dialogRequest("ACK", call.ID, 2, nil)
	sipClient.handleACK(ack)

	call.mu.Lock()
	pending := call.remoteOfferPending
	call.mu.Unlock()

	if pending {
		t.Fatal("ACK did not clear pending remote offer")
	}

	call.mu.Lock()
	call.localOfferPending = true
	call.mu.Unlock()
	sipClient.handleInvite(dialogRequest("INVITE", call.ID, 3, offer))

	if response := <-sent; response.StatusCode != 491 {
		t.Fatalf("simultaneous offer response = %d", response.StatusCode)
	}
}

func TestCallEstablishedCallbackRunsOnce(t *testing.T) {
	sipClient, err := NewClient(Config{ServerHost: "example.test", Username: "1001"})
	if err != nil {
		t.Fatal(err)
	}

	called := make(chan *Call, 2)
	sipClient.OnCallEstablished = func(_ *Client, call *Call) {
		called <- call
	}

	call := &Call{client: sipClient, ID: "established"}
	call.notifyEstablished()
	call.notifyEstablished()

	select {
	case received := <-called:
		if received != call {
			t.Fatal("callback received a different call")
		}
	case <-time.After(time.Second):
		t.Fatal("OnCallEstablished was not called")
	}

	select {
	case <-called:
		t.Fatal("OnCallEstablished was called more than once")
	default:
	}
}

func audioOffer(t *testing.T, port int, direction sdp.Direction) []byte {
	t.Helper()

	description := sdp.Description{
		OriginID:      1,
		OriginVersion: 1,
		SessionName:   "test",
		Connection:    net.ParseIP("127.0.0.1"),
		Media: []sdp.Media{{
			Type:       "audio",
			Port:       port,
			Protocol:   "RTP/AVP",
			Formats:    []uint8{0},
			Connection: net.ParseIP("127.0.0.1"),
			Direction:  direction,
			Codecs: map[uint8]sdp.Codec{
				0: {PayloadType: 0, Name: "PCMU", ClockRate: 8000, Channels: 1},
			},
		}},
	}

	body, err := description.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	return body
}

func dialogRequest(method, callID string, sequence uint32, body []byte) *sip.Message {
	request := sip.NewRequest(method, "sip:1001@example.test")
	request.Headers.Add("Via", "SIP/2.0/TCP peer.example.test;branch=z9hG4bK-"+strconv.FormatUint(uint64(sequence), 10)+method)
	request.Headers.Add("From", "<sip:2002@example.test>;tag=remote")
	request.Headers.Add("To", "<sip:1001@example.test>;tag=local")
	request.Headers.Add("Call-ID", callID)
	request.Headers.Add("CSeq", strconv.FormatUint(uint64(sequence), 10)+" "+method)
	request.Headers.Add("Contact", "<sip:2002@127.0.0.1>")

	if len(body) > 0 {
		request.Headers.Add("Content-Type", "application/sdp")
		request.Body = append([]byte(nil), body...)
	}

	return request
}
