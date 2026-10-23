package client

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jvrs2812/go-sip/auth"
	"github.com/jvrs2812/go-sip/discovery"
	"github.com/jvrs2812/go-sip/sip"
	siptransaction "github.com/jvrs2812/go-sip/transaction"
	"github.com/jvrs2812/go-sip/transport"
	"github.com/jvrs2812/go-sip/types"
)

type Config struct {
	ServerHost                  string
	ServerPort                  int
	OutboundProxy               string
	UseDNSDiscovery             bool
	DNSLookup                   discovery.Lookup
	DNSCacheTTL                 time.Duration
	DisableDNSAddressResolution bool
	LocalAddress                string
	AdvertisedAddress           string
	RTPPort                     int
	Username                    string
	Password                    string
	OAuthTokenProvider          auth.TokenProvider
	AKAProvider                 auth.AKAProvider
	Network                     transport.Network
	TLSConfig                   *tls.Config
	WebSocketPath               string
	WebSocketHeader             http.Header
	PingInterval                time.Duration
	RegistrationExpires         time.Duration
	TransactionTimeout          time.Duration
	FailoverTimeout             time.Duration
	ReconnectMinDelay           time.Duration
	ReconnectMaxDelay           time.Duration
	MaxMessageBodyBytes         int
	AcceptedMessageTypes        []string
	AcceptedInfoTypes           []string
	UserAgent                   string
	Logger                      *slog.Logger
	DialTransport               func(context.Context, transport.Config) (transport.Transport, error)
}

type Client struct {
	// Legacy configuration fields. New code should use NewClient with Config.
	IpServer           string
	PortServer         int
	PortForRtp         int
	Ramal              string
	Password           string
	OAuthTokenProvider auth.TokenProvider
	AKAProvider        auth.AKAProvider

	LocalAddress                string
	AdvertisedAddress           string
	OutboundProxy               string
	UseDNSDiscovery             bool
	DNSLookup                   discovery.Lookup
	DNSCacheTTL                 time.Duration
	DisableDNSAddressResolution bool
	Network                     transport.Network
	TLSConfig                   *tls.Config
	WebSocketPath               string
	WebSocketHeader             http.Header
	PingInterval                time.Duration
	RegistrationExpires         time.Duration
	TransactionTimeout          time.Duration
	FailoverTimeout             time.Duration
	ReconnectMinDelay           time.Duration
	ReconnectMaxDelay           time.Duration
	MaxMessageBodyBytes         int
	AcceptedMessageTypes        []string
	AcceptedInfoTypes           []string
	UserAgent                   string
	Logger                      *slog.Logger
	DialTransport               func(context.Context, transport.Config) (transport.Transport, error)

	OnInviteReceived      func(c *Client, inviteData types.InviteData)
	OnAudioReceived       func(c *Client, data types.AudioData)
	OnFirstMedia          func(c *Client, data types.AudioData)
	OnStartCall           func(c *Client, data types.AudioData)
	OnCallEstablished     func(c *Client, call *Call)
	OnEarlyMedia          func(c *Client, call *Call, response *sip.Message)
	OnMessage             func(c *Client, message *sip.Message)
	OnInstantMessage      func(c *Client, event MessageEvent)
	OnInfo                func(c *Client, event InfoEvent)
	OnNotify              func(c *Client, event NotifyEvent)
	OnSubscribe           func(c *Client, event SubscribeEvent)
	OnPublish             func(c *Client, event PublishEvent)
	OnRefer               func(c *Client, event ReferEvent)
	OnProvisionalResponse func(c *Client, response *sip.Message)
	OnError               func(c *Client, err error)

	startMu          *sync.Mutex
	mu               *sync.Mutex
	conn             transport.Transport
	ctx              context.Context
	cancel           context.CancelFunc
	connectionCancel context.CancelFunc
	closed           bool
	wg               *sync.WaitGroup

	waiters            map[string]chan *sip.Message
	calls              map[string]*Call
	serverTransactions map[string]cachedServerResponse
	completedInvites   map[string]*completedInvite
	subscriptions      map[string]*Subscription
	publications       map[*Publication]struct{}
	transfers          map[string]*Transfer
	serverPublications map[string]serverPublication

	registrationCallID  string
	registrationTag     string
	cseq                uint32
	registered          bool
	registrationDesired bool
	renewCancel         context.CancelFunc
	reconnecting        bool
	networkConfigured   bool
	dnsResolver         *discovery.Resolver
}

type OnInviteReceived func(c *Client, inviteData types.InviteData)

