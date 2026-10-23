package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/jvrs2812/go-sip/media"
	"github.com/jvrs2812/go-sip/sdp"
	"github.com/jvrs2812/go-sip/sip"
	siptransaction "github.com/jvrs2812/go-sip/transaction"
	"github.com/jvrs2812/go-sip/transport"
)

type completedInvite struct {
	request      *sip.Message
	selectedTag  string
	expiresAt    time.Time
	handledForks map[string]bool
}

func (c *Client) Invite(ctx context.Context, target string) (*Call, error) {
	return c.invite(ctx, target, nil)
}

func (c *Client) invite(ctx context.Context, target string, decorate func(*sip.Message) error) (*Call, error) {
	if err := c.Start(ctx); err != nil {
		return nil, err
	}

	target = normalizeSIPURI(target)

	if target == "" {
		return nil, errors.New("INVITE target is required")
	}

	localIP, err := c.mediaLocalIP()

	if err != nil {
		return nil, err
	}

	localRTPAddress, err := c.mediaBindAddress(c.PortForRtp)

	if err != nil {
		return nil, err
	}

	rtpSession, err := media.NewRTPSession(media.RTPConfig{
		LocalAddr:        localRTPAddress,
		PayloadType:      0,
		ClockRate:        8000,
		SamplesPerPacket: 160,
		SymmetricRTP:     true,
		Pacing:           true,
	})

	if err != nil {
		return nil, fmt.Errorf("open RTP listener: %w", err)
	}

	_, localRTPPortText, err := net.SplitHostPort(rtpSession.LocalAddr().String())

	if err != nil {
		_ = rtpSession.Close()

		return nil, err
	}

	localRTPPort, err := strconv.Atoi(localRTPPortText)

	if err != nil {
		_ = rtpSession.Close()

		return nil, err
	}

	origin := uint64(time.Now().UnixNano())
	formats, codecs := offeredAudioCodecs()
	offer := sdp.Description{
		OriginID:      origin,
		OriginVersion: origin,
		SessionName:   "go-sip",
		Connection:    localIP,
		Media: []sdp.Media{{
			Type:       "audio",
			Port:       localRTPPort,
			Protocol:   "RTP/AVP",
			Formats:    formats,
			Connection: localIP,
			Direction:  sdp.SendRecv,
			Codecs:     codecs,
		}},
	}

	offerBody, err := offer.Marshal()

	if err != nil {
		_ = rtpSession.Close()

		return nil, err
	}

	request, localTag, callID, sequence, err := c.buildInitialInvite(target, offerBody)

	if err != nil {
		_ = rtpSession.Close()

		return nil, err
	}

	if decorate != nil {
		if err := decorate(request); err != nil {
			_ = rtpSession.Close()

			return nil, err
		}
	}

	call := &Call{
		client:       c,
		ID:           callID,
		LocalTag:     localTag,
		LocalURI:     request.Headers.Get("From"),
		RemoteURI:    request.Headers.Get("To"),
		RemoteTarget: target,
		state:        CallEstablishing,
		invite:       request,
		inviteCSeq:   sequence,
		localCSeq:    sequence,
		media:        rtpSession,
		mediaDone:    make(chan struct{}),
	}

	completed := false
	defer func() {
		if completed {
			return
		}

		_ = call.closeMedia()
		c.removeCall(call.ID)
	}()

	response, transactionErr := c.roundTripInvite(ctx, request, func(provisional *sip.Message) {
		if err := c.applyEarlyMedia(call, provisional); err != nil {
			c.reportError(err)
		}
	})

	if transactionErr != nil && response == nil {
		_ = rtpSession.Close()

		return nil, transactionErr
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		ackCtx := ctx
		ackCancel := func() {}
		if transactionErr != nil {
			ackCtx, ackCancel = context.WithTimeout(context.Background(), c.transactionDuration())
		}

		ackErr := c.sendInviteACK(ackCtx, request, response, target, sequence)
		ackCancel()
		_ = rtpSession.Close()

		if transactionErr != nil {
			return nil, errors.Join(transactionErr, ackErr)
		}

		if ackErr != nil {
			return nil, ackErr
		}

		return nil, fmt.Errorf("INVITE failed: %d %s", response.StatusCode, response.Reason)
	}

	remoteTarget := contactURI(response.Headers.Get("Contact"))

	if remoteTarget == "" {
		remoteTarget = target
	}

	ackCtx := ctx
	ackCancel := func() {}
	if transactionErr != nil {
		ackCtx, ackCancel = context.WithTimeout(context.Background(), c.transactionDuration())
	}

	if err := c.sendInviteACK(ackCtx, request, response, remoteTarget, sequence); err != nil {
		ackCancel()
		_ = rtpSession.Close()

		return nil, errors.Join(transactionErr, err)
	}
	ackCancel()

	answer, err := sdp.Parse(response.Body)

	if err != nil {
		_ = rtpSession.Close()

		return nil, fmt.Errorf("parse SDP answer: %w", err)
	}

	remoteMedia := firstActiveAudio(answer)

	if remoteMedia.Connection == nil || remoteMedia.Port == 0 {
		_ = rtpSession.Close()

		return nil, errors.New("SDP answer has no active audio destination")
	}

	selected, err := selectAnswerCodec(remoteMedia)

	if err != nil {
		_ = rtpSession.Close()

		return nil, err
	}

	remoteRTPAddress := net.JoinHostPort(remoteMedia.Connection.String(), strconv.Itoa(remoteMedia.Port))

	if err := rtpSession.SetRemote(remoteRTPAddress); err != nil {
		_ = rtpSession.Close()

		return nil, err
	}

	rtpSession.ConfigurePayload(selected.PayloadType, uint32(selected.ClockRate), samplesPerPacket(selected))

	call.mu.Lock()
	call.RemoteTag = headerTag(response.Headers.Get("To"))
	call.LocalURI = response.Headers.Get("From")
	call.RemoteURI = response.Headers.Get("To")
	call.RemoteTarget = remoteTarget
	call.codec = selected
	call.routeSet = reverseStrings(response.Headers.Values("Record-Route"))
	call.mu.Unlock()

	call.dtmfPayload, call.hasDTMF = sdp.TelephoneEventPayload(answer)

	call.setState(CallActive)

	c.mu.Lock()
	c.calls[callID] = call
	c.mu.Unlock()

	c.startMediaPump(call)
	call.notifyEstablished()

	if transactionErr != nil {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), c.transactionDuration())
		hangupErr := call.Hangup(cleanupCtx)
		cleanupCancel()

		return nil, errors.Join(transactionErr, hangupErr)
	}

	completed = true

	return call, nil
}

