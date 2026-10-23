package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jvrs2812/go-sip/media"
	"github.com/jvrs2812/go-sip/sdp"
	"github.com/jvrs2812/go-sip/sip"
	"github.com/jvrs2812/go-sip/types"
)

type CallState string

var ErrCallCancelled = errors.New("call was cancelled before final response")

const (
	CallRinging      CallState = "ringing"
	CallEstablishing CallState = "establishing"
	CallEarlyMedia   CallState = "early-media"
	CallActive       CallState = "active"
	CallEnded        CallState = "ended"
)

type Call struct {
	client       *Client
	ID           string
	LocalTag     string
	RemoteTag    string
	LocalURI     string
	RemoteURI    string
	RemoteTarget string

	mu                 sync.Mutex
	state              CallState
	invite             *sip.Message
	inviteCSeq         uint32
	remoteCSeq         uint32
	localCSeq          uint32
	media              *media.RTPSession
	codec              sdp.Codec
	firstMedia         sync.Once
	mediaPump          sync.Once
	established        sync.Once
	mediaDone          chan struct{}
	routeSet           []string
	rseq               uint32
	prackRequired      bool
	prackReceived      bool
	prackDone          chan struct{}
	prackCancel        context.CancelFunc
	prackOnce          sync.Once
	dtmfPayload        uint8
	hasDTMF            bool
	held               bool
	finalResponseSent  bool
	localOfferPending  bool
	remoteOfferPending bool
	pendingOfferCSeq   uint32
}

type cachedServerResponse struct {
	message   *sip.Message
	createdAt time.Time
}

func (c *Client) Calls() []*Call {
	c.ensureSynchronization()

	c.mu.Lock()
	defer c.mu.Unlock()

	calls := make([]*Call, 0, len(c.calls))

	for _, call := range c.calls {
		calls = append(calls, call)
	}

	return calls
}

func (call *Call) State() CallState {
	call.mu.Lock()
	defer call.mu.Unlock()

	return call.state
}

func (call *Call) Codec() sdp.Codec {
	call.mu.Lock()
	defer call.mu.Unlock()

	return call.codec
}

func (call *Call) Accept(ctx context.Context) error {
	call.mu.Lock()

	if call.state != CallRinging {
		state := call.state
		call.mu.Unlock()

		return fmt.Errorf("cannot accept call in state %s", state)
	}

	invite := call.invite
	prackRequired := call.prackRequired
	prackReceived := call.prackReceived
	prackDone := call.prackDone
	call.state = CallEstablishing
	call.mu.Unlock()

	if invite == nil {
		return errors.New("call has no INVITE request")
	}

	if prackRequired && !prackReceived {
		select {
		case <-prackDone:
		case <-ctx.Done():
			call.setState(CallRinging)

			return fmt.Errorf("wait for PRACK: %w", ctx.Err())
		}
	}

	call.mu.Lock()
	cancelled := call.state == CallEnded
	call.mu.Unlock()

	if cancelled {
		return ErrCallCancelled
	}

	offer, err := sdp.Parse(invite.Body)
	if err != nil {
		call.setState(CallRinging)

		return fmt.Errorf("parse remote SDP offer: %w", err)
	}

	localIP, err := call.client.mediaLocalIP()
	if err != nil {
		call.setState(CallRinging)

		return err
	}

	localAddress, err := call.client.mediaBindAddress(call.client.PortForRtp)

	if err != nil {
		call.setState(CallRinging)

		return err
	}

	rtpSession, err := media.NewRTPSession(media.RTPConfig{
		LocalAddr:        localAddress,
		ClockRate:        8000,
		SamplesPerPacket: 160,
		SymmetricRTP:     true,
		Pacing:           true,
	})
	if err != nil {
		call.setState(CallRinging)

		return fmt.Errorf("open RTP listener: %w", err)
	}

	_, localPortText, err := net.SplitHostPort(rtpSession.LocalAddr().String())
	if err != nil {
		_ = rtpSession.Close()
		call.setState(CallRinging)

		return err
	}

	localPort, err := strconv.Atoi(localPortText)
	if err != nil {
		_ = rtpSession.Close()
		call.setState(CallRinging)

		return err
	}

	answer, selected, err := sdp.NegotiateAudio(offer, localIP, localPort, supportedAudioCodecs())
	if err != nil {
		_ = rtpSession.Close()
		call.setState(CallRinging)

		return err
	}

	remoteMedia := firstActiveAudio(offer)
	remoteAddress := net.JoinHostPort(remoteMedia.Connection.String(), strconv.Itoa(remoteMedia.Port))

	if err := rtpSession.SetRemote(remoteAddress); err != nil {
		_ = rtpSession.Close()
		call.setState(CallRinging)

		return err
	}

	rtpSession.ConfigurePayload(selected.PayloadType, uint32(selected.ClockRate), samplesPerPacket(selected))

	answerBody, err := answer.Marshal()
	if err != nil {
		_ = rtpSession.Close()
		call.setState(CallRinging)

		return err
	}

	response := call.client.responseFor(invite, 200, "OK", call.LocalTag)
	localHost, localSIPPort, err := call.client.localEndpoint()
	if err != nil {
		_ = rtpSession.Close()
		call.setState(CallRinging)

		return err
	}

	response.Headers.Add("Contact", fmt.Sprintf("<%s>", call.client.contactURI(localHost, localSIPPort)))
	response.Headers.Add("Content-Type", "application/sdp")
	response.Body = answerBody

	call.mu.Lock()
	if call.state == CallEnded {
		call.mu.Unlock()
		_ = rtpSession.Close()

		return ErrCallCancelled
	}

	call.finalResponseSent = true
	call.mu.Unlock()

	call.client.cacheServerResponse(invite, response)

	if err := call.client.send(ctx, response); err != nil {
		_ = rtpSession.Close()
		call.setState(CallRinging)

		return err
	}

	call.mu.Lock()
	call.media = rtpSession
	call.codec = selected
	call.mediaDone = make(chan struct{})
	call.dtmfPayload, call.hasDTMF = sdp.TelephoneEventPayload(answer)
	call.mu.Unlock()

	call.client.startMediaPump(call)

	return nil
}