func NewClient(config Config) (*Client, error) {
	if strings.TrimSpace(config.ServerHost) == "" {
		return nil, errors.New("client: server host is required")
	}

	if config.ServerPort == 0 {
		switch config.Network {
		case transport.TLS:
			config.ServerPort = 5061
		case transport.WS:
			config.ServerPort = 80
		case transport.WSS:
			config.ServerPort = 443
		default:
			config.ServerPort = 5060
		}
	}

	if config.ServerPort < 1 || config.ServerPort > 65535 {
		return nil, errors.New("client: server port is invalid")
	}

	if strings.TrimSpace(config.Username) == "" {
		return nil, errors.New("client: username is required")
	}

	client := &Client{
		IpServer:                    config.ServerHost,
		PortServer:                  config.ServerPort,
		PortForRtp:                  config.RTPPort,
		Ramal:                       config.Username,
		Password:                    config.Password,
		OAuthTokenProvider:          config.OAuthTokenProvider,
		AKAProvider:                 config.AKAProvider,
		LocalAddress:                config.LocalAddress,
		AdvertisedAddress:           config.AdvertisedAddress,
		OutboundProxy:               config.OutboundProxy,
		UseDNSDiscovery:             config.UseDNSDiscovery,
		DNSLookup:                   config.DNSLookup,
		DNSCacheTTL:                 config.DNSCacheTTL,
		DisableDNSAddressResolution: config.DisableDNSAddressResolution,
		Network:                     config.Network,
		TLSConfig:                   config.TLSConfig,
		WebSocketPath:               config.WebSocketPath,
		WebSocketHeader:             config.WebSocketHeader,
		PingInterval:                config.PingInterval,
		RegistrationExpires:         config.RegistrationExpires,
		TransactionTimeout:          config.TransactionTimeout,
		FailoverTimeout:             config.FailoverTimeout,
		ReconnectMinDelay:           config.ReconnectMinDelay,
		ReconnectMaxDelay:           config.ReconnectMaxDelay,
		MaxMessageBodyBytes:         config.MaxMessageBodyBytes,
		AcceptedMessageTypes:        append([]string(nil), config.AcceptedMessageTypes...),
		AcceptedInfoTypes:           append([]string(nil), config.AcceptedInfoTypes...),
		UserAgent:                   config.UserAgent,
		Logger:                      config.Logger,
		DialTransport:               config.DialTransport,
		startMu:                     &sync.Mutex{},
		mu:                          &sync.Mutex{},
		wg:                          &sync.WaitGroup{},
		waiters:                     make(map[string]chan *sip.Message),
		calls:                       make(map[string]*Call),
		serverTransactions:          make(map[string]cachedServerResponse),
		completedInvites:            make(map[string]*completedInvite),
		subscriptions:               make(map[string]*Subscription),
		publications:                make(map[*Publication]struct{}),
		transfers:                   make(map[string]*Transfer),
		serverPublications:          make(map[string]serverPublication),
		networkConfigured:           config.Network != "",
	}

	client.applyDefaults()

	return client, nil
}

func (c *Client) Start(ctx context.Context) error {
	c.ensureSynchronization()

	c.startMu.Lock()
	defer c.startMu.Unlock()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()

		return net.ErrClosed
	}

	if c.conn != nil {
		c.mu.Unlock()

		return nil
	}

	c.applyDefaultsLocked()
	c.mu.Unlock()

	connection, selectedNetwork, err := c.dialSignaling(ctx)

	if err != nil {
		return fmt.Errorf("connect SIP transport: %w", err)
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = connection.Close()

		return net.ErrClosed
	}

	if c.ctx == nil {
		c.ctx, c.cancel = context.WithCancel(context.Background())
	}

	runCtx, connectionCancel := context.WithCancel(c.ctx)
	c.conn = connection
	c.connectionCancel = connectionCancel
	c.Network = selectedNetwork
	c.mu.Unlock()

	c.wg.Add(1)
	go c.dispatch(connection, runCtx)

	c.logger().Info("SIP transport connected", "network", c.Network, "remote", connection.RemoteAddr().String())

	return nil
}

func (c *Client) Close() error {
	c.ensureSynchronization()

	c.startMu.Lock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		c.startMu.Unlock()

		return nil
	}

	c.closed = true

	if c.renewCancel != nil {
		c.renewCancel()
		c.renewCancel = nil
	}

	if c.cancel != nil {
		c.cancel()
	}

	if c.connectionCancel != nil {
		c.connectionCancel()
		c.connectionCancel = nil
	}

	connection := c.conn
	c.conn = nil
	callsToClose := make([]*Call, 0, len(c.calls))
	for _, call := range c.calls {
		callsToClose = append(callsToClose, call)
	}

	subscriptionsToClose := make([]*Subscription, 0, len(c.subscriptions))
	for _, subscription := range c.subscriptions {
		subscriptionsToClose = append(subscriptionsToClose, subscription)
	}

	publicationsToClose := make([]*Publication, 0, len(c.publications))
	for publication := range c.publications {
		publicationsToClose = append(publicationsToClose, publication)
	}

	transfersToClose := make([]*Transfer, 0, len(c.transfers))
	for _, transfer := range c.transfers {
		transfersToClose = append(transfersToClose, transfer)
	}

	publicationTimers := make([]*time.Timer, 0, len(c.serverPublications))
	for _, publication := range c.serverPublications {
		if publication.timer != nil {
			publicationTimers = append(publicationTimers, publication.timer)
		}
	}

	c.mu.Unlock()
	c.startMu.Unlock()

	for _, call := range callsToClose {
		_ = call.closeMedia()
	}

	for _, subscription := range subscriptionsToClose {
		subscription.stopTimer()
	}

	for _, publication := range publicationsToClose {
		publication.stopTimer()
	}

	for _, transfer := range transfersToClose {
		transfer.stopTimer()
	}

	for _, timer := range publicationTimers {
		timer.Stop()
	}

	var err error

	if connection != nil {
		err = connection.Close()
	}

	c.wg.Wait()

	return err
}