func (c *Client) applyEarlyMedia(call *Call, response *sip.Message) error {
	if response.StatusCode < 180 || response.StatusCode >= 200 || len(response.Body) == 0 {
		return nil
	}

	contentType := strings.ToLower(response.Headers.Get("Content-Type"))
	if contentType != "" && !strings.HasPrefix(contentType, "application/sdp") {
		return nil
	}

	answer, err := sdp.Parse(response.Body)
	if err != nil {
		return fmt.Errorf("parse early-media SDP: %w", err)
	}

	remoteMedia := firstActiveAudio(answer)
	if remoteMedia.Connection == nil || remoteMedia.Port == 0 {
		return errors.New("early-media SDP has no active audio destination")
	}

	selected, err := selectAnswerCodec(remoteMedia)
	if err != nil {
		return fmt.Errorf("select early-media codec: %w", err)
	}

	remoteAddress := net.JoinHostPort(remoteMedia.Connection.String(), strconv.Itoa(remoteMedia.Port))
	if err := call.media.SetRemote(remoteAddress); err != nil {
		return fmt.Errorf("set early-media destination: %w", err)
	}

	call.media.ConfigurePayload(selected.PayloadType, uint32(selected.ClockRate), samplesPerPacket(selected))
	dtmfPayload, hasDTMF := sdp.TelephoneEventPayload(answer)
	remoteTarget := contactURI(response.Headers.Get("Contact"))
	if remoteTarget == "" {
		remoteTarget = call.RemoteTarget
	}

	call.mu.Lock()
	call.RemoteTag = headerTag(response.Headers.Get("To"))
	call.LocalURI = response.Headers.Get("From")
	call.RemoteURI = response.Headers.Get("To")
	call.RemoteTarget = remoteTarget
	call.codec = selected
	call.dtmfPayload = dtmfPayload
	call.hasDTMF = hasDTMF
	call.routeSet = reverseStrings(response.Headers.Values("Record-Route"))
	call.state = CallEarlyMedia
	call.mu.Unlock()

	c.mu.Lock()
	c.calls[call.ID] = call
	c.mu.Unlock()

	c.startMediaPump(call)

	if c.OnEarlyMedia != nil {
		c.OnEarlyMedia(c, call, response)
	}

	return nil
}

