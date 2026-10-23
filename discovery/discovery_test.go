package discovery

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/jvrs2812/go-sip/transport"
)

type fakeLookup struct {
	naptr []NAPTR
	srv   map[string][]*net.SRV
	ips   map[string][]net.IPAddr

	mu           *sync.Mutex
	naptrLookups *int
	srvLookups   *int
	ipLookups    *int
}

func (f fakeLookup) LookupNAPTR(context.Context, string) ([]NAPTR, error) {
	f.increment(f.naptrLookups)

	if len(f.naptr) == 0 {
		return nil, errors.New("not found")
	}

	return f.naptr, nil
}

func (f fakeLookup) LookupSRV(_ context.Context, service, protocol, name string) (string, []*net.SRV, error) {
	f.increment(f.srvLookups)

	records := f.srv[service+"|"+protocol+"|"+name]

	if len(records) == 0 {
		return "", nil, errors.New("not found")
	}

	return "", records, nil
}

func (f fakeLookup) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	f.increment(f.ipLookups)

	addresses := f.ips[host]
	if len(addresses) == 0 {
		return nil, errors.New("not found")
	}

	return addresses, nil
}

func (f fakeLookup) increment(counter *int) {
	if counter == nil {
		return
	}

	if f.mu != nil {
		f.mu.Lock()
		defer f.mu.Unlock()
	}

	*counter++
}

func TestResolveUsesNAPTRAndSRVPriority(t *testing.T) {
	lookup := fakeLookup{
		naptr: []NAPTR{{Order: 10, Preference: 10, Service: "SIP+D2T", Replacement: "_sip._tcp.example.com."}},
		srv: map[string][]*net.SRV{
			"sip|tcp|example.com": {
				{Target: "backup.example.com.", Port: 5070, Priority: 20, Weight: 1},
				{Target: "primary.example.com.", Port: 5060, Priority: 10, Weight: 1},
			},
		},
	}

	targets, err := (Resolver{Lookup: lookup}).Resolve(context.Background(), "example.com", false, "")

	if err != nil {
		t.Fatal(err)
	}

	if len(targets) != 2 || targets[0].Host != "primary.example.com" || targets[0].Network != transport.TCP {
		t.Fatalf("targets = %#v", targets)
	}
}

func TestResolveFallsBackToExplicitHost(t *testing.T) {
	targets, err := (Resolver{Lookup: fakeLookup{}}).Resolve(context.Background(), "example.com", true, transport.TLS)

	if err != nil {
		t.Fatal(err)
	}

	if len(targets) != 1 || targets[0].Port != 5061 || targets[0].Network != transport.TLS {
		t.Fatalf("targets = %#v", targets)
	}
}

func TestResolveOrdersSRVByPriorityAndWeight(t *testing.T) {
	lookup := fakeLookup{
		srv: map[string][]*net.SRV{
			"sip|tcp|example.com": {
				{Target: "light.example.com.", Port: 5060, Priority: 10, Weight: 1},
				{Target: "heavy.example.com.", Port: 5060, Priority: 10, Weight: 9},
				{Target: "backup.example.com.", Port: 5060, Priority: 20, Weight: 100},
			},
		},
	}

	resolver := Resolver{
		Lookup: lookup,
		RandomUint: func(maximum uint32) uint32 {
			return maximum - 1
		},
	}

	targets, err := resolver.Resolve(context.Background(), "example.com", false, transport.TCP)
	if err != nil {
		t.Fatal(err)
	}

	if len(targets) != 3 {
		t.Fatalf("target count = %d, want 3", len(targets))
	}

	if targets[0].Host != "heavy.example.com" || targets[1].Host != "light.example.com" || targets[2].Host != "backup.example.com" {
		t.Fatalf("weighted targets = %#v", targets)
	}
}

func TestResolveExpandsIPv4AndIPv6WithoutLosingTLSServerName(t *testing.T) {
	lookup := fakeLookup{
		srv: map[string][]*net.SRV{
			"sips|tcp|example.com": {
				{Target: "edge.example.com.", Port: 5061, Priority: 10, Weight: 1},
			},
		},
		ips: map[string][]net.IPAddr{
			"edge.example.com": {
				{IP: net.ParseIP("192.0.2.10")},
				{IP: net.ParseIP("2001:db8::10")},
				{IP: net.ParseIP("192.0.2.10")},
			},
		},
	}

	targets, err := (Resolver{Lookup: lookup, ResolveAddr: true}).Resolve(context.Background(), "example.com", true, transport.TLS)
	if err != nil {
		t.Fatal(err)
	}

	if len(targets) != 2 {
		t.Fatalf("target count = %d, want 2: %#v", len(targets), targets)
	}

	if targets[0].Host != "192.0.2.10" || targets[1].Host != "2001:db8::10" {
		t.Fatalf("resolved targets = %#v", targets)
	}

	for _, target := range targets {
		if target.ServerName != "edge.example.com" {
			t.Fatalf("TLS server name = %q", target.ServerName)
		}
	}
}

func TestResolveCachesAndExpiresResults(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	lookups := 0
	mutex := new(sync.Mutex)
	lookup := fakeLookup{
		srv: map[string][]*net.SRV{
			"sip|tcp|example.com": {
				{Target: "edge.example.com.", Port: 5060, Priority: 10, Weight: 1},
			},
		},
		mu:         mutex,
		srvLookups: &lookups,
	}
	cache := NewCache(time.Minute)
	cache.Now = func() time.Time { return now }
	resolver := Resolver{Lookup: lookup, Cache: cache}

	first, err := resolver.Resolve(context.Background(), "example.com", false, transport.TCP)
	if err != nil {
		t.Fatal(err)
	}

	first[0].Host = "mutated.invalid"
	second, err := resolver.Resolve(context.Background(), "example.com", false, transport.TCP)
	if err != nil {
		t.Fatal(err)
	}

	if lookups != 1 || second[0].Host != "edge.example.com" {
		t.Fatalf("cache result = %#v, lookups = %d", second, lookups)
	}

	now = now.Add(time.Minute)
	if _, err := resolver.Resolve(context.Background(), "example.com", false, transport.TCP); err != nil {
		t.Fatal(err)
	}

	if lookups != 2 {
		t.Fatalf("lookups after expiration = %d, want 2", lookups)
	}
}

func TestCacheSupportsConcurrentReadersAndWriters(t *testing.T) {
	cache := NewCache(time.Minute)
	target := []Target{{Host: "example.com", Port: 5060, Network: transport.TCP}}

	var waitGroup sync.WaitGroup
	for index := 0; index < 32; index++ {
		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()

			cache.Put("key", target)
			if cached, ok := cache.Get("key"); !ok || len(cached) != 1 {
				t.Errorf("cache read = %#v, %t", cached, ok)
			}
		}()
	}

	waitGroup.Wait()
}