func (c *Client) Register(ctx context.Context) error {
	c.ensureSynchronization()

	c.mu.Lock()
	c.registrationDesired = true
	c.mu.Unlock()

	return c.register(ctx, c.registrationDuration(), true)
}

func (c *Client) Unregister(ctx context.Context) error {
	c.ensureSynchronization()

	c.mu.Lock()
	c.registrationDesired = false
	if c.renewCancel != nil {
		c.renewCancel()
		c.renewCancel = nil
	}

	c.mu.Unlock()

	if err := c.register(ctx, 0, false); err != nil {
		return err
	}

	c.mu.Lock()
	c.registered = false
	c.mu.Unlock()

	return nil
}

func (c *Client) IsRegistered() bool {
	c.ensureSynchronization()

	c.mu.Lock()
	defer c.mu.Unlock()
	return c.registered
}

func (c *Client) register(ctx context.Context, expires time.Duration, scheduleRenewal bool) error {
	if err := c.Start(ctx); err != nil {
		return err
	}

	c.ensureRegistrationIdentity()

	expiresSeconds := int(expires / time.Second)
	challengeAttempts := 0
	minimumRetries := 0
	nonceCounts := make(map[string]uint32)

	var authorizationName, authorizationValue string

	for {
		request, err := c.buildRegister(expiresSeconds, authorizationName, authorizationValue)
		if err != nil {
			return err
		}

		response, err := c.roundTrip(ctx, request)
		if err != nil {
			return fmt.Errorf("REGISTER transaction: %w", err)
		}

		switch response.StatusCode {
		case 200, 201, 202:
			granted := expires

			if value := response.Headers.Get("Expires"); value != "" {
				if seconds, parseErr := strconv.Atoi(strings.TrimSpace(value)); parseErr == nil && seconds >= 0 {
					granted = time.Duration(seconds) * time.Second
				}
			}

			c.mu.Lock()
			c.registered = expiresSeconds > 0
			c.mu.Unlock()

			if scheduleRenewal && granted > 0 {
				c.scheduleRegistrationRenewal(granted)
			}

			return nil

		case 401, 407:
			if challengeAttempts >= 4 {
				return fmt.Errorf("REGISTER authentication rejected after %d challenges", challengeAttempts+1)
			}

			challengeHeader := "WWW-Authenticate"
			authorizationName = "Authorization"
			if response.StatusCode == 407 {
				challengeHeader = "Proxy-Authenticate"
				authorizationName = "Proxy-Authorization"
			}

			authorizationValue, err = c.authorizationForChallenges(
				ctx,
				response.Headers.Values(challengeHeader),
				request,
				nonceCounts,
			)
			if err != nil {
				return fmt.Errorf("authenticate REGISTER: %w", err)
			}

			challengeAttempts++

		case 423:
			if minimumRetries >= 1 {
				return errors.New("REGISTER rejected with repeated 423 Interval Too Brief")
			}

			seconds, parseErr := strconv.Atoi(strings.TrimSpace(response.Headers.Get("Min-Expires")))
			if parseErr != nil || seconds <= 0 {
				return errors.New("REGISTER 423 response has invalid Min-Expires")
			}

			expiresSeconds = seconds
			minimumRetries++

		default:
			return fmt.Errorf("REGISTER failed: %d %s", response.StatusCode, response.Reason)
		}
	}
}

func (c *Client) authorizationForChallenges(ctx context.Context, values []string, request *sip.Message, nonceCounts map[string]uint32) (string, error) {
	var failures []error
	values = preferAKAv2Challenges(values)

	for _, value := range values {
		value = strings.TrimSpace(value)

		switch {
		case strings.HasPrefix(strings.ToLower(value), "bearer"):
			if c.OAuthTokenProvider == nil {
				failures = append(failures, errors.New("Bearer challenge received without an OAuth token provider"))

				continue
			}

			challenge, err := auth.ParseBearerChallenge(value)
			if err != nil {
				failures = append(failures, err)

				continue
			}

			token, err := c.OAuthTokenProvider.Token(ctx, challenge)
			if err != nil {
				failures = append(failures, fmt.Errorf("obtain OAuth token: %w", err))

				continue
			}

			authorization, err := auth.BearerAuthorization(token, time.Now())
			if err != nil {
				failures = append(failures, err)

				continue
			}

			return authorization, nil

		case strings.HasPrefix(strings.ToLower(value), "digest"):
			challenge, err := auth.ParseChallenge(value)
			if err != nil {
				failures = append(failures, err)

				continue
			}

			nonceCounts[challenge.Nonce]++
			password := c.Password

			if auth.IsAKAv2Algorithm(challenge.Algorithm) {
				if c.AKAProvider == nil {
					failures = append(failures, errors.New("AKAv2 challenge received without an AKA provider"))

					continue
				}

				akaResult, err := c.AKAProvider.AuthenticateAKA(ctx, auth.AKAChallenge{
					Realm: challenge.Realm,
					Nonce: challenge.Nonce,
				})
				if err != nil {
					failures = append(failures, fmt.Errorf("AKA authentication: %w", err))

					continue
				}

				keys, err := auth.DeriveAKAv2(challenge.Algorithm, akaResult)
				if err != nil {
					failures = append(failures, err)

					continue
				}

				password = keys.Password
			}

			authorization, err := auth.Authorization(challenge, auth.Credentials{Username: c.Ramal, Password: password}, auth.Request{
				Method:     request.Method,
				URI:        request.RequestURI,
				Body:       request.Body,
				NonceCount: nonceCounts[challenge.Nonce],
			})
			if err != nil {
				failures = append(failures, err)

				continue
			}

			return authorization, nil

		default:
			failures = append(failures, fmt.Errorf("unsupported authentication challenge %q", value))
		}
	}

	if len(failures) == 0 {
		return "", errors.New("authentication response has no challenge")
	}

	return "", errors.Join(failures...)
}