func (c *Client) roundTripInvite(ctx context.Context, request *sip.Message, onProvisional func(*sip.Message)) (*sip.Message, error) {
	transaction := siptransaction.NewClient("INVITE", c.Network == transport.UDP)
	defer transaction.Terminate()

	key, err := request.TransactionKey()
	if err != nil {
		return nil, err
	}

	waiter := make(chan *sip.Message, 16)
	c.mu.Lock()
	if _, exists := c.waiters[key]; exists {
		c.mu.Unlock()

		return nil, fmt.Errorf("transaction %s is already active", key)
	}

	connection := c.conn
	if connection == nil {
		c.mu.Unlock()

		return nil, errors.New("SIP transport is not connected")
	}

	c.waiters[key] = waiter
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.waiters, key)
		c.mu.Unlock()
	}()

	if err := connection.Send(ctx, request); err != nil {
		return nil, err
	}

	transactionCtx, transactionCancel := context.WithTimeout(context.Background(), c.transactionDuration())
	defer transactionCancel()

	var retransmitTimer *time.Timer
	var retransmit <-chan time.Time
	if retransmitInterval, ok := transaction.RetransmissionInterval(); ok {
		retransmitTimer = time.NewTimer(retransmitInterval)
		retransmit = retransmitTimer.C

		defer retransmitTimer.Stop()
	}

	callerDone := ctx.Done()
	var cancellationErr error
	provisionalReceived := false
	cancelSent := false
	var lastReliableSequence string

	for {
		select {
		case response := <-waiter:
			if response == nil {
				return nil, net.ErrClosed
			}

			if response.StatusCode >= 200 {
				transaction.Receive(response.StatusCode)
				completed := c.rememberCompletedInvite(key, request, response)
				for {
					select {
					case queued := <-waiter:
						if queued != nil && queued.StatusCode >= 200 {
							go c.handleCompletedInviteResponse(completed, queued)
						}
					default:
						return response, cancellationErr
					}
				}
			}

			provisionalReceived = true
			transaction.Receive(response.StatusCode)
			stopRetransmission(retransmitTimer, &retransmit)

			if c.OnProvisionalResponse != nil {
				c.OnProvisionalResponse(c, response)
			}

			if onProvisional != nil {
				onProvisional(response)
			}

			if headerHasToken(response.Headers.Get("Require"), "100rel") {
				reliableSequence := strings.TrimSpace(response.Headers.Get("RSeq"))
				if reliableSequence != "" && reliableSequence != lastReliableSequence {
					if err := c.acknowledgeReliableProvisional(transactionCtx, request, response); err != nil {
						return nil, err
					}

					lastReliableSequence = reliableSequence
				}
			}

			if cancellationErr != nil && !cancelSent {
				cancelSent = true
				c.sendInviteCancellation(request)
			}

		case <-callerDone:
			cancellationErr = ctx.Err()
			callerDone = nil

			if provisionalReceived && !cancelSent {
				cancelSent = true
				c.sendInviteCancellation(request)
			}

		case <-retransmit:
			if err := connection.Send(transactionCtx, request); err != nil {
				return nil, errors.Join(cancellationErr, err)
			}

			transaction.Retransmitted()
			if retransmitInterval, ok := transaction.RetransmissionInterval(); ok {
				retransmitTimer.Reset(retransmitInterval)
			} else {
				retransmit = nil
			}

		case <-transactionCtx.Done():
			return nil, errors.Join(cancellationErr, transactionCtx.Err())

		case <-c.done():
			return nil, errors.Join(cancellationErr, net.ErrClosed)
		}
	}
}

