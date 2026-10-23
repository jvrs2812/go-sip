package discovery

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/jvrs2812/go-sip/transport"
)

type NAPTR struct {
	Order       uint16
	Preference  uint16
	Service     string
	Replacement string
}

type Lookup interface {
	LookupNAPTR(context.Context, string) ([]NAPTR, error)
	LookupSRV(context.Context, string, string, string) (string, []*net.SRV, error)
}

type AddressLookup interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type Target struct {
	Host       string
	ServerName string
	Port       int
	Network    transport.Network
	Priority   uint16
	Weight     uint16
}

type Resolver struct {
	Lookup      Lookup
	Cache       *Cache
	RandomUint  func(uint32) uint32
	ResolveAddr bool
}

type cacheEntry struct {
	targets   []Target
	expiresAt time.Time
}

type Cache struct {
	TTL time.Duration
	Now func() time.Time

	mu      sync.Mutex
	entries map[string]cacheEntry
}

func NewCache(ttl time.Duration) *Cache {
	return &Cache{TTL: ttl}
}

func (c *Cache) Get(key string) ([]Target, bool) {
	if c == nil || c.TTL <= 0 {
		return nil, false
	}

	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok || !now.Before(entry.expiresAt) {
		delete(c.entries, key)

		return nil, false
	}

	return cloneTargets(entry.targets), true
}

func (c *Cache) Put(key string, targets []Target) {
	if c == nil || c.TTL <= 0 || len(targets) == 0 {
		return
	}

	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.entries == nil {
		c.entries = make(map[string]cacheEntry)
	}

	c.entries[key] = cacheEntry{
		targets:   cloneTargets(targets),
		expiresAt: now.Add(c.TTL),
	}
}

func (c *Cache) Clear() {
	if c == nil {
		return
	}

	c.mu.Lock()
	c.entries = nil
	c.mu.Unlock()
}

func (r Resolver) Resolve(ctx context.Context, host string, secure bool, preferred transport.Network) ([]Target, error) {
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")

	if host == "" {
		return nil, errors.New("SIP discovery host is required")
	}

	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return []Target{{Host: ip.String(), Port: defaultPort(secure, preferred), Network: defaultNetwork(secure, preferred)}}, nil
	}

	cacheKey := fmt.Sprintf("%s|%t|%s|%t", strings.ToLower(host), secure, preferred, r.ResolveAddr)
	if targets, ok := r.Cache.Get(cacheKey); ok {
		return targets, nil
	}

	lookup := r.Lookup

	if lookup == nil {
		lookup = systemLookup{}
	}

	if preferred != "" {
		targets, err := r.lookupTransport(ctx, lookup, host, secure, preferred)

		if err == nil && len(targets) > 0 {
			r.Cache.Put(cacheKey, targets)

			return targets, nil
		}

		targets = r.fallbackTargets(ctx, lookup, host, secure, preferred)
		r.Cache.Put(cacheKey, targets)

		return targets, nil
	}

	records, naptrErr := lookup.LookupNAPTR(ctx, host)

	if naptrErr == nil {
		sort.SliceStable(records, func(left, right int) bool {
			if records[left].Order != records[right].Order {
				return records[left].Order < records[right].Order
			}

			return records[left].Preference < records[right].Preference
		})

		for _, record := range records {
			network, recordSecure, ok := serviceTransport(record.Service)

			if !ok || recordSecure != secure {
				continue
			}

			replacement := strings.TrimSuffix(record.Replacement, ".")
			targets, err := r.lookupSRVName(ctx, lookup, replacement, network)

			if err == nil && len(targets) > 0 {
				r.Cache.Put(cacheKey, targets)

				return targets, nil
			}
		}
	}

	order := []transport.Network{transport.UDP, transport.TCP}

	if secure {
		order = []transport.Network{transport.TLS}
	}

	for _, network := range order {
		targets, err := r.lookupTransport(ctx, lookup, host, secure, network)

		if err == nil && len(targets) > 0 {
			r.Cache.Put(cacheKey, targets)

			return targets, nil
		}
	}

	targets := r.fallbackTargets(ctx, lookup, host, secure, "")
	r.Cache.Put(cacheKey, targets)

	return targets, nil
}

type systemLookup struct{}

func (systemLookup) LookupSRV(ctx context.Context, service, protocol, name string) (string, []*net.SRV, error) {
	return net.DefaultResolver.LookupSRV(ctx, service, protocol, name)
}

func (systemLookup) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

func (systemLookup) LookupNAPTR(ctx context.Context, host string) ([]NAPTR, error) {
	configuration, err := dns.ClientConfigFromFile("/etc/resolv.conf")

	if err != nil || len(configuration.Servers) == 0 {
		return nil, fmt.Errorf("load DNS resolver configuration: %w", err)
	}

	message := new(dns.Msg)
	message.SetQuestion(dns.Fqdn(host), dns.TypeNAPTR)

	client := new(dns.Client)
	response, _, err := client.ExchangeContext(ctx, message, net.JoinHostPort(configuration.Servers[0], configuration.Port))

	if err != nil {
		return nil, err
	}

	if response.Rcode != dns.RcodeSuccess {
		return nil, fmt.Errorf("NAPTR lookup failed with DNS rcode %s", dns.RcodeToString[response.Rcode])
	}

	records := make([]NAPTR, 0, len(response.Answer))

	for _, answer := range response.Answer {
		record, ok := answer.(*dns.NAPTR)

		if !ok {
			continue
		}

		records = append(records, NAPTR{
			Order:       record.Order,
			Preference:  record.Preference,
			Service:     record.Service,
			Replacement: record.Replacement,
		})
	}

	if len(records) == 0 {
		return nil, errors.New("no NAPTR records")
	}

	return records, nil
}