func preferAKAv2Challenges(values []string) []string {
	ordered := make([]string, 0, len(values))

	for _, value := range values {
		if strings.Contains(strings.ToLower(value), "algorithm=akav2-") {
			ordered = append(ordered, value)
		}
	}

	for _, value := range values {
		if !strings.Contains(strings.ToLower(value), "algorithm=akav2-") {
			ordered = append(ordered, value)
		}
	}

	return ordered
}

func (c *Client) WatchEvents() {
	ctx, cancel := context.WithTimeout(context.Background(), c.transactionDuration())
	defer cancel()
	if err := c.Start(ctx); err != nil {
		c.reportError(err)
	}
}

// RegisterSip is retained for source compatibility. Prefer (*Client).Register.
func RegisterSip(client Client) {
	ctx, cancel := context.WithTimeout(context.Background(), client.transactionDuration())
	defer cancel()
	if err := client.Register(ctx); err != nil {
		client.reportError(err)
	}
}

// HandleAuth is retained for source compatibility. Authentication is now handled by Register.
func (c *Client) HandleAuth(_ string) {
	ctx, cancel := context.WithTimeout(context.Background(), c.transactionDuration())
	defer cancel()
	if err := c.Register(ctx); err != nil {
		c.reportError(err)
	}
}

func (c *Client) roundTrip(ctx context.Context, request *sip.Message) (*sip.Message, error) {
	transaction := siptransaction.NewClient(request.Method, c.Network == transport.UDP)
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

	transactionCtx, cancel := context.WithTimeout(ctx, c.transactionDuration())
	defer cancel()

	var retransmitTimer *time.Timer
	var retransmit <-chan time.Time
	var lastReliableSequence string

	if retransmitInterval, ok := transaction.RetransmissionInterval(); ok {
		retransmitTimer = time.NewTimer(retransmitInterval)
		retransmit = retransmitTimer.C

		defer retransmitTimer.Stop()
	}

	for {
		select {
		case response := <-waiter:
			if response == nil {
				return nil, net.ErrClosed
			}

			if response.StatusCode >= 200 {
				transaction.Receive(response.StatusCode)

				return response, nil
			}

			transaction.Receive(response.StatusCode)

			if c.OnProvisionalResponse != nil {
				c.OnProvisionalResponse(c, response)
			}

			if request.Method == "INVITE" && headerHasToken(response.Headers.Get("Require"), "100rel") {
				reliableSequence := strings.TrimSpace(response.Headers.Get("RSeq"))

				if reliableSequence != "" && reliableSequence != lastReliableSequence {
					if err := c.acknowledgeReliableProvisional(transactionCtx, request, response); err != nil {
						return nil, err
					}

					lastReliableSequence = reliableSequence
				}
			}

			if _, shouldRetransmit := transaction.RetransmissionInterval(); !shouldRetransmit && retransmitTimer != nil {
				if !retransmitTimer.Stop() {
					select {
					case <-retransmitTimer.C:
					default:
					}
				}

				retransmit = nil
			}

		case <-retransmit:
			if err := connection.Send(transactionCtx, request); err != nil {
				return nil, err
			}

			transaction.Retransmitted()
			if retransmitInterval, ok := transaction.RetransmissionInterval(); ok {
				retransmitTimer.Reset(retransmitInterval)
			} else {
				retransmit = nil
			}

		case <-transactionCtx.Done():
			return nil, transactionCtx.Err()
		case <-c.done():
			return nil, net.ErrClosed
		}
	}
}

func (c *Client) send(ctx context.Context, message *sip.Message) error {
	c.mu.Lock()
	connection := c.conn
	c.mu.Unlock()
	if connection == nil {
		return errors.New("SIP transport is not connected")
	}

	return connection.Send(ctx, message)
}

