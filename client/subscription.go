package client

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jvrs2812/go-sip/sip"
)

type SubscriptionStatus string

const (
	SubscriptionPending    SubscriptionStatus = "pending"
	SubscriptionActive     SubscriptionStatus = "active"
	SubscriptionTerminated SubscriptionStatus = "terminated"
	SubscriptionExpired    SubscriptionStatus = "expired"
)

type Subscription struct {
	client *Client

	opMu sync.Mutex
	mu   sync.RWMutex

	target        string
	event         string
	callID        string
	localTag      string
	remoteTag     string
	localURI      string
	remoteURI     string
	remoteTarget  string
	routeSet      []string
	cseq          uint32
	status        SubscriptionStatus
	reason        string
	expiresAt     time.Time
	timer         *time.Timer
	notifications chan NotifyEvent
}

func (c *Client) NewSubscription(ctx context.Context, target, event string, expires time.Duration) (*Subscription, error) {
	if expires <= 0 {
		return nil, errors.New("initial SUBSCRIBE expiration must be positive")
	}

	event = eventToken(event)
	if event == "" {
		return nil, errors.New("SUBSCRIBE event is required")
	}

	request, err := c.newOutOfDialogRequest(ctx, "SUBSCRIBE", target)
	if err != nil {
		return nil, err
	}

	sequence, _, err := request.CSeq()
	if err != nil {
		return nil, err
	}

	subscription := &Subscription{
		client:        c,
		target:        request.RequestURI,
		event:         event,
		callID:        request.Headers.Get("Call-ID"),
		localTag:      headerTag(request.Headers.Get("From")),
		localURI:      request.Headers.Get("From"),
		remoteURI:     request.Headers.Get("To"),
		remoteTarget:  request.RequestURI,
		cseq:          sequence,
		status:        SubscriptionPending,
		notifications: make(chan NotifyEvent, 32),
	}

	c.mu.Lock()
	c.subscriptions[subscription.callID] = subscription
	c.mu.Unlock()

	request.Headers.Add("Event", event)
	request.Headers.Add("Expires", strconv.Itoa(int(expires/time.Second)))
	response, err := c.roundTrip(ctx, request)
	if err != nil {
		c.removeSubscription(subscription.callID)

		return nil, err
	}

	if response.StatusCode == 423 {
		minimum, parseErr := parsePositiveSeconds(response.Headers.Get("Min-Expires"))
		if parseErr != nil {
			c.removeSubscription(subscription.callID)

			return nil, fmt.Errorf("SUBSCRIBE interval too brief without valid Min-Expires: %w", parseErr)
		}

		expires = minimum
		response, expires, err = subscription.refresh(ctx, expires, false)
		if err != nil {
			c.removeSubscription(subscription.callID)

			return nil, err
		}
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		c.removeSubscription(subscription.callID)

		return nil, fmt.Errorf("SUBSCRIBE failed: %d %s", response.StatusCode, response.Reason)
	}

	subscription.applyResponse(response, expires)

	return subscription, nil
}

func (s *Subscription) Refresh(ctx context.Context, expires time.Duration) error {
	if expires <= 0 {
		return errors.New("subscription refresh expiration must be positive")
	}

	s.opMu.Lock()
	defer s.opMu.Unlock()

	response, granted, err := s.refresh(ctx, expires, true)
	if err != nil {
		return err
	}

	s.applyResponse(response, granted)

	return nil
}

func (s *Subscription) Cancel(ctx context.Context) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()

	response, _, err := s.refresh(ctx, 0, true)
	if err != nil {
		return err
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("unsubscribe failed: %d %s", response.StatusCode, response.Reason)
	}

	s.mu.Lock()
	if s.status != SubscriptionTerminated {
		s.status = SubscriptionTerminated
		s.reason = "deactivated"
		if s.timer != nil {
			s.timer.Stop()
		}

		s.expiresAt = time.Now().Add(32 * time.Second)
		s.timer = time.AfterFunc(32*time.Second, func() {
			s.client.removeSubscription(s.callID)
		})
	}
	s.mu.Unlock()

	return nil
}

func (s *Subscription) Status() SubscriptionStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.status
}

func (s *Subscription) Reason() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.reason
}

func (s *Subscription) ExpiresAt() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.expiresAt
}

func (s *Subscription) Notifications() <-chan NotifyEvent {
	return s.notifications
}

func (s *Subscription) Event() string {
	return s.event
}

func (s *Subscription) refresh(ctx context.Context, expires time.Duration, handleMinimum bool) (*sip.Message, time.Duration, error) {
	request, err := s.newRequest(expires)
	if err != nil {
		return nil, 0, err
	}

	response, err := s.client.roundTrip(ctx, request)
	if err != nil {
		return nil, 0, err
	}

	if response.StatusCode == 423 && handleMinimum {
		minimum, parseErr := parsePositiveSeconds(response.Headers.Get("Min-Expires"))
		if parseErr != nil {
			return nil, 0, fmt.Errorf("SUBSCRIBE interval too brief without valid Min-Expires: %w", parseErr)
		}

		expires = minimum

		request, err = s.newRequest(minimum)
		if err != nil {
			return nil, 0, err
		}

		response, err = s.client.roundTrip(ctx, request)
		if err != nil {
			return nil, 0, err
		}
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response, 0, fmt.Errorf("SUBSCRIBE failed: %d %s", response.StatusCode, response.Reason)
	}

	return response, expires, nil
}