func (c *Client) rememberCompletedInvite(key string, request, response *sip.Message) *completedInvite {
	completed := &completedInvite{
		request:      request,
		selectedTag:  headerTag(response.Headers.Get("To")),
		expiresAt:    time.Now().Add(32 * time.Second),
		handledForks: make(map[string]bool),
	}

	c.mu.Lock()
	for transactionKey, existing := range c.completedInvites {
		if time.Now().After(existing.expiresAt) {
			delete(c.completedInvites, transactionKey)
		}
	}

	c.completedInvites[key] = completed
	c.mu.Unlock()

	return completed
}

func (c *Client) handleCompletedInviteResponse(completed *completedInvite, response *sip.Message) {
	if completed == nil || completed.request == nil {
		return
	}

	sequence, _, err := completed.request.CSeq()
	if err != nil {
		c.reportError(err)

		return
	}

	target := completed.request.RequestURI
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		if contact := contactURI(response.Headers.Get("Contact")); contact != "" {
			target = contact
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.transactionDuration())
	defer cancel()

	if err := c.sendInviteACK(ctx, completed.request, response, target, sequence); err != nil {
		c.reportError(fmt.Errorf("ACK repeated INVITE response: %w", err))

		return
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return
	}

	remoteTag := headerTag(response.Headers.Get("To"))
	if remoteTag == "" || remoteTag == completed.selectedTag {
		return
	}

	c.mu.Lock()
	alreadyHandled := completed.handledForks[remoteTag]
	if !alreadyHandled {
		completed.handledForks[remoteTag] = true
	}
	c.mu.Unlock()

	if alreadyHandled {
		return
	}

	fork := &Call{
		client:       c,
		ID:           completed.request.Headers.Get("Call-ID"),
		LocalTag:     headerTag(completed.request.Headers.Get("From")),
		RemoteTag:    remoteTag,
		LocalURI:     completed.request.Headers.Get("From"),
		RemoteURI:    response.Headers.Get("To"),
		RemoteTarget: target,
		state:        CallActive,
		localCSeq:    sequence,
		routeSet:     reverseStrings(response.Headers.Values("Record-Route")),
	}

	bye := sip.NewRequest("BYE", target)
	if err := c.addDialogHeaders(bye, fork, "BYE"); err != nil {
		c.reportError(fmt.Errorf("build BYE for unselected fork: %w", err))

		return
	}

	byeResponse, err := c.roundTrip(ctx, bye)
	if err != nil {
		c.reportError(fmt.Errorf("terminate unselected fork: %w", err))

		return
	}

	if byeResponse.StatusCode < 200 || byeResponse.StatusCode >= 300 {
		c.reportError(fmt.Errorf("terminate unselected fork: %d %s", byeResponse.StatusCode, byeResponse.Reason))
	}
}

