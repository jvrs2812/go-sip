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

var ErrPublicationVersionMismatch = errors.New("publication entity tag is no longer current")

type PublicationStatus string

const (
	PublicationActive  PublicationStatus = "active"
	PublicationRemoved PublicationStatus = "removed"
	PublicationExpired PublicationStatus = "expired"
)

type Publication struct {
	client *Client

	opMu sync.Mutex
	mu   sync.RWMutex

	target      string
	event       string
	entityTag   string
	status      PublicationStatus
	expiresAt   time.Time
	expiryTimer *time.Timer
}

func (c *Client) NewPublication(ctx context.Context, target, event, contentType string, body []byte, expires time.Duration) (*Publication, error) {
	if expires <= 0 {
		return nil, errors.New("initial PUBLISH expiration must be positive")
	}

	event = eventToken(event)
	if event == "" {
		return nil, errors.New("PUBLISH event is required")
	}

	publication := &Publication{
		client: c,
		target: normalizeSIPURI(target),
		event:  event,
	}

	response, granted, err := publication.publish(ctx, "", contentType, body, expires, true)
	if err != nil {
		return nil, err
	}

	entityTag := strings.TrimSpace(response.Headers.Get("SIP-ETag"))
	if entityTag == "" {
		return nil, errors.New("successful initial PUBLISH response is missing SIP-ETag")
	}

	publication.mu.Lock()
	publication.entityTag = entityTag
	publication.status = PublicationActive
	publication.scheduleExpiryLocked(granted)
	publication.mu.Unlock()

	c.mu.Lock()
	c.publications[publication] = struct{}{}
	c.mu.Unlock()

	return publication, nil
}

func (p *Publication) Refresh(ctx context.Context, expires time.Duration) error {
	if expires <= 0 {
		return errors.New("publication refresh expiration must be positive")
	}

	return p.update(ctx, "", nil, expires)
}

func (p *Publication) Update(ctx context.Context, contentType string, body []byte, expires time.Duration) error {
	if expires <= 0 {
		return errors.New("publication update expiration must be positive")
	}

	if len(body) == 0 {
		return errors.New("publication update body is required")
	}

	return p.update(ctx, contentType, body, expires)
}

func (p *Publication) Remove(ctx context.Context) error {
	p.opMu.Lock()
	defer p.opMu.Unlock()

	p.mu.RLock()
	entityTag := p.entityTag
	p.mu.RUnlock()

	_, _, err := p.publish(ctx, entityTag, "", nil, 0, false)
	if err != nil {
		return err
	}

	p.mu.Lock()
	p.status = PublicationRemoved
	p.expiresAt = time.Time{}
	if p.expiryTimer != nil {
		p.expiryTimer.Stop()
		p.expiryTimer = nil
	}
	p.mu.Unlock()

	p.client.removePublication(p)

	return nil
}

func (p *Publication) Status() PublicationStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.status
}

func (p *Publication) EntityTag() string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.entityTag
}

func (p *Publication) ExpiresAt() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.expiresAt
}

func (p *Publication) update(ctx context.Context, contentType string, body []byte, expires time.Duration) error {
	p.opMu.Lock()
	defer p.opMu.Unlock()

	p.mu.RLock()
	entityTag := p.entityTag
	status := p.status
	p.mu.RUnlock()

	if status == PublicationRemoved {
		return errors.New("publication has been removed")
	}

	response, granted, err := p.publish(ctx, entityTag, contentType, body, expires, true)
	if err != nil {
		return err
	}

	updatedTag := strings.TrimSpace(response.Headers.Get("SIP-ETag"))
	if updatedTag == "" {
		updatedTag = entityTag
	}

	p.mu.Lock()
	p.entityTag = updatedTag
	p.status = PublicationActive
	p.scheduleExpiryLocked(granted)
	p.mu.Unlock()

	return nil
}

func (p *Publication) publish(ctx context.Context, entityTag, contentType string, body []byte, expires time.Duration, handleMinimum bool) (*sip.Message, time.Duration, error) {
	response, err := p.send(ctx, entityTag, contentType, body, expires)
	if err != nil {
		return nil, 0, err
	}

	if response.StatusCode == 423 && handleMinimum {
		minimum, parseErr := parsePositiveSeconds(response.Headers.Get("Min-Expires"))
		if parseErr != nil {
			return nil, 0, fmt.Errorf("PUBLISH interval too brief without valid Min-Expires: %w", parseErr)
		}

		expires = minimum
		response, err = p.send(ctx, entityTag, contentType, body, expires)
		if err != nil {
			return nil, 0, err
		}
	}

	if response.StatusCode == 412 {
		return response, 0, ErrPublicationVersionMismatch
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response, 0, fmt.Errorf("PUBLISH failed: %d %s", response.StatusCode, response.Reason)
	}

	granted := expires
	if value := response.Headers.Get("Expires"); value != "" && expires > 0 {
		if parsed, parseErr := parsePositiveSeconds(value); parseErr == nil {
			granted = parsed
		}
	}

	return response, granted, nil
}

func (p *Publication) send(ctx context.Context, entityTag, contentType string, body []byte, expires time.Duration) (*sip.Message, error) {
	request, err := p.client.newOutOfDialogRequest(ctx, "PUBLISH", p.target)
	if err != nil {
		return nil, err
	}

	request.Headers.Add("Event", p.event)
	request.Headers.Add("Expires", strconv.Itoa(max(int(expires/time.Second), 0)))

	if entityTag != "" {
		request.Headers.Add("SIP-If-Match", entityTag)
	}

	if contentType != "" {
		request.Headers.Add("Content-Type", contentType)
	}

	request.Body = append([]byte(nil), body...)

	return p.client.roundTrip(ctx, request)
}

func (p *Publication) scheduleExpiryLocked(expires time.Duration) {
	if p.expiryTimer != nil {
		p.expiryTimer.Stop()
	}

	p.expiresAt = time.Now().Add(expires)
	p.expiryTimer = time.AfterFunc(expires, func() {
		p.mu.Lock()
		if p.status == PublicationActive {
			p.status = PublicationExpired
		}
		p.expiryTimer = nil
		p.mu.Unlock()

		p.client.removePublication(p)
	})
}

func (p *Publication) stopTimer() {
	p.mu.Lock()
	if p.expiryTimer != nil {
		p.expiryTimer.Stop()
		p.expiryTimer = nil
	}
	p.mu.Unlock()
}

func (c *Client) removePublication(publication *Publication) {
	c.mu.Lock()
	delete(c.publications, publication)
	c.mu.Unlock()
}
