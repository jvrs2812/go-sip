package auth

import (
	"strings"
	"testing"
)

func TestLegacyMD5Response(t *testing.T) {
	response, err := CalculateResponse(
		Challenge{Realm: "example.com", Nonce: "abcdef", Algorithm: "MD5"},
		Credentials{Username: "alice", Password: "secret"},
		Request{Method: "REGISTER", URI: "sip:example.com"},
	)
	if err != nil {
		t.Fatalf("CalculateResponse() error = %v", err)
	}

	if response != "a78abdd5e960b1ad429a41df9ce15386" {
		t.Fatalf("response = %q", response)
	}
}

func TestSHA256WithQOP(t *testing.T) {
	challenge, err := ParseChallenge(`Digest realm="sip.example.com", nonce="n-1", algorithm=SHA-256, qop="auth,auth-int", opaque="o"`)
	if err != nil {
		t.Fatalf("ParseChallenge() error = %v", err)
	}

	header, err := Authorization(
		challenge,
		Credentials{Username: "1001", Password: "password"},
		Request{Method: "REGISTER", URI: "sip:sip.example.com", NonceCount: 2, CNonce: "fixed"},
	)
	if err != nil {
		t.Fatalf("Authorization() error = %v", err)
	}

	for _, fragment := range []string{"algorithm=SHA-256", "qop=auth", "nc=00000002", `cnonce="fixed"`, `opaque="o"`} {
		if !strings.Contains(header, fragment) {
			t.Fatalf("header %q does not contain %q", header, fragment)
		}
	}

	response, err := CalculateResponse(challenge, Credentials{Username: "1001", Password: "password"}, Request{Method: "REGISTER", URI: "sip:sip.example.com", NonceCount: 2, CNonce: "fixed"})
	if err != nil {
		t.Fatalf("CalculateResponse() error = %v", err)
	}

	if response != "35c68777bb5cc0590bb90306fe785d5fcd65da254c002b2e47d3f4a3459b59c9" {
		t.Fatalf("response = %q", response)
	}
}

func TestParseChallengeWithEscapedQuote(t *testing.T) {
	challenge, err := ParseChallenge(`Digest realm="a\"b", nonce="n", stale=true`)
	if err != nil {
		t.Fatalf("ParseChallenge() error = %v", err)
	}

	if challenge.Realm != `a"b` || !challenge.Stale {
		t.Fatalf("challenge = %#v", challenge)
	}
}