func (c *Client) sendInviteCancellation(invite *sip.Message) {
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), c.transactionDuration())
	defer cleanupCancel()

	cancelRequest := sip.NewRequest("CANCEL", invite.RequestURI)
	for _, via := range invite.Headers.Values("Via") {
		cancelRequest.Headers.Add("Via", via)
	}

	cancelRequest.Headers.Add("Max-Forwards", invite.Headers.Get("Max-Forwards"))
	cancelRequest.Headers.Add("From", invite.Headers.Get("From"))
	cancelRequest.Headers.Add("To", invite.Headers.Get("To"))
	cancelRequest.Headers.Add("Call-ID", invite.Headers.Get("Call-ID"))

	sequence, _, err := invite.CSeq()
	if err != nil {
		c.reportError(err)

		return
	}

	cancelRequest.Headers.Add("CSeq", fmt.Sprintf("%d CANCEL", sequence))
	for _, route := range invite.Headers.Values("Route") {
		cancelRequest.Headers.Add("Route", route)
	}

	response, err := c.roundTrip(cleanupCtx, cancelRequest)
	if err != nil {
		c.reportError(fmt.Errorf("CANCEL transaction: %w", err))

		return
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		c.reportError(fmt.Errorf("CANCEL failed: %d %s", response.StatusCode, response.Reason))
	}
}

func stopRetransmission(timer *time.Timer, channel *<-chan time.Time) {
	if timer == nil {
		return
	}

	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}

	*channel = nil
}

func (c *Client) buildInitialInvite(target string, body []byte) (*sip.Message, string, string, uint32, error) {
	branch, err := randomToken(12)

	if err != nil {
		return nil, "", "", 0, err
	}

	localTag, err := randomToken(10)

	if err != nil {
		return nil, "", "", 0, err
	}

	callID, err := randomToken(16)

	if err != nil {
		return nil, "", "", 0, err
	}

	localHost, localPort, err := c.localEndpoint()

	if err != nil {
		return nil, "", "", 0, err
	}

	sequence := c.nextCSeq()
	transportName := strings.ToUpper(string(c.Network))
	request := sip.NewRequest("INVITE", target)

	request.Headers.Add("Via", fmt.Sprintf("SIP/2.0/%s %s;branch=z9hG4bK%s;rport", transportName, hostPort(localHost, localPort), branch))
	request.Headers.Add("Max-Forwards", "70")
	request.Headers.Add("From", fmt.Sprintf("<%s>;tag=%s", c.addressOfRecord(), localTag))
	request.Headers.Add("To", fmt.Sprintf("<%s>", target))
	request.Headers.Add("Call-ID", callID)
	request.Headers.Add("CSeq", fmt.Sprintf("%d INVITE", sequence))
	request.Headers.Add("Contact", fmt.Sprintf("<%s>", c.contactURI(localHost, localPort)))
	request.Headers.Add("Content-Type", "application/sdp")
	request.Headers.Add("Allow", "INVITE, ACK, BYE, CANCEL, OPTIONS, INFO, REFER, NOTIFY, PRACK")
	request.Headers.Add("Supported", "100rel, replaces, timer")
	request.Headers.Add("User-Agent", c.UserAgent)
	request.Body = body

	return request, localTag, callID, sequence, nil
}

func (c *Client) sendInviteACK(ctx context.Context, invite, response *sip.Message, target string, sequence uint32) error {
	localHost, localPort, err := c.localEndpoint()

	if err != nil {
		return err
	}

	transportName := strings.ToUpper(string(c.Network))
	request := sip.NewRequest("ACK", target)

	if response.StatusCode >= 300 {
		for _, via := range invite.Headers.Values("Via") {
			request.Headers.Add("Via", via)
		}
	} else {
		branch, branchErr := randomToken(12)
		if branchErr != nil {
			return branchErr
		}

		request.Headers.Add("Via", fmt.Sprintf("SIP/2.0/%s %s;branch=z9hG4bK%s;rport", transportName, hostPort(localHost, localPort), branch))
	}

	request.Headers.Add("Max-Forwards", "70")
	request.Headers.Add("From", invite.Headers.Get("From"))
	request.Headers.Add("To", response.Headers.Get("To"))
	request.Headers.Add("Call-ID", invite.Headers.Get("Call-ID"))
	request.Headers.Add("CSeq", fmt.Sprintf("%d ACK", sequence))
	request.Headers.Add("Contact", invite.Headers.Get("Contact"))
	request.Headers.Add("User-Agent", c.UserAgent)

	if response.StatusCode >= 300 {
		for _, route := range invite.Headers.Values("Route") {
			request.Headers.Add("Route", route)
		}
	} else {
		for _, route := range reverseStrings(response.Headers.Values("Record-Route")) {
			request.Headers.Add("Route", route)
		}
	}

	return c.send(ctx, request)
}

