package identity

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jvrs2812/go-sip/sip"
)

var (
	ErrInvalidIdentity   = errors.New("invalid SIP Identity")
	ErrIdentitySignature = errors.New("invalid SIP Identity signature")
	ErrIdentityExpired   = errors.New("SIP Identity is outside the accepted time window")
)

type Claims struct {
	Origin      string
	Destination []string
	IssuedAt    time.Time
	OrigID      string
}

type passportHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
	Profile   string `json:"ppt,omitempty"`
	X5U       string `json:"x5u"`
}

type passportIdentity struct {
	TelephoneNumber string `json:"tn"`
}

type passportDestinations struct {
	TelephoneNumbers []string `json:"tn"`
}

type passportClaims struct {
	Origin      passportIdentity     `json:"orig"`
	Destination passportDestinations `json:"dest"`
	IssuedAt    int64                `json:"iat"`
	OrigID      string               `json:"origid"`
}

type Signer struct {
	PrivateKey     *ecdsa.PrivateKey
	CertificateURL string
	Profile        string
}

type CertificateResolver interface {
	ResolveCertificate(context.Context, string) (*x509.Certificate, error)
}

type CertificateResolverFunc func(context.Context, string) (*x509.Certificate, error)

func (f CertificateResolverFunc) ResolveCertificate(ctx context.Context, certificateURL string) (*x509.Certificate, error) {
	return f(ctx, certificateURL)
}

type VerifyOptions struct {
	Resolver            CertificateResolver
	Now                 time.Time
	MaxAge              time.Duration
	ExpectedOrigin      string
	ExpectedDestination string
}

func (s Signer) IdentityHeader(claims Claims) (string, error) {
	if s.PrivateKey == nil || s.PrivateKey.Curve == nil || s.PrivateKey.Curve.Params().Name != "P-256" {
		return "", fmt.Errorf("%w: ES256 P-256 key is required", ErrInvalidIdentity)
	}

	if strings.TrimSpace(s.CertificateURL) == "" || strings.ContainsAny(s.CertificateURL, "\r\n<>") {
		return "", fmt.Errorf("%w: certificate URL", ErrInvalidIdentity)
	}

	if claims.Origin == "" || len(claims.Destination) == 0 || claims.OrigID == "" {
		return "", fmt.Errorf("%w: incomplete claims", ErrInvalidIdentity)
	}

	issuedAt := claims.IssuedAt
	if issuedAt.IsZero() {
		issuedAt = time.Now().UTC()
	}

	profile := s.Profile
	if profile == "" {
		profile = "shaken"
	}

	headerJSON, err := json.Marshal(passportHeader{
		Algorithm: "ES256",
		Type:      "passport",
		Profile:   profile,
		X5U:       s.CertificateURL,
	})
	if err != nil {
		return "", err
	}

	claimsJSON, err := json.Marshal(passportClaims{
		Origin:      passportIdentity{TelephoneNumber: claims.Origin},
		Destination: passportDestinations{TelephoneNumbers: append([]string(nil), claims.Destination...)},
		IssuedAt:    issuedAt.Unix(),
		OrigID:      claims.OrigID,
	})
	if err != nil {
		return "", err
	}

	encodedHeader := base64.RawURLEncoding.EncodeToString(headerJSON)
	encodedClaims := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := encodedHeader + "." + encodedClaims
	digest := sha256.Sum256([]byte(signingInput))

	r, signatureS, err := ecdsa.Sign(rand.Reader, s.PrivateKey, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign SIP Identity: %w", err)
	}

	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	signatureS.FillBytes(signature[32:])

	token := signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)

	return fmt.Sprintf("%s;info=<%s>;alg=ES256;ppt=%s", token, s.CertificateURL, profile), nil
}

func (s Signer) SignMessage(message *sip.Message, claims Claims) error {
	if message == nil {
		return fmt.Errorf("%w: nil SIP message", ErrInvalidIdentity)
	}

	header, err := s.IdentityHeader(claims)
	if err != nil {
		return err
	}

	message.Headers.Set("Identity", header)

	return nil
}

