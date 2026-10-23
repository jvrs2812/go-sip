package client

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jvrs2812/go-sip/sip"
)

type TransferStatus string

const (
	TransferPending   TransferStatus = "pending"
	TransferActive    TransferStatus = "active"
	TransferSucceeded TransferStatus = "succeeded"
	TransferFailed    TransferStatus = "failed"
	TransferExpired   TransferStatus = "expired"
)

type Transfer struct {
	client *Client

	mu sync.RWMutex

	callID        string
	localTag      string
	remoteTag     string
	referTo       string
	status        TransferStatus
	reason        string
	responseCode  int
	notifications chan NotifyEvent
	timer         *time.Timer
}

func newTransfer(call *Call, referTo string) *Transfer {
	return &Transfer{
		client:        call.client,
		callID:        call.ID,
		localTag:      call.LocalTag,
		remoteTag:     call.RemoteTag,
		referTo:       referTo,
		status:        TransferPending,
		notifications: make(chan NotifyEvent, 16),
	}
}

func (t *Transfer) Status() TransferStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.status
}

func (t *Transfer) Reason() string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.reason
}

func (t *Transfer) ResponseCode() int {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.responseCode
}

func (t *Transfer) ReferTo() string {
	return t.referTo
}

func (t *Transfer) Notifications() <-chan NotifyEvent {
	return t.notifications
}

func (t *Transfer) matchesNotify(request *sip.Message) bool {
	return headerTag(request.Headers.Get("To")) == t.localTag && headerTag(request.Headers.Get("From")) == t.remoteTag
}

func (t *Transfer) applyNotify(event NotifyEvent, subscriptionStatus SubscriptionStatus, reason string, responseCode int) {
	t.mu.Lock()
	t.responseCode = responseCode

	switch {
	case responseCode < 200:
		t.status = TransferActive
	case responseCode < 300:
		t.status = TransferSucceeded
	default:
		t.status = TransferFailed
	}

	if subscriptionStatus == SubscriptionTerminated {
		t.reason = reason

		if t.status == TransferPending || t.status == TransferActive {
			t.status = TransferFailed
		}

		if t.timer != nil {
			t.timer.Stop()
			t.timer = nil
		}
	}
	t.mu.Unlock()

	select {
	case t.notifications <- event:
	default:
		t.client.reportError(errors.New("transfer notification queue is full"))
	}

	if subscriptionStatus == SubscriptionTerminated {
		t.client.removeTransfer(t.callID, t)
	}
}

func (t *Transfer) startTimer(timeout time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.timer != nil {
		t.timer.Stop()
	}

	t.timer = time.AfterFunc(timeout, func() {
		t.mu.Lock()
		if t.status == TransferPending || t.status == TransferActive {
			t.status = TransferExpired
			t.reason = "timeout"
		}
		t.timer = nil
		t.mu.Unlock()

		t.client.removeTransfer(t.callID, t)
	})
}

func (t *Transfer) stopTimer() {
	t.mu.Lock()
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	t.mu.Unlock()
}

func (c *Client) removeTransfer(callID string, transfer *Transfer) {
	c.mu.Lock()
	if c.transfers[callID] == transfer {
		delete(c.transfers, callID)
	}
	c.mu.Unlock()
}

func sipfragStatus(body []byte) (int, error) {
	line := strings.TrimSpace(string(body))
	if firstLine, _, found := strings.Cut(line, "\n"); found {
		line = strings.TrimSpace(firstLine)
	}

	fields := strings.Fields(line)
	if len(fields) < 2 || !strings.EqualFold(fields[0], "SIP/2.0") {
		return 0, errors.New("invalid message/sipfrag status line")
	}

	status, err := strconv.Atoi(fields[1])
	if err != nil || status < 100 || status > 699 {
		return 0, errors.New("invalid message/sipfrag status code")
	}

	return status, nil
}