func (c *Client) dispatch(connection transport.Transport, runCtx context.Context) {
	defer c.wg.Done()

	messages := connection.Messages()
	errorsChannel := connection.Errors()
	var transportErr error

	for {
		if messages == nil && errorsChannel == nil {
			if transportErr == nil {
				transportErr = net.ErrClosed
			}

			c.handleTransportLoss(connection, transportErr)

			return
		}

		select {
		case message, ok := <-messages:
			if !ok {
				messages = nil

				continue
			}

			if message.IsResponse() {
				c.dispatchResponse(message)
			} else {
				c.handleRequest(message)
			}
		case err, ok := <-errorsChannel:
			if !ok {
				errorsChannel = nil

				continue
			}

			if err != nil {
				transportErr = err
			}
		case <-runCtx.Done():
			return
		}
	}
}

func (c *Client) handleTransportLoss(connection transport.Transport, cause error) {
	c.mu.Lock()
	if c.conn != connection {
		c.mu.Unlock()

		return
	}

	c.conn = nil
	c.registered = false

	if c.connectionCancel != nil {
		c.connectionCancel()
		c.connectionCancel = nil
	}

	for _, waiter := range c.waiters {
		select {
		case waiter <- nil:
		default:
		}
	}

	shouldReconnect := !c.closed && !c.reconnecting
	if shouldReconnect {
		c.reconnecting = true
	}

	c.mu.Unlock()

	_ = connection.Close()

	if cause != nil && !errors.Is(cause, net.ErrClosed) {
		c.reportError(fmt.Errorf("SIP transport lost: %w", cause))
	}

	if shouldReconnect {
		c.wg.Add(1)
		go c.reconnectLoop()
	}
}

func (c *Client) reconnectLoop() {
	defer c.wg.Done()
	defer func() {
		c.mu.Lock()
		c.reconnecting = false
		c.mu.Unlock()
	}()

	delay := c.ReconnectMinDelay

	for {
		wait := jitteredDelay(delay)
		timer := time.NewTimer(wait)

		select {
		case <-timer.C:
		case <-c.done():
			if !timer.Stop() {
				<-timer.C
			}

			return
		}

		attemptCtx, cancel := context.WithTimeout(c.doneContext(), c.transactionDuration())
		err := c.Start(attemptCtx)
		cancel()

		if err != nil {
			c.reportError(fmt.Errorf("reconnect SIP transport: %w", err))
			delay = nextBackoff(delay, c.ReconnectMaxDelay)

			continue
		}

		c.mu.Lock()
		registrationDesired := c.registrationDesired
		c.mu.Unlock()

		if !registrationDesired {
			return
		}

		registerCtx, registerCancel := context.WithTimeout(c.doneContext(), c.transactionDuration())
		err = c.register(registerCtx, c.registrationDuration(), true)
		registerCancel()

		if err == nil {
			return
		}

		c.mu.Lock()
		stillConnected := c.conn != nil
		c.mu.Unlock()

		if stillConnected {
			c.reportError(fmt.Errorf("restore SIP registration: %w", err))

			return
		}

		delay = nextBackoff(delay, c.ReconnectMaxDelay)
	}
}

func (c *Client) dispatchResponse(message *sip.Message) {
	key, err := message.TransactionKey()
	if err != nil {
		c.reportError(err)
		return
	}

	c.mu.Lock()
	completed := c.completedInvites[key]
	if completed != nil && time.Now().After(completed.expiresAt) {
		delete(c.completedInvites, key)
		completed = nil
	}

	waiter := c.waiters[key]
	c.mu.Unlock()

	if completed != nil && message.StatusCode >= 200 {
		go c.handleCompletedInviteResponse(completed, message)

		return
	}

	if waiter == nil {
		c.logger().Debug("unmatched SIP response", "status", message.StatusCode, "transaction", key)
		return
	}

	select {
	case waiter <- message:
	default:
		c.reportError(fmt.Errorf("transaction response queue full: %s", key))
	}
}

func (c *Client) handleRequest(request *sip.Message) {
	if c.resendCachedResponse(request) {
		return
	}

	if c.OnMessage != nil {
		c.OnMessage(c, request)
	}

	switch request.Method {
	case "INVITE":
		c.handleInvite(request)
	case "ACK":
		c.handleACK(request)
	case "BYE":
		c.handleBYE(request)
	case "CANCEL":
		c.handleCANCEL(request)
	case "OPTIONS":
		c.handleOPTIONS(request)
	case "PRACK":
		c.handlePRACK(request)
	case "MESSAGE":
		c.handleMESSAGE(request)
	case "INFO":
		c.handleINFO(request)
	case "NOTIFY":
		c.handleNOTIFY(request)
	case "SUBSCRIBE":
		c.handleSUBSCRIBE(request)
	case "PUBLISH":
		c.handlePUBLISH(request)
	case "REFER":
		c.handleREFER(request)
	default:
		response := c.responseFor(request, 405, "Method Not Allowed", "")
		response.Headers.Add("Allow", "INVITE, ACK, BYE, CANCEL, OPTIONS, MESSAGE, INFO, REFER, SUBSCRIBE, NOTIFY, PUBLISH, PRACK")

		c.sendServerResponse(request, response)
	}
}