func (call *Call) Reject(ctx context.Context, statusCode int, reason string) error {
	if statusCode < 300 || statusCode > 699 {
		return errors.New("reject status must be between 300 and 699")
	}

	call.mu.Lock()

	if call.state != CallRinging && call.state != CallEstablishing {
		state := call.state
		call.mu.Unlock()

		return fmt.Errorf("cannot reject call in state %s", state)
	}

	invite := call.invite
	call.state = CallEnded
	call.mu.Unlock()

	response := call.client.responseFor(invite, statusCode, reason, call.LocalTag)
	call.client.cacheServerResponse(invite, response)

	if err := call.client.send(ctx, response); err != nil {
		return err
	}

	call.client.removeCall(call.ID)

	return nil
}

func (call *Call) Hangup(ctx context.Context) error {
	call.mu.Lock()

	if call.state != CallActive && call.state != CallEstablishing {
		state := call.state
		call.mu.Unlock()

		return fmt.Errorf("cannot hang up call in state %s", state)
	}

	request := sip.NewRequest("BYE", call.RemoteTarget)
	call.mu.Unlock()

	if err := call.client.addDialogHeaders(request, call, "BYE"); err != nil {
		return err
	}

	response, err := call.client.roundTrip(ctx, request)
	if err != nil {
		return err
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("BYE failed: %d %s", response.StatusCode, response.Reason)
	}

	call.setState(CallEnded)
	_ = call.closeMedia()
	call.client.removeCall(call.ID)

	return nil
}

func (call *Call) SendAudio(ctx context.Context, payload []byte, marker bool) error {
	call.mu.Lock()
	rtpSession := call.media
	state := call.state
	call.mu.Unlock()

	if rtpSession == nil {
		return errors.New("call media is not initialized")
	}

	if state == CallEnded {
		return errors.New("call has ended")
	}

	return rtpSession.Send(ctx, payload, marker)
}

