package client

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jvrs2812/go-sip/sip"
)

type MessageEvent struct {
	From        string
	To          string
	ContentType string
	Body        []byte
	Request     *sip.Message
}

type InfoEvent struct {
	CallID      string
	ContentType string
	Body        []byte
	Request     *sip.Message
}

type NotifyEvent struct {
	Event             string
	SubscriptionState string
	ContentType       string
	Body              []byte
	Request           *sip.Message
}

type SubscribeEvent struct {
	Event   string
	Expires time.Duration
	Request *sip.Message
}

type PublishEvent struct {
	Event       string
	EntityTag   string
	ContentType string
	Body        []byte
	Request     *sip.Message
}

type serverPublication struct {
	entityTag string
	timer     *time.Timer
}

type ReferEvent struct {
	ReferTo    string
	ReferredBy string
	Request    *sip.Message
}

func (c *Client) Options(ctx context.Context, target string) (*sip.Message, error) {
	request, err := c.newOutOfDialogRequest(ctx, "OPTIONS", target)

	if err != nil {
		return nil, err
	}

	request.Headers.Add("Accept", "application/sdp, application/pidf+xml, text/plain")

	return c.successfulResponse(ctx, request)
}

func (c *Client) SendMessage(ctx context.Context, target, contentType string, body []byte) error {
	if contentType == "" {
		contentType = "text/plain; charset=utf-8"
	}

	limit := c.messageBodyLimit()
	if len(body) > limit {
		return fmt.Errorf("MESSAGE body exceeds %d bytes", limit)
	}

	if _, _, err := mime.ParseMediaType(contentType); err != nil {
		return fmt.Errorf("invalid MESSAGE content type: %w", err)
	}

	request, err := c.newOutOfDialogRequest(ctx, "MESSAGE", target)

	if err != nil {
		return err
	}

	request.Headers.Add("Content-Type", contentType)
	request.Body = append([]byte(nil), body...)

	_, err = c.successfulResponse(ctx, request)

	return err
}

func (call *Call) Info(ctx context.Context, contentType string, body []byte) error {
	if contentType != "" {
		if _, _, err := mime.ParseMediaType(contentType); err != nil {
			return fmt.Errorf("invalid INFO content type: %w", err)
		}
	}

	request := sip.NewRequest("INFO", call.RemoteTarget)

	if err := call.client.addDialogHeaders(request, call, "INFO"); err != nil {
		return err
	}

	if contentType != "" {
		request.Headers.Add("Content-Type", contentType)
	}

	request.Body = append([]byte(nil), body...)
	_, err := call.client.successfulResponse(ctx, request)

	return err
}

func (call *Call) SendDTMFInfo(ctx context.Context, digit rune, duration time.Duration) error {
	if !validDTMFDigit(digit) {
		return fmt.Errorf("invalid DTMF digit %q", digit)
	}

	durationMilliseconds := duration.Milliseconds()
	if durationMilliseconds < 70 || durationMilliseconds > 6000 {
		return errors.New("DTMF duration must be between 70ms and 6s")
	}

	body := fmt.Appendf(nil, "Signal=%c\r\nDuration=%d\r\n", digit, durationMilliseconds)

	return call.Info(ctx, "application/dtmf-relay", body)
}

func (call *Call) Refer(ctx context.Context, referTo string) error {
	_, err := call.Transfer(ctx, referTo)

	return err
}

func (call *Call) Transfer(ctx context.Context, referTo string) (*Transfer, error) {
	referTo = normalizeSIPURI(referTo)

	if referTo == "" {
		return nil, errors.New("REFER target is required")
	}

	request := sip.NewRequest("REFER", call.RemoteTarget)

	if err := call.client.addDialogHeaders(request, call, "REFER"); err != nil {
		return nil, err
	}

	request.Headers.Add("Refer-To", "<"+referTo+">")
	request.Headers.Add("Referred-By", ensureTag(call.LocalURI, call.LocalTag))
	request.Headers.Add("Event", "refer")

	transfer := newTransfer(call, referTo)
	transfer.startTimer(5 * time.Minute)

	call.client.mu.Lock()
	if _, exists := call.client.transfers[call.ID]; exists {
		call.client.mu.Unlock()
		transfer.stopTimer()

		return nil, errors.New("a transfer is already active for this dialog")
	}

	call.client.transfers[call.ID] = transfer
	call.client.mu.Unlock()

	response, err := call.client.successfulResponse(ctx, request)
	if err != nil {
		call.client.removeTransfer(call.ID, transfer)
		transfer.stopTimer()

		return nil, err
	}

	if response.StatusCode != 202 && response.StatusCode != 200 {
		call.client.removeTransfer(call.ID, transfer)
		transfer.stopTimer()

		return nil, fmt.Errorf("REFER failed: %d %s", response.StatusCode, response.Reason)
	}

	return transfer, nil
}