func (c *Client) buildRegister(expires int, authorizationName, authorizationValue string) (*sip.Message, error) {
	cseq := c.nextCSeq()
	uri := c.serverURI()
	request := sip.NewRequest("REGISTER", uri)
	branch, err := randomToken(12)
	if err != nil {
		return nil, err
	}

	localHost, localPort, err := c.localEndpoint()
	if err != nil {
		return nil, err
	}

	transportName := strings.ToUpper(string(c.Network))
	if transportName == "TLS" {
		transportName = "TLS"
	}

	request.Headers.Add("Via", fmt.Sprintf("SIP/2.0/%s %s;branch=z9hG4bK%s;rport", transportName, hostPort(localHost, localPort), branch))
	request.Headers.Add("Max-Forwards", "70")
	request.Headers.Add("From", fmt.Sprintf("<%s>;tag=%s", c.addressOfRecord(), c.registrationTag))
	request.Headers.Add("To", fmt.Sprintf("<%s>", c.addressOfRecord()))
	request.Headers.Add("Call-ID", c.registrationCallID)
	request.Headers.Add("CSeq", fmt.Sprintf("%d REGISTER", cseq))
	request.Headers.Add("Contact", fmt.Sprintf("<%s;transport=%s>", c.contactURI(localHost, localPort), strings.ToLower(string(c.Network))))
	request.Headers.Add("Expires", strconv.Itoa(expires))
	request.Headers.Add("User-Agent", c.UserAgent)
	if authorizationValue != "" {
		request.Headers.Add(authorizationName, authorizationValue)
	}

	return request, nil
}

func (c *Client) responseFor(request *sip.Message, code int, reason, localTag string) *sip.Message {
	response := sip.NewResponse(code, reason)
	for _, via := range request.Headers.Values("Via") {
		response.Headers.Add("Via", via)
	}

	response.Headers.Add("From", request.Headers.Get("From"))
	to := request.Headers.Get("To")
	if localTag != "" && !strings.Contains(strings.ToLower(to), ";tag=") {
		to += ";tag=" + localTag
	}

	response.Headers.Add("To", to)
	response.Headers.Add("Call-ID", request.Headers.Get("Call-ID"))
	response.Headers.Add("CSeq", request.Headers.Get("CSeq"))
	response.Headers.Add("User-Agent", c.UserAgent)
	return response
}

func (c *Client) ensureRegistrationIdentity() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.registrationCallID == "" {
		c.registrationCallID, _ = randomToken(16)
	}

	if c.registrationTag == "" {
		c.registrationTag, _ = randomToken(10)
	}
}

func (c *Client) scheduleRegistrationRenewal(granted time.Duration) {
	delay := granted * 9 / 10
	if delay < time.Second {
		delay = granted / 2
	}

	c.mu.Lock()
	if c.closed || c.ctx == nil {
		c.mu.Unlock()

		return
	}

	if c.renewCancel != nil {
		c.renewCancel()
	}

	ctx, cancel := context.WithCancel(c.ctx)
	c.renewCancel = cancel
	c.wg.Add(1)
	c.mu.Unlock()

	go func() {
		defer c.wg.Done()
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
			requestCtx, requestCancel := context.WithTimeout(ctx, c.transactionDuration())
			defer requestCancel()
			if err := c.register(requestCtx, granted, true); err != nil {
				c.reportError(fmt.Errorf("automatic registration renewal: %w", err))
			}
		case <-ctx.Done():
		}
	}()
}

func (c *Client) nextCSeq() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cseq++
	return c.cseq
}

func (c *Client) done() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctx == nil {
		channel := make(chan struct{})
		return channel
	}

	return c.ctx.Done()
}

func (c *Client) doneContext() context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ctx == nil {
		return context.Background()
	}

	return c.ctx
}

func (c *Client) localEndpoint() (string, int, error) {
	host, port, err := c.transportLocalEndpoint()
	if err != nil {
		return "", 0, err
	}

	c.mu.Lock()
	advertised := c.AdvertisedAddress
	c.mu.Unlock()

	if advertised == "" {
		return host, port, nil
	}

	if advertisedHost, advertisedPort, splitErr := net.SplitHostPort(advertised); splitErr == nil {
		parsedPort, parseErr := strconv.Atoi(advertisedPort)
		if parseErr != nil {
			return "", 0, fmt.Errorf("invalid advertised SIP port: %w", parseErr)
		}

		return advertisedHost, parsedPort, nil
	}

	return strings.Trim(advertised, "[]"), port, nil
}

func (c *Client) transportLocalEndpoint() (string, int, error) {
	c.mu.Lock()
	connection := c.conn
	c.mu.Unlock()

	if connection == nil {
		return "", 0, errors.New("SIP transport is not connected")
	}

	host, portText, err := net.SplitHostPort(connection.LocalAddr().String())
	if err != nil {
		return "", 0, err
	}

	port, err := strconv.Atoi(portText)
	if err != nil {
		return "", 0, err
	}

	return host, port, nil
}