func (call *Call) SendDTMF(ctx context.Context, digit rune, duration time.Duration) error {
	call.mu.Lock()
	rtpSession := call.media
	payloadType := call.dtmfPayload
	hasDTMF := call.hasDTMF
	call.mu.Unlock()

	if rtpSession == nil {
		return errors.New("call media is not initialized")
	}

	if !hasDTMF {
		return errors.New("telephone-event was not negotiated")
	}

	eventCode, err := media.DTMFEventCode(digit)

	if err != nil {
		return err
	}

	return rtpSession.SendTelephoneEvent(ctx, payloadType, eventCode, duration)
}

func (call *Call) Hold(ctx context.Context) error {
	return call.renegotiate(ctx, sdp.SendOnly, true)
}

func (call *Call) Resume(ctx context.Context) error {
	return call.renegotiate(ctx, sdp.SendRecv, false)
}

func (call *Call) IsHeld() bool {
	call.mu.Lock()
	defer call.mu.Unlock()

	return call.held
}

func (call *Call) renegotiate(ctx context.Context, direction sdp.Direction, held bool) error {
	call.mu.Lock()
	rtpSession := call.media
	codec := call.codec
	dtmfPayload := call.dtmfPayload
	hasDTMF := call.hasDTMF
	state := call.state
	if call.localOfferPending || call.remoteOfferPending {
		call.mu.Unlock()

		return errors.New("another SDP offer is already pending")
	}

	call.localOfferPending = true
	call.mu.Unlock()
	defer func() {
		call.mu.Lock()
		call.localOfferPending = false
		call.mu.Unlock()
	}()

	if state != CallActive {
		return fmt.Errorf("cannot renegotiate call in state %s", state)
	}

	if rtpSession == nil {
		return errors.New("call media is not initialized")
	}

	localIP, err := call.client.mediaLocalIP()

	if err != nil {
		return err
	}

	_, portText, err := net.SplitHostPort(rtpSession.LocalAddr().String())

	if err != nil {
		return err
	}

	port, err := strconv.Atoi(portText)

	if err != nil {
		return err
	}

	formats := []uint8{codec.PayloadType}
	codecs := map[uint8]sdp.Codec{codec.PayloadType: codec}

	if hasDTMF {
		formats = append(formats, dtmfPayload)
		codecs[dtmfPayload] = sdp.Codec{PayloadType: dtmfPayload, Name: "telephone-event", ClockRate: codec.ClockRate, Channels: 1, FMTP: "0-16"}
	}

	origin := uint64(time.Now().UnixNano())
	offer := sdp.Description{
		OriginID:      origin,
		OriginVersion: origin,
		SessionName:   "go-sip",
		Connection:    localIP,
		Media: []sdp.Media{{
			Type:       "audio",
			Port:       port,
			Protocol:   "RTP/AVP",
			Formats:    formats,
			Connection: localIP,
			Direction:  direction,
			Codecs:     codecs,
		}},
	}

	body, err := offer.Marshal()

	if err != nil {
		return err
	}

	request := sip.NewRequest("INVITE", call.RemoteTarget)

	if err := call.client.addDialogHeaders(request, call, "INVITE"); err != nil {
		return err
	}

	request.Headers.Add("Content-Type", "application/sdp")
	request.Body = body

	response, err := call.client.successfulResponse(ctx, request)

	if err != nil {
		return err
	}

	sequence, _, err := request.CSeq()

	if err != nil {
		return err
	}

	if err := call.client.sendInviteACK(ctx, request, response, call.RemoteTarget, sequence); err != nil {
		return err
	}

	if len(response.Body) > 0 {
		answer, parseErr := sdp.Parse(response.Body)

		if parseErr != nil {
			return parseErr
		}

		remoteMedia := firstActiveAudio(answer)

		if remoteMedia.Connection != nil && remoteMedia.Port != 0 {
			remoteAddress := net.JoinHostPort(remoteMedia.Connection.String(), strconv.Itoa(remoteMedia.Port))

			if err := rtpSession.SetRemote(remoteAddress); err != nil {
				return err
			}
		}
	}

	call.mu.Lock()
	call.held = held
	call.mu.Unlock()

	return nil
}

func (call *Call) setState(state CallState) {
	call.mu.Lock()
	call.state = state
	call.mu.Unlock()
}

func (call *Call) notifyEstablished() {
	call.established.Do(func() {
		if call.client.OnCallEstablished != nil {
			call.client.OnCallEstablished(call.client, call)
		}
	})
}