func (c *Client) acknowledgeReliableProvisional(ctx context.Context, invite, response *sip.Message) error {
	rseq := strings.TrimSpace(response.Headers.Get("RSeq"))
	inviteSequence, method, err := invite.CSeq()

	if err != nil || method != "INVITE" {
		return errors.New("reliable provisional response has invalid INVITE CSeq")
	}

	target := contactURI(response.Headers.Get("Contact"))

	if target == "" {
		target = invite.RequestURI
	}

	branch, err := randomToken(12)

	if err != nil {
		return err
	}

	localHost, localPort, err := c.localEndpoint()

	if err != nil {
		return err
	}

	prackSequence := c.nextCSeq()
	request := sip.NewRequest("PRACK", target)
	request.Headers.Add("Via", fmt.Sprintf("SIP/2.0/%s %s;branch=z9hG4bK%s;rport", strings.ToUpper(string(c.Network)), hostPort(localHost, localPort), branch))
	request.Headers.Add("Max-Forwards", "70")
	request.Headers.Add("From", invite.Headers.Get("From"))
	request.Headers.Add("To", response.Headers.Get("To"))
	request.Headers.Add("Call-ID", invite.Headers.Get("Call-ID"))
	request.Headers.Add("CSeq", fmt.Sprintf("%d PRACK", prackSequence))
	request.Headers.Add("RAck", fmt.Sprintf("%s %d INVITE", rseq, inviteSequence))
	request.Headers.Add("User-Agent", c.UserAgent)

	for _, route := range reverseStrings(response.Headers.Values("Record-Route")) {
		request.Headers.Add("Route", route)
	}

	prackResponse, err := c.roundTrip(ctx, request)

	if err != nil {
		return fmt.Errorf("PRACK transaction: %w", err)
	}

	if prackResponse.StatusCode < 200 || prackResponse.StatusCode >= 300 {
		return fmt.Errorf("PRACK failed: %d %s", prackResponse.StatusCode, prackResponse.Reason)
	}

	return nil
}

func selectAnswerCodec(mediaDescription sdp.Media) (sdp.Codec, error) {
	for _, payloadType := range mediaDescription.Formats {
		codec := mediaDescription.Codecs[payloadType]

		if strings.EqualFold(codec.Name, "G722") && codec.ClockRate == 8000 {
			return codec, nil
		}

		if strings.EqualFold(codec.Name, "PCMU") && codec.ClockRate == 8000 {
			return codec, nil
		}

		if strings.EqualFold(codec.Name, "PCMA") && codec.ClockRate == 8000 {
			return codec, nil
		}

		if strings.EqualFold(codec.Name, "iLBC") && codec.ClockRate == 8000 {
			return codec, nil
		}

		if media.GSMAvailable() && strings.EqualFold(codec.Name, "GSM") && codec.ClockRate == 8000 {
			return codec, nil
		}

		if media.OpusAvailable() && strings.EqualFold(codec.Name, "opus") && codec.ClockRate == 48000 && (codec.Channels == 1 || codec.Channels == 2) {
			return codec, nil
		}

		if media.SILKAvailable() && strings.EqualFold(codec.Name, "SILK") && validSILKClock(codec.ClockRate) {
			return codec, nil
		}
	}

	return sdp.Codec{}, errors.New("SDP answer selected an unsupported codec")
}