func (c *Client) mediaBindAddress(port int) (string, error) {
	host, _, err := c.transportLocalEndpoint()
	if err != nil {
		return "", err
	}

	if c.LocalAddress != "" {
		configuredHost := c.LocalAddress
		if parsedHost, _, splitErr := net.SplitHostPort(c.LocalAddress); splitErr == nil {
			configuredHost = parsedHost
		}

		configuredHost = strings.Trim(configuredHost, "[]")
		if configuredIP := net.ParseIP(configuredHost); configuredIP != nil && !configuredIP.IsUnspecified() {
			host = configuredIP.String()
		}
	}

	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil || ip.IsUnspecified() {
		return "", fmt.Errorf("local SIP address %q is not usable for RTP bind", host)
	}

	return net.JoinHostPort(ip.String(), strconv.Itoa(port)), nil
}

func (c *Client) serverURI() string {
	scheme := "sip"
	defaultPort := 5060

	switch c.Network {
	case transport.TLS:
		scheme, defaultPort = "sips", 5061
	case transport.WS:
		defaultPort = 80
	case transport.WSS:
		defaultPort = 443
	}

	host := uriHost(c.IpServer)
	if c.PortServer != defaultPort {
		host += ":" + strconv.Itoa(c.PortServer)
	}

	return scheme + ":" + host
}

func (c *Client) addressOfRecord() string {
	scheme := "sip"
	if c.Network == transport.TLS {
		scheme = "sips"
	}

	return fmt.Sprintf("%s:%s@%s", scheme, c.Ramal, uriHost(c.IpServer))
}

func (c *Client) contactURI(host string, port int) string {
	scheme := "sip"
	if c.Network == transport.TLS {
		scheme = "sips"
	}

	return fmt.Sprintf("%s:%s@%s", scheme, c.Ramal, hostPort(host, port))
}

func (c *Client) registrationDuration() time.Duration {
	if c.RegistrationExpires <= 0 {
		return time.Hour
	}

	return c.RegistrationExpires
}

func (c *Client) transactionDuration() time.Duration {
	if c.TransactionTimeout <= 0 {
		return 10 * time.Second
	}

	return c.TransactionTimeout
}

func (c *Client) applyDefaults() {
	c.ensureSynchronization()

	c.mu.Lock()
	defer c.mu.Unlock()
	c.applyDefaultsLocked()
}

var synchronizationInitialization sync.Mutex

func (c *Client) ensureSynchronization() {
	synchronizationInitialization.Lock()
	defer synchronizationInitialization.Unlock()

	if c.startMu == nil {
		c.startMu = &sync.Mutex{}
	}

	if c.mu == nil {
		c.mu = &sync.Mutex{}
	}

	if c.wg == nil {
		c.wg = &sync.WaitGroup{}
	}
}

func (c *Client) applyDefaultsLocked() {
	if c.Network != "" && c.dnsResolver == nil {
		c.networkConfigured = true
	}

	if c.PortServer == 0 {
		switch c.Network {
		case transport.TLS:
			c.PortServer = 5061
		case transport.WS:
			c.PortServer = 80
		case transport.WSS:
			c.PortServer = 443
		default:
			c.PortServer = 5060
		}
	}

	if c.Network == "" {
		c.Network = transport.TCP
	}

	if c.UserAgent == "" {
		c.UserAgent = "go-sip/0.3"
	}

	if c.RegistrationExpires <= 0 {
		c.RegistrationExpires = time.Hour
	}

	if c.TransactionTimeout <= 0 {
		c.TransactionTimeout = 10 * time.Second
	}

	if c.FailoverTimeout <= 0 {
		c.FailoverTimeout = 15 * time.Second
	}

	if c.DNSCacheTTL <= 0 {
		c.DNSCacheTTL = 5 * time.Minute
	}

	if c.UseDNSDiscovery && c.dnsResolver == nil {
		c.dnsResolver = &discovery.Resolver{
			Lookup:      c.DNSLookup,
			Cache:       discovery.NewCache(c.DNSCacheTTL),
			ResolveAddr: !c.DisableDNSAddressResolution,
		}
	}

	if c.ReconnectMinDelay <= 0 {
		c.ReconnectMinDelay = 250 * time.Millisecond
	}

	if c.ReconnectMaxDelay < c.ReconnectMinDelay {
		c.ReconnectMaxDelay = 5 * time.Second
	}

	if c.MaxMessageBodyBytes <= 0 {
		c.MaxMessageBodyBytes = 64 << 10
	}

	if len(c.AcceptedMessageTypes) == 0 {
		c.AcceptedMessageTypes = []string{
			"application/im-iscomposing+xml",
			"message/cpim",
			"text/plain",
		}
	}

	if c.waiters == nil {
		c.waiters = make(map[string]chan *sip.Message)
	}

	if c.calls == nil {
		c.calls = make(map[string]*Call)
	}

	if c.serverTransactions == nil {
		c.serverTransactions = make(map[string]cachedServerResponse)
	}

	if c.completedInvites == nil {
		c.completedInvites = make(map[string]*completedInvite)
	}

	if c.subscriptions == nil {
		c.subscriptions = make(map[string]*Subscription)
	}

	if c.publications == nil {
		c.publications = make(map[*Publication]struct{})
	}

	if c.transfers == nil {
		c.transfers = make(map[string]*Transfer)
	}

	if c.serverPublications == nil {
		c.serverPublications = make(map[string]serverPublication)
	}
}