func (call *Call) nextCSeq() uint32 {
	call.mu.Lock()
	defer call.mu.Unlock()

	call.localCSeq++

	return call.localCSeq
}

func (call *Call) closeMedia() error {
	call.mu.Lock()
	rtpSession := call.media
	call.media = nil
	call.mu.Unlock()

	if rtpSession == nil {
		return nil
	}

	return rtpSession.Close()
}

func (c *Client) handleInvite(request *sip.Message) {
	callID := strings.TrimSpace(request.Headers.Get("Call-ID"))

	if callID == "" {
		c.sendResponse(request, 400, "Bad Request", "")

		return
	}

	sequence, method, err := request.CSeq()

	if err != nil || method != "INVITE" {
		c.sendResponse(request, 400, "Bad Request", "")

		return
	}

	c.mu.Lock()
	existing := c.calls[callID]
	c.mu.Unlock()

	if existing != nil && existing.State() != CallEnded {
		if !existing.matchesInboundRequest(request) {
			c.sendResponse(request, 481, "Call/Transaction Does Not Exist", "")

			return
		}

		c.handleReinvite(existing, request, sequence)

		return
	}

	localTag, err := randomToken(10)

	if err != nil {
		c.reportError(err)

		return
	}

	call := &Call{
		client:       c,
		ID:           callID,
		LocalTag:     localTag,
		RemoteTag:    headerTag(request.Headers.Get("From")),
		LocalURI:     request.Headers.Get("To"),
		RemoteURI:    request.Headers.Get("From"),
		RemoteTarget: contactURI(request.Headers.Get("Contact")),
		state:        CallRinging,
		invite:       request,
		inviteCSeq:   sequence,
		remoteCSeq:   sequence,
		localCSeq:    1,
		routeSet:     append([]string(nil), request.Headers.Values("Record-Route")...),
	}

	if call.RemoteTarget == "" {
		call.RemoteTarget = request.RequestURI
	}

	c.mu.Lock()
	c.calls[callID] = call
	c.mu.Unlock()

	ringing := c.responseFor(request, 180, "Ringing", localTag)

	if headerHasToken(request.Headers.Get("Require"), "100rel") || headerHasToken(request.Headers.Get("Supported"), "100rel") {
		rseq, sequenceErr := randomUint32()

		if sequenceErr != nil {
			c.reportError(sequenceErr)

			return
		}

		if rseq == 0 {
			rseq = 1
		}

		call.mu.Lock()
		call.rseq = rseq
		call.prackRequired = true
		call.prackDone = make(chan struct{})
		call.mu.Unlock()

		ringing.Headers.Add("Require", "100rel")
		ringing.Headers.Add("RSeq", strconv.FormatUint(uint64(rseq), 10))
	}

	c.sendServerResponse(request, ringing)

	call.mu.Lock()
	reliable := call.prackRequired
	call.mu.Unlock()

	if reliable {
		c.startReliableProvisional(call, request, ringing)
	}

	if c.OnInviteReceived == nil {
		return
	}

	inviteData := types.InviteData{
		Via:      request.Headers.Get("Via"),
		From:     request.Headers.Get("From"),
		To:       request.Headers.Get("To"),
		CallID:   callID,
		Cseq:     request.Headers.Get("CSeq"),
		Contact:  request.Headers.Get("Contact"),
		Body:     append([]byte(nil), request.Body...),
		Request:  request,
		LocalTag: localTag,
	}

	go c.OnInviteReceived(c, inviteData)
}

func (c *Client) handleACK(request *sip.Message) {
	callID := strings.TrimSpace(request.Headers.Get("Call-ID"))

	c.mu.Lock()
	call := c.calls[callID]
	c.mu.Unlock()

	if call != nil && call.matchesInboundRequest(request) {
		sequence, _, _ := request.CSeq()

		call.mu.Lock()
		if call.remoteOfferPending && sequence == call.pendingOfferCSeq {
			call.remoteOfferPending = false
			call.pendingOfferCSeq = 0
		}
		call.mu.Unlock()

		call.setState(CallActive)
		call.notifyEstablished()
	}
}