func (call *Call) AttendedTransfer(ctx context.Context, replaces *Call) (*Transfer, error) {
	if replaces == nil {
		return nil, errors.New("replaced call is required")
	}

	replaces.mu.Lock()
	replacesValue := fmt.Sprintf("%s;to-tag=%s;from-tag=%s", replaces.ID, replaces.RemoteTag, replaces.LocalTag)
	target := replaces.RemoteTarget
	replaces.mu.Unlock()

	separator := "?"
	if strings.Contains(target, "?") {
		separator = "&"
	}

	target += separator + "Replaces=" + url.QueryEscape(replacesValue)

	return call.Transfer(ctx, target)
}

func (c *Client) Subscribe(ctx context.Context, target, event string, expires time.Duration) (*sip.Message, error) {
	if strings.TrimSpace(event) == "" {
		return nil, errors.New("SUBSCRIBE event is required")
	}

	request, err := c.newOutOfDialogRequest(ctx, "SUBSCRIBE", target)

	if err != nil {
		return nil, err
	}

	request.Headers.Add("Event", event)
	request.Headers.Add("Expires", strconv.Itoa(max(int(expires/time.Second), 0)))

	return c.successfulResponse(ctx, request)
}

func (c *Client) Publish(ctx context.Context, target, event, entityTag, contentType string, body []byte) (*sip.Message, error) {
	if strings.TrimSpace(event) == "" {
		return nil, errors.New("PUBLISH event is required")
	}

	request, err := c.newOutOfDialogRequest(ctx, "PUBLISH", target)

	if err != nil {
		return nil, err
	}

	request.Headers.Add("Event", event)

	if entityTag != "" {
		request.Headers.Add("SIP-If-Match", entityTag)
	}

	if contentType != "" {
		request.Headers.Add("Content-Type", contentType)
	}

	request.Body = append([]byte(nil), body...)

	return c.successfulResponse(ctx, request)
}

func (c *Client) newOutOfDialogRequest(ctx context.Context, method, target string) (*sip.Message, error) {
	if err := c.Start(ctx); err != nil {
		return nil, err
	}

	target = normalizeSIPURI(target)

	if target == "" {
		return nil, fmt.Errorf("%s target is required", method)
	}

	branch, err := randomToken(12)

	if err != nil {
		return nil, err
	}

	localTag, err := randomToken(10)

	if err != nil {
		return nil, err
	}

	callID, err := randomToken(16)

	if err != nil {
		return nil, err
	}

	localHost, localPort, err := c.localEndpoint()

	if err != nil {
		return nil, err
	}

	sequence := c.nextCSeq()
	request := sip.NewRequest(method, target)
	request.Headers.Add("Via", fmt.Sprintf("SIP/2.0/%s %s;branch=z9hG4bK%s;rport", strings.ToUpper(string(c.Network)), hostPort(localHost, localPort), branch))
	request.Headers.Add("Max-Forwards", "70")
	request.Headers.Add("From", fmt.Sprintf("<%s>;tag=%s", c.addressOfRecord(), localTag))
	request.Headers.Add("To", fmt.Sprintf("<%s>", target))
	request.Headers.Add("Call-ID", callID)
	request.Headers.Add("CSeq", fmt.Sprintf("%d %s", sequence, method))
	request.Headers.Add("Contact", fmt.Sprintf("<%s>", c.contactURI(localHost, localPort)))
	request.Headers.Add("User-Agent", c.UserAgent)

	return request, nil
}

func (c *Client) successfulResponse(ctx context.Context, request *sip.Message) (*sip.Message, error) {
	response, err := c.roundTrip(ctx, request)

	if err != nil {
		return nil, err
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response, fmt.Errorf("%s failed: %d %s", request.Method, response.StatusCode, response.Reason)
	}

	return response, nil
}

func (c *Client) handleMESSAGE(request *sip.Message) {
	if len(request.Body) > c.MaxMessageBodyBytes {
		c.sendResponse(request, 413, "Content Too Large", headerTag(request.Headers.Get("To")))

		return
	}

	contentType := request.Headers.Get("Content-Type")
	if len(request.Body) > 0 && !mediaTypeAllowed(contentType, c.AcceptedMessageTypes) {
		response := c.responseFor(request, 415, "Unsupported Media Type", headerTag(request.Headers.Get("To")))
		response.Headers.Add("Accept", strings.Join(c.AcceptedMessageTypes, ", "))
		c.sendServerResponse(request, response)

		return
	}

	if c.OnInstantMessage == nil {
		c.sendResponse(request, 501, "Not Implemented", headerTag(request.Headers.Get("To")))

		return
	}

	c.sendResponse(request, 200, "OK", headerTag(request.Headers.Get("To")))

	event := MessageEvent{
		From:        request.Headers.Get("From"),
		To:          request.Headers.Get("To"),
		ContentType: contentType,
		Body:        append([]byte(nil), request.Body...),
		Request:     request,
	}

	go c.OnInstantMessage(c, event)
}