func (c *Client) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}

	return slog.Default()
}

func (c *Client) reportError(err error) {
	if err == nil {
		return
	}

	c.logger().Error("go-sip error", "error", err)
	if c.OnError != nil {
		c.OnError(c, err)
	} else {
		log.Printf("[go-sip] %v", err)
	}
}

func randomToken(bytesCount int) (string, error) {
	buffer := make([]byte, bytesCount)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}

	return hex.EncodeToString(buffer), nil
}

func randomUint32() (uint32, error) {
	var value [4]byte

	if _, err := rand.Read(value[:]); err != nil {
		return 0, err
	}

	return binary.BigEndian.Uint32(value[:]), nil
}

func jitteredDelay(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}

	randomValue, err := randomUint32()
	if err != nil {
		return delay
	}

	percentage := int64(randomValue%51) - 25

	return delay + time.Duration(int64(delay)*percentage/100)
}

func nextBackoff(current, maximum time.Duration) time.Duration {
	if current <= 0 {
		return maximum
	}

	next := current * 2
	if next < current || next > maximum {
		return maximum
	}

	return next
}

func (c *Client) signalingAddress() string {
	if c.OutboundProxy != "" {
		return c.OutboundProxy
	}

	return net.JoinHostPort(c.IpServer, strconv.Itoa(c.PortServer))
}

func (c *Client) dialSignaling(ctx context.Context) (transport.Transport, transport.Network, error) {
	dialCtx := ctx
	cancel := func() {}
	if c.FailoverTimeout > 0 {
		dialCtx, cancel = context.WithTimeout(ctx, c.FailoverTimeout)
	}
	defer cancel()

	targets := []discovery.Target{{
		Host:    c.IpServer,
		Port:    c.PortServer,
		Network: c.Network,
	}}

	if c.OutboundProxy != "" {
		host, portText, err := net.SplitHostPort(c.OutboundProxy)

		if err != nil {
			return nil, "", fmt.Errorf("invalid outbound proxy: %w", err)
		}

		port, err := strconv.Atoi(portText)

		if err != nil {
			return nil, "", fmt.Errorf("invalid outbound proxy port: %w", err)
		}

		targets = []discovery.Target{{Host: host, Port: port, Network: c.Network}}
	} else if c.UseDNSDiscovery {
		resolver := c.dnsResolver
		if resolver == nil {
			resolver = &discovery.Resolver{
				Lookup:      c.DNSLookup,
				Cache:       discovery.NewCache(c.DNSCacheTTL),
				ResolveAddr: true,
			}
		}

		secure := c.Network == transport.TLS
		preferred := c.Network
		if !c.networkConfigured {
			preferred = ""
		}

		resolved, err := resolver.Resolve(dialCtx, c.IpServer, secure, preferred)

		if err != nil {
			return nil, "", err
		}

		targets = resolved
	}

	var failures []error

	for _, target := range targets {
		tlsConfig := c.TLSConfig
		if (target.Network == transport.TLS || target.Network == transport.WSS) && target.ServerName != "" {
			if tlsConfig == nil {
				tlsConfig = &tls.Config{}
			} else {
				tlsConfig = tlsConfig.Clone()
			}

			if tlsConfig.ServerName == "" {
				tlsConfig.ServerName = target.ServerName
			}
		}

		dialTransport := c.DialTransport
		if dialTransport == nil {
			dialTransport = transport.Dial
		}

		connection, err := dialTransport(dialCtx, transport.Config{
			Network:         target.Network,
			RemoteAddr:      net.JoinHostPort(target.Host, strconv.Itoa(target.Port)),
			LocalAddr:       c.LocalAddress,
			TLSConfig:       tlsConfig,
			WebSocketPath:   c.WebSocketPath,
			WebSocketHeader: c.WebSocketHeader,
			PingInterval:    c.PingInterval,
			DialTimeout:     c.TransactionTimeout,
			WriteTimeout:    c.TransactionTimeout,
			Limits:          sip.DefaultLimits(),
		})

		if err == nil {
			return connection, target.Network, nil
		}

		failures = append(failures, fmt.Errorf("%s/%s: %w", target.Network, net.JoinHostPort(target.Host, strconv.Itoa(target.Port)), err))

		if dialCtx.Err() != nil {
			break
		}
	}

	if err := dialCtx.Err(); err != nil {
		failures = append(failures, err)
	}

	return nil, "", errors.Join(failures...)
}

func headerHasToken(value, token string) bool {
	for _, candidate := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(candidate), token) {
			return true
		}
	}

	return false
}

func uriHost(host string) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]"
	}

	return host
}

func hostPort(host string, port int) string {
	return net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))
}