func (c *Client) handleReinvite(call *Call, request *sip.Message, sequence uint32) {
	call.mu.Lock()
	if call.localOfferPending || call.remoteOfferPending {
		call.mu.Unlock()
		c.sendResponse(request, 491, "Request Pending", call.LocalTag)

		return
	}

	if sequence <= call.remoteCSeq {
		call.mu.Unlock()
		c.sendResponse(request, 500, "Server Internal Error", call.LocalTag)

		return
	}

	rtpSession := call.media
	call.remoteOfferPending = true
	call.pendingOfferCSeq = sequence
	call.mu.Unlock()

	clearPending := func() {
		call.mu.Lock()
		call.remoteOfferPending = false
		call.pendingOfferCSeq = 0
		call.mu.Unlock()
	}

	if rtpSession == nil {
		clearPending()
		c.sendResponse(request, 488, "Not Acceptable Here", call.LocalTag)

		return
	}

	offer, err := sdp.Parse(request.Body)
	if err != nil {
		clearPending()
		c.sendResponse(request, 488, "Not Acceptable Here", call.LocalTag)

		return
	}

	localIP, err := c.mediaLocalIP()
	if err != nil {
		clearPending()
		c.sendResponse(request, 500, "Server Internal Error", call.LocalTag)

		return
	}

	_, portText, err := net.SplitHostPort(rtpSession.LocalAddr().String())
	if err != nil {
		clearPending()
		c.sendResponse(request, 500, "Server Internal Error", call.LocalTag)

		return
	}

	port, err := strconv.Atoi(portText)
	if err != nil {
		clearPending()
		c.sendResponse(request, 500, "Server Internal Error", call.LocalTag)

		return
	}

	answer, selected, err := sdp.NegotiateAudio(offer, localIP, port, supportedAudioCodecs())
	if err != nil {
		clearPending()
		c.sendResponse(request, 488, "Not Acceptable Here", call.LocalTag)

		return
	}

	answerBody, err := answer.Marshal()
	if err != nil {
		clearPending()
		c.sendResponse(request, 500, "Server Internal Error", call.LocalTag)

		return
	}

	remoteMedia := firstActiveAudio(offer)
	if remoteMedia.Connection != nil && remoteMedia.Port != 0 {
		remoteAddress := net.JoinHostPort(remoteMedia.Connection.String(), strconv.Itoa(remoteMedia.Port))
		if err := rtpSession.SetRemote(remoteAddress); err != nil {
			clearPending()
			c.sendResponse(request, 488, "Not Acceptable Here", call.LocalTag)

			return
		}
	}

	rtpSession.ConfigurePayload(selected.PayloadType, uint32(selected.ClockRate), samplesPerPacket(selected))
	dtmfPayload, hasDTMF := sdp.TelephoneEventPayload(answer)

	call.mu.Lock()
	call.remoteCSeq = sequence
	call.codec = selected
	call.dtmfPayload = dtmfPayload
	call.hasDTMF = hasDTMF
	if target := contactURI(request.Headers.Get("Contact")); target != "" {
		call.RemoteTarget = target
	}
	call.mu.Unlock()

	localHost, localPort, err := c.localEndpoint()
	if err != nil {
		clearPending()
		c.sendResponse(request, 500, "Server Internal Error", call.LocalTag)

		return
	}

	response := c.responseFor(request, 200, "OK", call.LocalTag)
	response.Headers.Add("Contact", fmt.Sprintf("<%s>", c.contactURI(localHost, localPort)))
	response.Headers.Add("Content-Type", "application/sdp")
	response.Body = answerBody
	c.sendServerResponse(request, response)
}

func (c *Client) handleBYE(request *sip.Message) {
	callID := strings.TrimSpace(request.Headers.Get("Call-ID"))

	c.mu.Lock()
	call := c.calls[callID]
	c.mu.Unlock()

	if call == nil || !call.matchesInboundRequest(request) {
		c.sendResponse(request, 481, "Call/Transaction Does Not Exist", "")

		return
	}

	c.sendResponse(request, 200, "OK", call.LocalTag)

	call.setState(CallEnded)
	_ = call.closeMedia()
	c.removeCall(callID)
}