func (s *Subscription) newRequest(expires time.Duration) (*sip.Message, error) {
	branch, err := randomToken(12)
	if err != nil {
		return nil, err
	}

	localHost, localPort, err := s.client.localEndpoint()
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.cseq++
	cseq := s.cseq
	remoteTarget := s.remoteTarget
	localURI := s.localURI
	remoteURI := s.remoteURI
	routeSet := append([]string(nil), s.routeSet...)
	s.mu.Unlock()

	request := sip.NewRequest("SUBSCRIBE", remoteTarget)
	request.Headers.Add("Via", fmt.Sprintf("SIP/2.0/%s %s;branch=z9hG4bK%s;rport", strings.ToUpper(string(s.client.Network)), hostPort(localHost, localPort), branch))
	request.Headers.Add("Max-Forwards", "70")
	request.Headers.Add("From", localURI)
	request.Headers.Add("To", remoteURI)
	request.Headers.Add("Call-ID", s.callID)
	request.Headers.Add("CSeq", fmt.Sprintf("%d SUBSCRIBE", cseq))
	request.Headers.Add("Contact", fmt.Sprintf("<%s>", s.client.contactURI(localHost, localPort)))
	request.Headers.Add("Event", s.event)
	request.Headers.Add("Expires", strconv.Itoa(max(int(expires/time.Second), 0)))
	request.Headers.Add("User-Agent", s.client.UserAgent)

	for _, route := range routeSet {
		request.Headers.Add("Route", route)
	}

	return request, nil
}

func (s *Subscription) applyResponse(response *sip.Message, requested time.Duration) {
	granted := requested
	if value := response.Headers.Get("Expires"); value != "" {
		if parsed, err := parsePositiveSeconds(value); err == nil {
			granted = parsed
		}
	}

	s.mu.Lock()
	s.remoteTag = headerTag(response.Headers.Get("To"))
	s.remoteURI = response.Headers.Get("To")
	if target := contactURI(response.Headers.Get("Contact")); target != "" {
		s.remoteTarget = target
	}
	s.routeSet = reverseStrings(response.Headers.Values("Record-Route"))
	s.scheduleExpiryLocked(granted)
	s.mu.Unlock()
}

func (s *Subscription) applyNotify(event NotifyEvent, status SubscriptionStatus, reason string, expires time.Duration) {
	s.mu.Lock()
	if s.remoteTag == "" {
		s.remoteTag = headerTag(event.Request.Headers.Get("From"))
	}

	s.status = status
	s.reason = reason
	if status == SubscriptionActive || status == SubscriptionPending {
		if expires > 0 {
			s.scheduleExpiryLocked(expires)
		}
	} else if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.mu.Unlock()

	select {
	case s.notifications <- event:
	default:
		s.client.reportError(fmt.Errorf("subscription notification queue full: %s", s.callID))
	}

	if status == SubscriptionTerminated {
		s.client.removeSubscription(s.callID)
	}
}

func (s *Subscription) matchesNotify(request *sip.Message, event string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if event != s.event {
		return false
	}

	toTag := headerTag(request.Headers.Get("To"))
	fromTag := headerTag(request.Headers.Get("From"))
	if toTag == "" || fromTag == "" || toTag != s.localTag {
		return false
	}

	return s.remoteTag == "" || fromTag == s.remoteTag
}

func (s *Subscription) scheduleExpiryLocked(expires time.Duration) {
	if s.timer != nil {
		s.timer.Stop()
	}

	s.expiresAt = time.Now().Add(expires)
	s.timer = time.AfterFunc(expires, func() {
		s.mu.Lock()
		if s.status != SubscriptionTerminated {
			s.status = SubscriptionExpired
			s.reason = "timeout"
		}
		s.timer = nil
		s.mu.Unlock()

		s.client.removeSubscription(s.callID)
	})
}

func (s *Subscription) stopTimer() {
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.mu.Unlock()
}

func (c *Client) removeSubscription(callID string) {
	c.mu.Lock()
	delete(c.subscriptions, callID)
	c.mu.Unlock()
}

func eventToken(value string) string {
	token, _, _ := strings.Cut(strings.TrimSpace(value), ";")

	return strings.ToLower(strings.TrimSpace(token))
}

func parsePositiveSeconds(value string) (time.Duration, error) {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds <= 0 {
		return 0, errors.New("positive expiration in seconds is required")
	}

	return time.Duration(seconds) * time.Second, nil
}

func parseSubscriptionState(value string) (SubscriptionStatus, string, time.Duration, error) {
	parts := strings.Split(value, ";")
	if len(parts) == 0 {
		return "", "", 0, errors.New("Subscription-State is required")
	}

	status := SubscriptionStatus(strings.ToLower(strings.TrimSpace(parts[0])))
	if status != SubscriptionPending && status != SubscriptionActive && status != SubscriptionTerminated {
		return "", "", 0, fmt.Errorf("unsupported subscription state %q", status)
	}

	var reason string
	var expires time.Duration
	for _, rawParameter := range parts[1:] {
		name, value, found := strings.Cut(strings.TrimSpace(rawParameter), "=")
		if !found {
			continue
		}

		switch strings.ToLower(name) {
		case "reason":
			reason = value
		case "expires":
			seconds, err := strconv.Atoi(value)
			if err != nil || seconds < 0 {
				return "", "", 0, errors.New("invalid Subscription-State expires parameter")
			}

			expires = time.Duration(seconds) * time.Second
		}
	}

	return status, reason, expires, nil
}
