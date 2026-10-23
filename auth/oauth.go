package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrInvalidBearerChallenge = errors.New("invalid Bearer challenge")

type BearerChallenge struct {
	Realm               string
	Scope               string
	AuthorizationServer string
	Error               string
}

type Token struct {
	AccessToken string
	ExpiresAt   time.Time
}

type TokenProvider interface {
	Token(context.Context, BearerChallenge) (Token, error)
}

type TokenProviderFunc func(context.Context, BearerChallenge) (Token, error)

func (f TokenProviderFunc) Token(ctx context.Context, challenge BearerChallenge) (Token, error) {
	return f(ctx, challenge)
}

func ParseBearerChallenge(value string) (BearerChallenge, error) {
	value = strings.TrimSpace(value)
	if len(value) < len("Bearer") || !strings.EqualFold(value[:len("Bearer")], "Bearer") {
		return BearerChallenge{}, ErrInvalidBearerChallenge
	}

	parameters, err := parseParameters(strings.TrimSpace(value[len("Bearer"):]))
	if err != nil {
		return BearerChallenge{}, fmt.Errorf("%w: %v", ErrInvalidBearerChallenge, err)
	}

	challenge := BearerChallenge{
		Realm:               parameters["realm"],
		Scope:               parameters["scope"],
		AuthorizationServer: parameters["auth_server"],
		Error:               parameters["error"],
	}

	return challenge, nil
}

func BearerAuthorization(token Token, now time.Time) (string, error) {
	if strings.TrimSpace(token.AccessToken) == "" {
		return "", errors.New("OAuth token is empty")
	}

	if strings.ContainsAny(token.AccessToken, "\r\n") {
		return "", errors.New("OAuth token contains a line break")
	}

	if !token.ExpiresAt.IsZero() && !token.ExpiresAt.After(now) {
		return "", errors.New("OAuth token is expired")
	}

	return "Bearer " + token.AccessToken, nil
}
