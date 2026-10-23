package nat

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/pion/ice/v4"
	"github.com/pion/stun/v3"
)

var (
	ErrClosed            = errors.New("ICE agent is closed")
	ErrInvalidRemoteICE  = errors.New("invalid remote ICE parameters")
	ErrUnsupportedServer = errors.New("unsupported ICE server")
)

type ICERole string

const (
	ICEControlling ICERole = "controlling"
	ICEControlled  ICERole = "controlled"
)

type ICEServer struct {
	URL        string
	Username   string
	Credential string
}

type ICEConfig struct {
	Servers         []ICEServer
	PortMin         uint16
	PortMax         uint16
	IncludeLoopback bool
	HostOnly        bool
	RelayOnly       bool
}

type ICECredentials struct {
	UsernameFragment string
	Password         string
}

type RemoteICE struct {
	Credentials ICECredentials
	Candidates  []string
}

type CandidatePair struct {
	Local  string
	Remote string
}

type ICEAgent struct {
	agent *ice.Agent

	mu         sync.RWMutex
	closed     bool
	candidates []string
}

func NewICEAgent(config ICEConfig) (*ICEAgent, error) {
	if config.HostOnly && config.RelayOnly {
		return nil, errors.New("ICE host-only and relay-only modes are mutually exclusive")
	}

	urls, err := parseServers(config.Servers)
	if err != nil {
		return nil, err
	}

	agentConfig := &ice.AgentConfig{
		Urls:            urls,
		PortMin:         config.PortMin,
		PortMax:         config.PortMax,
		IncludeLoopback: config.IncludeLoopback,
		NetworkTypes:    []ice.NetworkType{ice.NetworkTypeUDP4, ice.NetworkTypeUDP6},
	}

	if config.HostOnly {
		agentConfig.CandidateTypes = []ice.CandidateType{ice.CandidateTypeHost}
	} else if config.RelayOnly {
		agentConfig.CandidateTypes = []ice.CandidateType{ice.CandidateTypeRelay}
	}

	underlying, err := ice.NewAgent(agentConfig)
	if err != nil {
		return nil, fmt.Errorf("create ICE agent: %w", err)
	}

	agent := &ICEAgent{agent: underlying}

	return agent, nil
}

func (a *ICEAgent) Credentials() (ICECredentials, error) {
	if err := a.ensureOpen(); err != nil {
		return ICECredentials{}, err
	}

	usernameFragment, password, err := a.agent.GetLocalUserCredentials()
	if err != nil {
		return ICECredentials{}, fmt.Errorf("get local ICE credentials: %w", err)
	}

	return ICECredentials{UsernameFragment: usernameFragment, Password: password}, nil
}

func (a *ICEAgent) Gather(ctx context.Context) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if err := a.ensureOpen(); err != nil {
		return nil, err
	}

	a.mu.Lock()
	a.candidates = nil
	a.mu.Unlock()

	complete := make(chan struct{})
	var completeOnce sync.Once

	if err := a.agent.OnCandidate(func(candidate ice.Candidate) {
		if candidate == nil {
			completeOnce.Do(func() {
				close(complete)
			})

			return
		}

		a.mu.Lock()
		a.candidates = append(a.candidates, candidate.Marshal())
		a.mu.Unlock()
	}); err != nil {
		return nil, fmt.Errorf("install ICE candidate handler: %w", err)
	}

	if err := a.agent.GatherCandidates(); err != nil {
		return nil, fmt.Errorf("gather ICE candidates: %w", err)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-complete:
	}

	a.mu.RLock()
	candidates := append([]string(nil), a.candidates...)
	a.mu.RUnlock()

	return candidates, nil
}

func (a *ICEAgent) Connect(ctx context.Context, remote RemoteICE, role ICERole) (net.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if err := a.ensureOpen(); err != nil {
		return nil, err
	}

	if remote.Credentials.UsernameFragment == "" || remote.Credentials.Password == "" {
		return nil, ErrInvalidRemoteICE
	}

	for _, rawCandidate := range remote.Candidates {
		candidate, err := ice.UnmarshalCandidate(rawCandidate)
		if err != nil {
			return nil, fmt.Errorf("%w: candidate: %v", ErrInvalidRemoteICE, err)
		}

		if err := a.agent.AddRemoteCandidate(candidate); err != nil {
			return nil, fmt.Errorf("add remote ICE candidate: %w", err)
		}
	}

	switch role {
	case ICEControlling:
		connection, err := a.agent.Dial(ctx, remote.Credentials.UsernameFragment, remote.Credentials.Password)
		if err != nil {
			return nil, fmt.Errorf("connect controlling ICE agent: %w", err)
		}

		return connection, nil
	case ICEControlled:
		connection, err := a.agent.Accept(ctx, remote.Credentials.UsernameFragment, remote.Credentials.Password)
		if err != nil {
			return nil, fmt.Errorf("connect controlled ICE agent: %w", err)
		}

		return connection, nil
	default:
		return nil, fmt.Errorf("%w: role %q", ErrInvalidRemoteICE, role)
	}
}

func (a *ICEAgent) Restart(ctx context.Context) ([]string, error) {
	if err := a.ensureOpen(); err != nil {
		return nil, err
	}

	if err := a.agent.Restart("", ""); err != nil {
		return nil, fmt.Errorf("restart ICE agent: %w", err)
	}

	return a.Gather(ctx)
}

func (a *ICEAgent) SelectedPair() (CandidatePair, bool) {
	if err := a.ensureOpen(); err != nil {
		return CandidatePair{}, false
	}

	pair, err := a.agent.GetSelectedCandidatePair()
	if err != nil || pair == nil {
		return CandidatePair{}, false
	}

	return CandidatePair{Local: pair.Local.Marshal(), Remote: pair.Remote.Marshal()}, true
}

func (a *ICEAgent) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()

		return nil
	}

	a.closed = true
	a.mu.Unlock()

	if err := a.agent.Close(); err != nil {
		return fmt.Errorf("close ICE agent: %w", err)
	}

	return nil
}

func (a *ICEAgent) ensureOpen() error {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.closed {
		return ErrClosed
	}

	return nil
}

func parseServers(servers []ICEServer) ([]*stun.URI, error) {
	urls := make([]*stun.URI, 0, len(servers))

	for _, server := range servers {
		parsed, err := stun.ParseURI(server.URL)
		if err != nil {
			return nil, fmt.Errorf("%w %q: %v", ErrUnsupportedServer, server.URL, err)
		}

		if parsed.Scheme == stun.SchemeTypeTURN || parsed.Scheme == stun.SchemeTypeTURNS {
			if server.Username == "" || server.Credential == "" {
				return nil, fmt.Errorf("%w %q: TURN credentials are required", ErrUnsupportedServer, server.URL)
			}

			parsed.Username = server.Username
			parsed.Password = server.Credential
		}

		urls = append(urls, parsed)
	}

	return urls, nil
}
