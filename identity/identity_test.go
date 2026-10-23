package identity

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"github.com/jvrs2812/go-sip/sip"
)

func TestSignAndVerifyMessageIdentity(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Unix(2000000000, 0).UTC()
	certificate := testCertificate(t, privateKey, now)
	certificateURL := "https://cert.example.test/passport.cer"

	message := sip.NewRequest("INVITE", "sip:+12025550102@example.test")
	message.Headers.Add("From", "<sip:+12025550101@example.test>;tag=1")
	message.Headers.Add("To", "<sip:+12025550102@example.test>")

	signer := Signer{PrivateKey: privateKey, CertificateURL: certificateURL}
	claims := Claims{
		Origin:      "+12025550101",
		Destination: []string{"+12025550102"},
		IssuedAt:    now,
		OrigID:      "550e8400-e29b-41d4-a716-446655440000",
	}

	if err := signer.SignMessage(message, claims); err != nil {
		t.Fatal(err)
	}

	verified, err := VerifyMessage(context.Background(), message, VerifyOptions{
		Resolver: CertificateResolverFunc(func(_ context.Context, requestedURL string) (*x509.Certificate, error) {
			if requestedURL != certificateURL {
				t.Fatalf("requested certificate URL = %q", requestedURL)
			}

			return certificate, nil
		}),
		Now:                 now.Add(10 * time.Second),
		MaxAge:              time.Minute,
		ExpectedOrigin:      claims.Origin,
		ExpectedDestination: claims.Destination[0],
	})
	if err != nil {
		t.Fatal(err)
	}

	if verified.OrigID != claims.OrigID {
		t.Fatalf("verified claims = %#v", verified)
	}

	message.Headers.Set("Identity", message.Headers.Get("Identity")+"tampered")
	if _, err := VerifyMessage(context.Background(), message, VerifyOptions{Resolver: CertificateResolverFunc(func(context.Context, string) (*x509.Certificate, error) {
		return certificate, nil
	}), Now: now}); err == nil {
		t.Fatal("expected a tampered Identity header to fail")
	}
}

func testCertificate(t *testing.T, privateKey *ecdsa.PrivateKey, now time.Time) *x509.Certificate {
	t.Helper()

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "identity.test"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}

	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}

	return certificate
}