func (r Resolver) lookupTransport(ctx context.Context, lookup Lookup, host string, secure bool, network transport.Network) ([]Target, error) {
	service := "sip"
	protocol := string(network)

	if secure {
		service = "sips"
	}

	if network == transport.TLS {
		protocol = "tcp"
	}

	_, records, err := lookup.LookupSRV(ctx, service, protocol, host)

	if err != nil {
		return nil, err
	}

	return r.convertSRV(ctx, lookup, records, network), nil
}

func (r Resolver) lookupSRVName(ctx context.Context, lookup Lookup, replacement string, network transport.Network) ([]Target, error) {
	service, protocol, domain, ok := splitSRVName(replacement)

	if !ok {
		return nil, fmt.Errorf("invalid NAPTR replacement %q", replacement)
	}

	_, records, err := lookup.LookupSRV(ctx, service, protocol, domain)

	if err != nil {
		return nil, err
	}

	return r.convertSRV(ctx, lookup, records, network), nil
}

func (r Resolver) convertSRV(ctx context.Context, lookup Lookup, records []*net.SRV, network transport.Network) []Target {
	targets := make([]Target, 0, len(records))

	for _, record := range records {
		if record == nil || record.Target == "." {
			continue
		}

		targets = append(targets, Target{
			Host:       strings.TrimSuffix(record.Target, "."),
			ServerName: strings.TrimSuffix(record.Target, "."),
			Port:       int(record.Port),
			Network:    network,
			Priority:   record.Priority,
			Weight:     record.Weight,
		})
	}

	targets = weightedOrder(targets, r.randomUint)

	if r.ResolveAddr {
		targets = expandAddresses(ctx, lookup, targets)
	}

	return targets
}

func (r Resolver) fallbackTargets(ctx context.Context, lookup Lookup, host string, secure bool, preferred transport.Network) []Target {
	target := Target{
		Host:       host,
		ServerName: host,
		Port:       defaultPort(secure, preferred),
		Network:    defaultNetwork(secure, preferred),
	}
	targets := []Target{target}

	if r.ResolveAddr {
		targets = expandAddresses(ctx, lookup, targets)
	}

	return targets
}

func expandAddresses(ctx context.Context, lookup Lookup, targets []Target) []Target {
	addressLookup, ok := lookup.(AddressLookup)
	if !ok {
		return targets
	}

	expanded := make([]Target, 0, len(targets)*2)
	for _, target := range targets {
		addresses, err := addressLookup.LookupIPAddr(ctx, target.Host)
		if err != nil || len(addresses) == 0 {
			expanded = append(expanded, target)

			continue
		}

		seen := make(map[string]struct{}, len(addresses))
		for _, address := range addresses {
			host := address.IP.String()
			if host == "" {
				continue
			}

			if _, exists := seen[host]; exists {
				continue
			}

			seen[host] = struct{}{}
			resolved := target
			resolved.Host = host
			expanded = append(expanded, resolved)
		}
	}

	return expanded
}

func weightedOrder(targets []Target, randomUint func(uint32) uint32) []Target {
	ordered := append([]Target(nil), targets...)
	sort.SliceStable(ordered, func(left, right int) bool {
		return ordered[left].Priority < ordered[right].Priority
	})

	result := make([]Target, 0, len(ordered))
	for start := 0; start < len(ordered); {
		end := start + 1
		for end < len(ordered) && ordered[end].Priority == ordered[start].Priority {
			end++
		}

		group := append([]Target(nil), ordered[start:end]...)
		for len(group) > 0 {
			index := weightedIndex(group, randomUint)
			result = append(result, group[index])
			group = append(group[:index], group[index+1:]...)
		}

		start = end
	}

	return result
}

func weightedIndex(targets []Target, randomUint func(uint32) uint32) int {
	var total uint32
	for _, target := range targets {
		total += uint32(target.Weight)
	}

	if total == 0 {
		return int(randomUint(uint32(len(targets))))
	}

	selected := randomUint(total)
	var cumulative uint32
	for index, target := range targets {
		cumulative += uint32(target.Weight)
		if selected < cumulative {
			return index
		}
	}

	return len(targets) - 1
}

func (r Resolver) randomUint(maximum uint32) uint32 {
	if maximum <= 1 {
		return 0
	}

	if r.RandomUint != nil {
		return r.RandomUint(maximum) % maximum
	}

	var buffer [4]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return 0
	}

	return binary.BigEndian.Uint32(buffer[:]) % maximum
}

func cloneTargets(targets []Target) []Target {
	return append([]Target(nil), targets...)
}

func splitSRVName(value string) (string, string, string, bool) {
	parts := strings.SplitN(strings.TrimPrefix(value, "_"), ".", 3)

	if len(parts) != 3 {
		return "", "", "", false
	}

	return parts[0], strings.TrimPrefix(parts[1], "_"), parts[2], true
}

func serviceTransport(service string) (transport.Network, bool, bool) {
	switch strings.ToUpper(strings.TrimSpace(service)) {
	case "SIP+D2U":
		return transport.UDP, false, true

	case "SIP+D2T":
		return transport.TCP, false, true

	case "SIPS+D2T":
		return transport.TLS, true, true

	default:
		return "", false, false
	}
}

func defaultNetwork(secure bool, preferred transport.Network) transport.Network {
	if preferred != "" {
		return preferred
	}

	if secure {
		return transport.TLS
	}

	return transport.UDP
}

func defaultPort(secure bool, preferred transport.Network) int {
	if secure || preferred == transport.TLS {
		return 5061
	}

	return 5060
}