func (c *Client) handleCANCEL(request *sip.Message) {
	callID := strings.TrimSpace(request.Headers.Get("Call-ID"))
	sequence, method, cseqErr := request.CSeq()

	c.mu.Lock()
	call := c.calls[callID]
	c.mu.Unlock()

	if call == nil {
		c.sendResponse(request, 481, "Call/Transaction Does Not Exist", "")

		return
	}

	call.mu.Lock()
	invite := call.invite
	state := call.state
	finalResponseSent := call.finalResponseSent
	inviteSequence := call.inviteCSeq

	matches := cseqErr == nil && method == "CANCEL" && sequence == inviteSequence &&
		invite != nil && topViaBranch(request.Headers.Get("Via")) == topViaBranch(invite.Headers.Get("Via"))

	if !matches || finalResponseSent || (state != CallRinging && state != CallEstablishing) {
		call.mu.Unlock()
		c.sendResponse(request, 481, "Call/Transaction Does Not Exist", call.LocalTag)

		return
	}

	call.state = CallEnded
	if call.prackCancel != nil {
		call.prackCancel()
		call.prackCancel = nil
	}

	if call.prackDone != nil {
		call.prackOnce.Do(func() {
			close(call.prackDone)
		})
	}
	call.mu.Unlock()

	c.sendResponse(request, 200, "OK", call.LocalTag)
	c.sendResponse(invite, 487, "Request Terminated", call.LocalTag)
	_ = call.closeMedia()
	c.removeCall(callID)
}

func (c *Client) handleOPTIONS(request *sip.Message) {
	response := c.responseFor(request, 200, "OK", headerTag(request.Headers.Get("To")))
	response.Headers.Add("Allow", "INVITE, ACK, BYE, CANCEL, OPTIONS, MESSAGE, INFO, REFER, SUBSCRIBE, NOTIFY, PUBLISH, PRACK")
	response.Headers.Add("Accept", "application/sdp, application/pidf+xml, text/plain")
	response.Headers.Add("Supported", "100rel, replaces, timer")

	c.sendServerResponse(request, response)
}

func (c *Client) handlePRACK(request *sip.Message) {
	callID := strings.TrimSpace(request.Headers.Get("Call-ID"))

	c.mu.Lock()
	call := c.calls[callID]
	c.mu.Unlock()

	if call == nil || !call.matchesInboundRequest(request) {
		c.sendResponse(request, 481, "Call/Transaction Does Not Exist", "")

		return
	}

	rackParts := strings.Fields(request.Headers.Get("RAck"))

	if len(rackParts) != 3 || !strings.EqualFold(rackParts[2], "INVITE") {
		c.sendResponse(request, 400, "Bad Request", call.LocalTag)

		return
	}

	reliableSequence, rseqErr := strconv.ParseUint(rackParts[0], 10, 32)
	inviteSequence, cseqErr := strconv.ParseUint(rackParts[1], 10, 32)

	call.mu.Lock()
	valid := rseqErr == nil && cseqErr == nil && uint32(reliableSequence) == call.rseq && uint32(inviteSequence) == call.inviteCSeq

	if valid {
		call.prackReceived = true
		call.prackOnce.Do(func() {
			close(call.prackDone)
		})

		if call.prackCancel != nil {
			call.prackCancel()
			call.prackCancel = nil
		}
	}

	call.mu.Unlock()

	if !valid {
		c.sendResponse(request, 481, "Call/Transaction Does Not Exist", call.LocalTag)

		return
	}

	c.sendResponse(request, 200, "OK", call.LocalTag)
}

func (call *Call) matchesInboundRequest(request *sip.Message) bool {
	if call == nil || request == nil {
		return false
	}

	if strings.TrimSpace(request.Headers.Get("Call-ID")) != call.ID {
		return false
	}

	localTag := headerTag(request.Headers.Get("To"))
	remoteTag := headerTag(request.Headers.Get("From"))

	return localTag != "" && remoteTag != "" && localTag == call.LocalTag && remoteTag == call.RemoteTag
}