func VerifyMessage(ctx context.Context, message *sip.Message, options VerifyOptions) (Claims, error) {
	if message == nil || options.Resolver == nil {
		return Claims{}, fmt.Errorf("%w: message and certificate resolver are required", ErrInvalidIdentity)
	}

	identityValue := strings.TrimSpace(message.Headers.Get("Identity"))
	if identityValue == "" || strings.ContainsAny(identityValue, "\r\n") {
		return Claims{}, ErrInvalidIdentity
	}

	token, parameters := splitIdentityHeader(identityValue)
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, ErrInvalidIdentity
	}

	headerData, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Claims{}, ErrInvalidIdentity
	}

	claimsData, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrInvalidIdentity
	}

	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != 64 {
		return Claims{}, ErrInvalidIdentity
	}

	var header passportHeader
	if err := json.Unmarshal(headerData, &header); err != nil {
		return Claims{}, ErrInvalidIdentity
	}

	if header.Algorithm != "ES256" || header.Type != "passport" || header.X5U == "" {
		return Claims{}, ErrInvalidIdentity
	}

	if parameters["alg"] != "ES256" || parameters["info"] != header.X5U || parameters["ppt"] != header.Profile {
		return Claims{}, ErrInvalidIdentity
	}

	certificate, err := options.Resolver.ResolveCertificate(ctx, header.X5U)
	if err != nil {
		return Claims{}, fmt.Errorf("resolve SIP Identity certificate: %w", err)
	}

	now := options.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	if now.Before(certificate.NotBefore) || now.After(certificate.NotAfter) {
		return Claims{}, ErrIdentityExpired
	}

	publicKey, ok := certificate.PublicKey.(*ecdsa.PublicKey)
	if !ok || publicKey.Curve.Params().Name != "P-256" {
		return Claims{}, fmt.Errorf("%w: certificate key is not P-256", ErrInvalidIdentity)
	}

	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r := new(big.Int).SetBytes(signature[:32])
	signatureS := new(big.Int).SetBytes(signature[32:])

	if !ecdsa.Verify(publicKey, digest[:], r, signatureS) {
		return Claims{}, ErrIdentitySignature
	}

	var encodedClaims passportClaims
	if err := json.Unmarshal(claimsData, &encodedClaims); err != nil {
		return Claims{}, ErrInvalidIdentity
	}

	claims := Claims{
		Origin:      encodedClaims.Origin.TelephoneNumber,
		Destination: append([]string(nil), encodedClaims.Destination.TelephoneNumbers...),
		IssuedAt:    time.Unix(encodedClaims.IssuedAt, 0).UTC(),
		OrigID:      encodedClaims.OrigID,
	}

	maxAge := options.MaxAge
	if maxAge <= 0 {
		maxAge = time.Minute
	}

	if claims.IssuedAt.After(now.Add(maxAge)) || now.Sub(claims.IssuedAt) > maxAge {
		return Claims{}, ErrIdentityExpired
	}

	if options.ExpectedOrigin != "" && claims.Origin != options.ExpectedOrigin {
		return Claims{}, ErrInvalidIdentity
	}

	if options.ExpectedDestination != "" && !contains(claims.Destination, options.ExpectedDestination) {
		return Claims{}, ErrInvalidIdentity
	}

	return claims, nil
}

func splitIdentityHeader(value string) (string, map[string]string) {
	parts := strings.Split(value, ";")
	parameters := make(map[string]string, len(parts)-1)

	for _, rawParameter := range parts[1:] {
		name, parameterValue, found := strings.Cut(strings.TrimSpace(rawParameter), "=")
		if !found {
			continue
		}

		parameterValue = strings.TrimSpace(parameterValue)
		parameterValue = strings.TrimPrefix(parameterValue, "<")
		parameterValue = strings.TrimSuffix(parameterValue, ">")
		parameters[strings.ToLower(strings.TrimSpace(name))] = parameterValue
	}

	return strings.TrimSpace(parts[0]), parameters
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}

	return false
}
