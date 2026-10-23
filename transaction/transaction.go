package transaction

import (
	"strings"
	"sync"
	"time"
)

type State string

const (
	Calling    State = "calling"
	Trying     State = "trying"
	Proceeding State = "proceeding"
	Completed  State = "completed"
	Confirmed  State = "confirmed"
	Terminated State = "terminated"
)

const (
	defaultT1 = 500 * time.Millisecond
	defaultT2 = 4 * time.Second
)

type Client struct {
	mu sync.RWMutex

	invite     bool
	unreliable bool
	state      State
	interval   time.Duration
}

func NewClient(method string, unreliable bool) *Client {
	invite := strings.EqualFold(method, "INVITE")
	state := Trying
	if invite {
		state = Calling
	}

	return &Client{
		invite:     invite,
		unreliable: unreliable,
		state:      state,
		interval:   defaultT1,
	}
}

func (t *Client) State() State {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.state
}

func (t *Client) Receive(statusCode int) (duplicate bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if statusCode < 100 {
		return false
	}

	if t.state == Completed || t.state == Terminated {
		return statusCode >= 200
	}

	if statusCode < 200 {
		t.state = Proceeding

		return false
	}

	t.state = Completed

	return false
}

func (t *Client) RetransmissionInterval() (time.Duration, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if !t.unreliable || t.state == Completed || t.state == Terminated {
		return 0, false
	}

	if t.invite && t.state == Proceeding {
		return 0, false
	}

	return t.interval, true
}

func (t *Client) Retransmitted() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.interval *= 2
	if t.interval > defaultT2 {
		t.interval = defaultT2
	}
}

func (t *Client) Terminate() {
	t.mu.Lock()
	t.state = Terminated
	t.mu.Unlock()
}

type Server struct {
	mu sync.RWMutex

	invite bool
	state  State
}

func NewServer(method string) *Server {
	return &Server{
		invite: strings.EqualFold(method, "INVITE"),
		state:  Trying,
	}
}

func (t *Server) State() State {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.state
}

func (t *Server) Send(statusCode int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.state == Terminated || statusCode < 100 {
		return
	}

	if statusCode < 200 {
		t.state = Proceeding

		return
	}

	t.state = Completed
}

func (t *Server) Acknowledge() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.invite && t.state == Completed {
		t.state = Confirmed
	}
}

func (t *Server) Terminate() {
	t.mu.Lock()
	t.state = Terminated
	t.mu.Unlock()
}