func (c *Client) handleINFO(request *sip.Message) {
	callID := strings.TrimSpace(request.Headers.Get("Call-ID"))
	c.mu.Lock()
	call := c.calls[callID]
	c.mu.Unlock()

	if call == nil || !call.matchesInboundRequest(request) {
		c.sendResponse(request, 481, "Call/Transaction Does Not Exist", "")

		return
	}

	if c.OnInfo == nil {
		c.sendResponse(request, 415, "Unsupported Media Type", headerTag(request.Headers.Get("To")))

		return
	}

	contentType := request.Headers.Get("Content-Type")
	if len(c.AcceptedInfoTypes) > 0 && !mediaTypeAllowed(contentType, c.AcceptedInfoTypes) {
		response := c.responseFor(request, 415, "Unsupported Media Type", headerTag(request.Headers.Get("To")))
		response.Headers.Add("Accept", strings.Join(c.AcceptedInfoTypes, ", "))
		c.sendServerResponse(request, response)

		return
	}

	c.sendResponse(request, 200, "OK", headerTag(request.Headers.Get("To")))

	event := InfoEvent{
		CallID:      request.Headers.Get("Call-ID"),
		ContentType: contentType,
		Body:        append([]byte(nil), request.Body...),
		Request:     request,
	}

	go c.OnInfo(c, event)
}

func (c *Client) handleNOTIFY(request *sip.Message) {
	eventPackage := eventToken(request.Headers.Get("Event"))
	if eventPackage == "" {
		c.sendResponse(request, 489, "Bad Event", headerTag(request.Headers.Get("To")))

		return
	}

	status, reason, expires, err := parseSubscriptionState(request.Headers.Get("Subscription-State"))
	if err != nil {
		c.sendResponse(request, 400, "Bad Request", headerTag(request.Headers.Get("To")))

		return
	}

	callID := strings.TrimSpace(request.Headers.Get("Call-ID"))
	c.mu.Lock()
	subscription := c.subscriptions[callID]
	transfer := c.transfers[callID]
	c.mu.Unlock()

	if transfer != nil && eventPackage == "refer" {
		if !transfer.matchesNotify(request) {
			c.sendResponse(request, 481, "Subscription Does Not Exist", headerTag(request.Headers.Get("To")))

			return
		}

		if !mediaTypeAllowed(request.Headers.Get("Content-Type"), []string{"message/sipfrag"}) {
			c.sendResponse(request, 415, "Unsupported Media Type", headerTag(request.Headers.Get("To")))

			return
		}

		responseCode, parseErr := sipfragStatus(request.Body)
		if parseErr != nil {
			c.sendResponse(request, 400, "Bad Request", headerTag(request.Headers.Get("To")))

			return
		}

		event := NotifyEvent{
			Event:             request.Headers.Get("Event"),
			SubscriptionState: request.Headers.Get("Subscription-State"),
			ContentType:       request.Headers.Get("Content-Type"),
			Body:              append([]byte(nil), request.Body...),
			Request:           request,
		}

		c.sendResponse(request, 200, "OK", headerTag(request.Headers.Get("To")))
		transfer.applyNotify(event, status, reason, responseCode)

		if c.OnNotify != nil {
			go c.OnNotify(c, event)
		}

		return
	}

	if subscription == nil {
		c.sendResponse(request, 481, "Subscription Does Not Exist", headerTag(request.Headers.Get("To")))

		return
	}

	if eventPackage != subscription.Event() {
		c.sendResponse(request, 489, "Bad Event", headerTag(request.Headers.Get("To")))

		return
	}

	if !subscription.matchesNotify(request, eventPackage) {
		c.sendResponse(request, 481, "Subscription Does Not Exist", headerTag(request.Headers.Get("To")))

		return
	}

	event := NotifyEvent{
		Event:             request.Headers.Get("Event"),
		SubscriptionState: request.Headers.Get("Subscription-State"),
		ContentType:       request.Headers.Get("Content-Type"),
		Body:              append([]byte(nil), request.Body...),
		Request:           request,
	}

	c.sendResponse(request, 200, "OK", headerTag(request.Headers.Get("To")))
	subscription.applyNotify(event, status, reason, expires)

	if c.OnNotify != nil {
		go c.OnNotify(c, event)
	}
}