func samplesPerPacket(codec sdp.Codec) uint32 {
	if strings.EqualFold(codec.Name, "iLBC") && strings.Contains(strings.ToLower(codec.FMTP), "mode=30") {
		return 240
	}

	if strings.EqualFold(codec.Name, "opus") {
		return 960
	}

	return uint32(codec.ClockRate / 50)
}

func supportedAudioCodecs() []sdp.Codec {
	codecs := []sdp.Codec{
		{Name: "G722", ClockRate: 8000, Channels: 1},
		{Name: "PCMU", ClockRate: 8000, Channels: 1},
		{Name: "PCMA", ClockRate: 8000, Channels: 1},
		{Name: "iLBC", ClockRate: 8000, Channels: 1, FMTP: "mode=20"},
		{Name: "iLBC", ClockRate: 8000, Channels: 1, FMTP: "mode=30"},
	}

	if media.GSMAvailable() {
		codecs = append(codecs, sdp.Codec{Name: "GSM", ClockRate: 8000, Channels: 1})
	}

	if media.OpusAvailable() {
		codecs = append([]sdp.Codec{{Name: "opus", ClockRate: 48000, Channels: 2, FMTP: "minptime=10;useinbandfec=1"}}, codecs...)
	}

	if media.SILKAvailable() {
		codecs = append([]sdp.Codec{{Name: "SILK", ClockRate: 16000, Channels: 1}}, codecs...)
	}

	return codecs
}

func offeredAudioCodecs() ([]uint8, map[uint8]sdp.Codec) {
	formats := []uint8{9, 0, 8, 102, 103}
	codecs := map[uint8]sdp.Codec{
		9:   {PayloadType: 9, Name: "G722", ClockRate: 8000, Channels: 1},
		0:   {PayloadType: 0, Name: "PCMU", ClockRate: 8000, Channels: 1},
		8:   {PayloadType: 8, Name: "PCMA", ClockRate: 8000, Channels: 1},
		102: {PayloadType: 102, Name: "iLBC", ClockRate: 8000, Channels: 1, FMTP: "mode=20"},
		103: {PayloadType: 103, Name: "iLBC", ClockRate: 8000, Channels: 1, FMTP: "mode=30"},
	}

	if media.GSMAvailable() {
		formats = append(formats, 3)
		codecs[3] = sdp.Codec{PayloadType: 3, Name: "GSM", ClockRate: 8000, Channels: 1}
	}

	if media.OpusAvailable() {
		formats = append([]uint8{media.DefaultOpusPayloadType}, formats...)
		codecs[media.DefaultOpusPayloadType] = sdp.Codec{
			PayloadType: media.DefaultOpusPayloadType,
			Name:        "opus",
			ClockRate:   48000,
			Channels:    2,
			FMTP:        "minptime=10;useinbandfec=1",
		}
	}

	if media.SILKAvailable() {
		formats = append([]uint8{media.DefaultSILKPayloadType}, formats...)
		codecs[media.DefaultSILKPayloadType] = sdp.Codec{
			PayloadType: media.DefaultSILKPayloadType,
			Name:        "SILK",
			ClockRate:   16000,
			Channels:    1,
		}
	}

	formats = append(formats, 101)
	codecs[101] = sdp.Codec{PayloadType: 101, Name: "telephone-event", ClockRate: 8000, Channels: 1, FMTP: "0-16"}

	return formats, codecs
}

func validSILKClock(clockRate int) bool {
	return clockRate == 8000 || clockRate == 12000 || clockRate == 16000 || clockRate == 24000
}

func normalizeSIPURI(value string) string {
	value = strings.TrimSpace(value)

	if value == "" {
		return ""
	}

	lowerValue := strings.ToLower(value)
	if strings.HasPrefix(lowerValue, "sip:") || strings.HasPrefix(lowerValue, "sips:") || strings.HasPrefix(lowerValue, "urn:") {
		return value
	}

	return "sip:" + value
}

func reverseStrings(values []string) []string {
	reversed := make([]string, len(values))

	for index := range values {
		reversed[len(values)-1-index] = values[index]
	}

	return reversed
}
