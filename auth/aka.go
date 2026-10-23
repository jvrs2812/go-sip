package auth

import (
	"context"
	"crypto/hmac"
	"crypto/md5" // #nosec G501 -- AKAv2-MD5 is the interoperable algorithm registered by RFC 4169.
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"hash"
	"strings"
)

var ErrInvalidAKAResult = errors.New("invalid AKA authentication result")

type AKAChallenge struct {
	Realm string
	Nonce string
}

type AKAResult struct {
	Response      []byte
	IntegrityKey  []byte
	CipherKey     []byte
	ResyncPayload []byte
}

type AKAProvider interface {
	AuthenticateAKA(context.Context, AKAChallenge) (AKAResult, error)
}

type AKAProviderFunc func(context.Context, AKAChallenge) (AKAResult, error)

func (f AKAProviderFunc) AuthenticateAKA(ctx context.Context, challenge AKAChallenge) (AKAResult, error) {
	return f(ctx, challenge)
}

type AKAv2Keys struct {
	Password     string
	IntegrityKey []byte
	CipherKey    []byte
}

func DeriveAKAv2(algorithm string, result AKAResult) (AKAv2Keys, error) {
	if len(result.Response) == 0 || len(result.IntegrityKey) == 0 || len(result.CipherKey) == 0 {
		return AKAv2Keys{}, ErrInvalidAKAResult
	}

	hashFunction, err := akaHash(algorithm)
	if err != nil {
		return AKAv2Keys{}, err
	}

	secret := make([]byte, 0, len(result.Response)+len(result.IntegrityKey)+len(result.CipherKey))
	secret = append(secret, result.Response...)
	secret = append(secret, result.IntegrityKey...)
	secret = append(secret, result.CipherKey...)

	password := akaPRF(hashFunction, secret, "http-digest-akav2-password")
	integrityKey := akaPRF(hashFunction, result.IntegrityKey, "http-digest-akav2-integritykey")
	cipherKey := akaPRF(hashFunction, result.CipherKey, "http-digest-akav2-cipherkey")

	return AKAv2Keys{
		Password:     base64.StdEncoding.EncodeToString(password),
		IntegrityKey: integrityKey,
		CipherKey:    cipherKey,
	}, nil
}

func IsAKAv2Algorithm(algorithm string) bool {
	return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(algorithm)), "AKAV2-")
}

func akaHash(algorithm string) (func() hash.Hash, error) {
	algorithm = strings.ToUpper(strings.TrimSpace(algorithm))
	algorithm = strings.TrimPrefix(algorithm, "AKAV2-")

	switch algorithm {
	case "MD5":
		return md5.New, nil // #nosec G401 -- required for AKAv2-MD5 interoperability.
	case "SHA-256":
		return sha256.New, nil
	default:
		return nil, fmt.Errorf("%w: AKAv2-%s", ErrUnsupportedAlgorithm, algorithm)
	}
}

func akaPRF(hashFunction func() hash.Hash, secret []byte, label string) []byte {
	mac := hmac.New(hashFunction, secret)
	_, _ = mac.Write([]byte(label))

	return mac.Sum(nil)
}