func (c *Client) handleSUBSCRIBE(request *sip.Message) {
	if strings.TrimSpace(request.Headers.Get("Event")) == "" {
		c.sendResponse(request, 489, "Bad Event", headerTag(request.Headers.Get("To")))

		return
	}

	c.sendResponse(request, 202, "Accepted", headerTag(request.Headers.Get("To")))

	if c.OnSubscribe != nil {
		seconds, _ := strconv.Atoi(request.Headers.Get("Expires"))
		event := SubscribeEvent{
			Event:   request.Headers.Get("Event"),
			Expires: time.Duration(max(seconds, 0)) * time.Second,
			Request: request,
		}

		go c.OnSubscribe(c, event)
	}
}

func (c *Client) handlePUBLISH(request *sip.Message) {
	eventPackage := eventToken(request.Headers.Get("Event"))
	if eventPackage == "" {
		c.sendResponse(request, 489, "Bad Event", headerTag(request.Headers.Get("To")))

		return
	}

	expiresSeconds := 3600
	if value := strings.TrimSpace(request.Headers.Get("Expires")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			c.sendResponse(request, 400, "Bad Request", headerTag(request.Headers.Get("To")))

			return
		}

		expiresSeconds = parsed
	}

	key := request.RequestURI + "|" + eventPackage
	ifMatch := strings.TrimSpace(request.Headers.Get("SIP-If-Match"))

	c.mu.Lock()
	current, exists := c.serverPublications[key]
	if ifMatch != "" && (!exists || current.entityTag != ifMatch) {
		c.mu.Unlock()
		c.sendResponse(request, 412, "Conditional Request Failed", headerTag(request.Headers.Get("To")))

		return
	}

	if ifMatch == "" && (expiresSeconds == 0 || len(request.Body) == 0) {
		c.mu.Unlock()
		c.sendResponse(request, 400, "Bad Request", headerTag(request.Headers.Get("To")))

		return
	}

	if current.timer != nil {
		current.timer.Stop()
	}

	if expiresSeconds == 0 {
		delete(c.serverPublications, key)
		c.mu.Unlock()

		response := c.responseFor(request, 200, "OK", headerTag(request.Headers.Get("To")))
		response.Headers.Add("Expires", "0")
		c.sendServerResponse(request, response)

		c.notifyPublish(request)

		return
	}

	entityTag, err := randomToken(8)
	if err != nil {
		c.mu.Unlock()
		c.sendResponse(request, 500, "Server Internal Error", headerTag(request.Headers.Get("To")))

		return
	}

	current = serverPublication{entityTag: entityTag}
	current.timer = time.AfterFunc(time.Duration(expiresSeconds)*time.Second, func() {
		c.mu.Lock()
		if latest, found := c.serverPublications[key]; found && latest.entityTag == entityTag {
			delete(c.serverPublications, key)
		}
		c.mu.Unlock()
	})
	c.serverPublications[key] = current
	c.mu.Unlock()

	response := c.responseFor(request, 200, "OK", headerTag(request.Headers.Get("To")))
	response.Headers.Add("SIP-ETag", entityTag)
	response.Headers.Add("Expires", strconv.Itoa(expiresSeconds))
	c.sendServerResponse(request, response)
	c.notifyPublish(request)
}

func (c *Client) notifyPublish(request *sip.Message) {
	if c.OnPublish != nil {
		event := PublishEvent{
			Event:       request.Headers.Get("Event"),
			EntityTag:   request.Headers.Get("SIP-If-Match"),
			ContentType: request.Headers.Get("Content-Type"),
			Body:        append([]byte(nil), request.Body...),
			Request:     request,
		}

		go c.OnPublish(c, event)
	}
}

func (c *Client) handleREFER(request *sip.Message) {
	if strings.TrimSpace(request.Headers.Get("Refer-To")) == "" {
		c.sendResponse(request, 400, "Bad Request", headerTag(request.Headers.Get("To")))

		return
	}

	c.sendResponse(request, 202, "Accepted", headerTag(request.Headers.Get("To")))

	if c.OnRefer != nil {
		event := ReferEvent{
			ReferTo:    request.Headers.Get("Refer-To"),
			ReferredBy: request.Headers.Get("Referred-By"),
			Request:    request,
		}

		go c.OnRefer(c, event)
	}
}

func mediaTypeAllowed(value string, accepted []string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return false
	}

	for _, candidate := range accepted {
		acceptedType, _, parseErr := mime.ParseMediaType(candidate)
		if parseErr == nil && strings.EqualFold(mediaType, acceptedType) {
			return true
		}
	}

	return false
}

func validDTMFDigit(digit rune) bool {
	return digit >= '0' && digit <= '9' || digit >= 'A' && digit <= 'D' || digit >= 'a' && digit <= 'd' || digit == '*' || digit == '#'
}

func (c *Client) messageBodyLimit() int {
	c.ensureSynchronization()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.MaxMessageBodyBytes <= 0 {
		c.MaxMessageBodyBytes = 64 << 10
	}

	return c.MaxMessageBodyBytes
}