func (c *Client) startReliableProvisional(call *Call, invite, response *sip.Message) {
	ctx, cancel := context.WithCancel(c.doneContext())

	call.mu.Lock()
	if call.prackReceived || call.state == CallEnded {
		call.mu.Unlock()
		cancel()

		return
	}

	call.prackCancel = cancel
	call.mu.Unlock()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		cancel()

		return
	}

	c.wg.Add(1)
	c.mu.Unlock()

	go func() {
		defer c.wg.Done()

		interval := 500 * time.Millisecond
		timer := time.NewTimer(interval)
		defer timer.Stop()

		deadline := time.NewTimer(64 * 500 * time.Millisecond)
		defer deadline.Stop()

		for {
			select {
			case <-timer.C:
				sendCtx, sendCancel := context.WithTimeout(ctx, c.transactionDuration())
				err := c.send(sendCtx, response)
				sendCancel()

				if err != nil && ctx.Err() == nil {
					c.reportError(fmt.Errorf("retransmit reliable provisional response: %w", err))
				}

				interval *= 2
				if interval > 4*time.Second {
					interval = 4 * time.Second
				}

				timer.Reset(interval)
			case <-deadline.C:
				call.mu.Lock()
				acknowledged := call.prackReceived
				call.mu.Unlock()

				if !acknowledged {
					c.sendResponse(invite, 504, "Server Time-out", call.LocalTag)
					call.setState(CallEnded)
					c.removeCall(call.ID)
				}

				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (c *Client) AcceptInvite(inviteData types.InviteData) {
	c.ensureSynchronization()

	c.mu.Lock()
	call := c.calls[inviteData.CallID]
	c.mu.Unlock()

	if call == nil {
		c.reportError(fmt.Errorf("call %s was not found", inviteData.CallID))

		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.transactionDuration())
	defer cancel()

	if err := call.Accept(ctx); err != nil {
		c.reportError(err)
	}
}

func (c *Client) SendAudio(payload []byte, source types.AudioData) {
	c.ensureSynchronization()

	c.mu.Lock()

	var selected *Call

	for _, call := range c.calls {
		if call.State() != CallEnded {
			selected = call

			break
		}

	}

	c.mu.Unlock()

	if selected == nil {
		c.reportError(errors.New("no active call available for audio"))

		return
	}

	if source.RemoteAddr != "" {
		selected.mu.Lock()
		rtpSession := selected.media
		selected.mu.Unlock()

		if rtpSession != nil {
			if err := rtpSession.SetRemote(source.RemoteAddr); err != nil {
				c.reportError(err)

				return
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.transactionDuration())
	defer cancel()

	if err := selected.SendAudio(ctx, payload, source.Marker); err != nil {
		c.reportError(err)
	}
}

func (c *Client) startMediaPump(call *Call) {
	call.mediaPump.Do(func() {
		c.runMediaPump(call)
	})
}

func (c *Client) runMediaPump(call *Call) {
	call.mu.Lock()
	rtpSession := call.media
	call.mu.Unlock()

	if rtpSession == nil {
		return
	}

	c.wg.Add(1)

	go func() {
		defer c.wg.Done()

		for {
			select {
			case packet, ok := <-rtpSession.Packets():
				if !ok {
					return
				}

				data := types.AudioData{
					Version:     int(packet.Version),
					PayloadType: packet.PayloadType,
					SequenceNum: packet.SequenceNumber,
					Timestamp:   packet.Timestamp,
					SSRC:        packet.SSRC,
					Payload:     packet.Payload,
					RemoteAddr:  packet.RemoteAddr.String(),
					Marker:      packet.Marker,
				}

				call.firstMedia.Do(func() {
					if c.OnFirstMedia != nil {
						c.OnFirstMedia(c, data)
					}

					if c.OnStartCall != nil {
						c.OnStartCall(c, data)
					}
				})

				if c.OnAudioReceived != nil {
					c.OnAudioReceived(c, data)
				}

			case err, ok := <-rtpSession.Errors():
				if ok && err != nil {
					c.reportError(err)
				}

			case <-c.done():
				return
			}
		}
	}()
}

func (c *Client) addDialogHeaders(request *sip.Message, call *Call, method string) error {
	branch, err := randomToken(12)

	if err != nil {
		return err
	}

	localHost, localPort, err := c.localEndpoint()

	if err != nil {
		return err
	}

	cseq := call.nextCSeq()
	transportName := strings.ToUpper(string(c.Network))

	request.Headers.Add("Via", fmt.Sprintf("SIP/2.0/%s %s;branch=z9hG4bK%s;rport", transportName, hostPort(localHost, localPort), branch))
	request.Headers.Add("Max-Forwards", "70")
	request.Headers.Add("From", ensureTag(call.LocalURI, call.LocalTag))
	request.Headers.Add("To", ensureTag(call.RemoteURI, call.RemoteTag))
	request.Headers.Add("Call-ID", call.ID)
	request.Headers.Add("CSeq", fmt.Sprintf("%d %s", cseq, method))
	request.Headers.Add("Contact", fmt.Sprintf("<%s>", c.contactURI(localHost, localPort)))
	request.Headers.Add("User-Agent", c.UserAgent)

	for _, route := range call.routeSet {
		request.Headers.Add("Route", route)
	}

	return nil
}

func (c *Client) sendResponse(request *sip.Message, statusCode int, reason, localTag string) {
	response := c.responseFor(request, statusCode, reason, localTag)

	c.sendServerResponse(request, response)
}

func (c *Client) sendServerResponse(request, response *sip.Message) {
	c.cacheServerResponse(request, response)

	ctx, cancel := context.WithTimeout(context.Background(), c.transactionDuration())
	defer cancel()

	if err := c.send(ctx, response); err != nil {
		c.reportError(err)
	}
}

func (c *Client) cacheServerResponse(request, response *sip.Message) {
	key, err := request.TransactionKey()

	if err != nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.serverTransactions) >= 512 {
		cutoff := time.Now().Add(-64 * 500 * time.Millisecond)

		for transactionKey, cached := range c.serverTransactions {
			if cached.createdAt.Before(cutoff) {
				delete(c.serverTransactions, transactionKey)
			}
		}
	}

	c.serverTransactions[key] = cachedServerResponse{
		message:   response,
		createdAt: time.Now(),
	}
}

func (c *Client) resendCachedResponse(request *sip.Message) bool {
	key, err := request.TransactionKey()

	if err != nil {
		return false
	}

	c.mu.Lock()
	cached, exists := c.serverTransactions[key]

	if exists && time.Since(cached.createdAt) > 32*time.Second {
		delete(c.serverTransactions, key)
		exists = false
	}

	c.mu.Unlock()

	if !exists {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.transactionDuration())
	defer cancel()

	if err := c.send(ctx, cached.message); err != nil {
		c.reportError(err)
	}

	return true
}

func (c *Client) removeCall(callID string) {
	c.mu.Lock()
	delete(c.calls, callID)
	c.mu.Unlock()
}

func (c *Client) mediaLocalIP() (net.IP, error) {
	host, _, err := c.localEndpoint()

	if err != nil {
		return nil, err
	}

	ip := net.ParseIP(host)

	if ip == nil || ip.IsUnspecified() {
		return nil, fmt.Errorf("local SIP address %q is not usable for SDP; configure AdvertisedAddress", host)
	}

	return ip, nil
}

func firstActiveAudio(description sdp.Description) sdp.Media {
	for _, mediaDescription := range description.Media {
		if mediaDescription.Type == "audio" && mediaDescription.Port != 0 {
			return mediaDescription
		}
	}

	return sdp.Media{}
}

func headerTag(value string) string {
	for _, parameter := range strings.Split(value, ";")[1:] {
		name, parameterValue, found := strings.Cut(strings.TrimSpace(parameter), "=")

		if found && strings.EqualFold(name, "tag") {
			return strings.Trim(parameterValue, "\"")
		}
	}

	return ""
}

func ensureTag(value, tag string) string {
	if tag == "" || headerTag(value) != "" {
		return value
	}

	return value + ";tag=" + tag
}

func topViaBranch(value string) string {
	return sip.ViaBranch(value)
}

func contactURI(value string) string {
	value = strings.TrimSpace(value)

	if start := strings.IndexByte(value, '<'); start >= 0 {
		if end := strings.IndexByte(value[start+1:], '>'); end >= 0 {
			return value[start+1 : start+1+end]
		}
	}

	if end := strings.IndexByte(value, ';'); end >= 0 {
		return strings.TrimSpace(value[:end])
	}

	return value
}
