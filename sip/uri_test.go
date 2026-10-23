package sip

import (
	"errors"
	"testing"
)

func TestParseURIRoundTripWithIPv6AndHeaders(t *testing.T) {
	value := "sips:alice:secret@[2001:db8::10]:5061;transport=tcp;lr?subject=project&priority=urgent"
	uri, err := ParseURI(value)
	if err != nil {
		t.Fatal(err)
	}

	if uri.Scheme != "sips" || uri.User != "alice" || uri.Password != "secret" || uri.Host != "2001:db8::10" || uri.Port != 5061 {
		t.Fatalf("parsed URI = %#v", uri)
	}

	if transportValue, ok := uri.Parameter("Transport"); !ok || transportValue != "tcp" {
		t.Fatalf("transport parameter = %q, %t", transportValue, ok)
	}

	if _, ok := uri.Parameter("lr"); !ok {
		t.Fatal("lr flag parameter was not preserved")
	}

	if subject, ok := uri.Header("Subject"); !ok || subject != "project" {
		t.Fatalf("subject header = %q, %t", subject, ok)
	}

	if serialized := uri.String(); serialized != value {
		t.Fatalf("serialized URI = %q", serialized)
	}
}

func TestParseAddressRoundTrip(t *testing.T) {
	value := `"Alice Example" <sip:alice@example.com;user=phone>;tag=abc;expires=60`
	address, err := ParseAddress(value)
	if err != nil {
		t.Fatal(err)
	}

	if address.DisplayName != "Alice Example" || address.URI.User != "alice" || address.URI.Host != "example.com" {
		t.Fatalf("parsed address = %#v", address)
	}

	if tag, ok := address.Parameter("TAG"); !ok || tag != "abc" {
		t.Fatalf("tag = %q, %t", tag, ok)
	}

	if serialized := address.String(); serialized != value {
		t.Fatalf("serialized address = %q", serialized)
	}
}

func TestParseURIRejectsMalformedInput(t *testing.T) {
	invalid := []string{
		"http://example.com",
		"sip:",
		"sip:alice@2001:db8::1",
		"sip:alice@example.com:70000",
		"sip:alice@example.com;bad parameter",
		"sip:alice@example.com%zz",
		"sip:alice@example.com\r\nInjected: yes",
	}

	for _, value := range invalid {
		if _, err := ParseURI(value); !errors.Is(err, ErrMalformedURI) {
			t.Errorf("ParseURI(%q) error = %v", value, err)
		}
	}
}

func TestMessageParsesRequestURI(t *testing.T) {
	request := NewRequest("OPTIONS", "sip:service@example.com:5070;transport=tcp")
	uri, err := request.ParsedRequestURI()
	if err != nil {
		t.Fatal(err)
	}

	if uri.User != "service" || uri.Port != 5070 {
		t.Fatalf("request URI = %#v", uri)
	}
}
