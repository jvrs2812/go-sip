package auth

import (
	"strings"
	"testing"
	"time"
)

func TestParseBearerChallengeAndAuthorization(t *testing.T) {
	challenge, err := ParseBearerChallenge(`Bearer realm="sip.example", scope="sip:register sip:invite", auth_server="https://id.example/token"`)
	if err != nil {
		t.Fatal(err)
	}

	if challenge.Realm != "sip.example" || challenge.Scope != "sip:register sip:invite" || challenge.AuthorizationServer != "https://id.example/token" {
		t.Fatalf("unexpected challenge: %#v", challenge)
	}

	authorization, err := BearerAuthorization(Token{
		AccessToken: "opaque-access-token",
		ExpiresAt:   time.Now().Add(time.Minute),
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	if authorization != "Bearer opaque-access-token" {
		t.Fatalf("authorization = %q", authorization)
	}
}

func TestBearerAuthorizationRejectsHeaderInjection(t *testing.T) {
	_, err := BearerAuthorization(Token{AccessToken: "token\r\nInjected: true"}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "line break") {
		t.Fatalf("expected line-break rejection, got %v", err)
	}
}
