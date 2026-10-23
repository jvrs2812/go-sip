package transport

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"github.com/jvrs2812/go-sip/sip"
)

func TestTLSTransportValidatesCertificateAndExchangesSIP(t *testing.T) {
	certificate, leaf := newTestTLSCertificate(t, "sip.example.test")
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{certificate},
	})
	if err != nil {
		t.Fatal(err)
	}

	defer listener.Close()

	serverErrors := make(chan error, 1)

	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErrors <- acceptErr

			return
		}

		defer connection.Close()

		request, parseErr := sip.ParseReader(bufio.NewReader(connection), sip.DefaultLimits())
		if parseErr != nil {
			serverErrors <- parseErr

			return
		}

		response := sip.NewResponse(200, "OK")
		response.Headers.Add("Call-ID", request.Headers.Get("Call-ID"))
		response.Headers.Add("CSeq", request.Headers.Get("CSeq"))
		payload, marshalErr := response.MarshalBinary()
		if marshalErr != nil {
			serverErrors <- marshalErr

			return
		}

		_, writeErr := connection.Write(payload)
		serverErrors <- writeErr
	}()

	roots := x509.NewCertPool()
	roots.AddCert(leaf)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	connection, err := Dial(ctx, Config{
		Network:    TLS,
		RemoteAddr: listener.Addr().String(),
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: "sip.example.test",
			RootCAs:    roots,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	defer connection.Close()

	request := sip.NewRequest("OPTIONS", "sips:sip.example.test")
	request.Headers.Add("Call-ID", "tls-test")
	request.Headers.Add("CSeq", "1 OPTIONS")

	if err := connection.Send(ctx, request); err != nil {
		t.Fatal(err)
	}

	select {
	case response := <-connection.Messages():
		if response == nil || response.StatusCode != 200 {
			t.Fatalf("unexpected TLS SIP response: %#v", response)
		}
	case err := <-connection.Errors():
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("timed out waiting for TLS SIP response")
	}

	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestTLSTransportRejectsWrongHostname(t *testing.T) {
	certificate, leaf := newTestTLSCertificate(t, "sip.example.test")
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{certificate},
	})
	if err != nil {
		t.Fatal(err)
	}

	defer listener.Close()

	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			defer connection.Close()
			_, _ = connection.Read(make([]byte, 1))
		}
	}()

	roots := x509.NewCertPool()
	roots.AddCert(leaf)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err = Dial(ctx, Config{
		Network:    TLS,
		RemoteAddr: listener.Addr().String(),
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: "wrong.example.test",
			RootCAs:    roots,
		},
	})
	if err == nil {
		t.Fatal("expected hostname validation to fail")
	}
}

func newTestTLSCertificate(t *testing.T, serverName string) (tls.Certificate, *x509.Certificate) {
	t.Helper()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: serverName},
		DNSNames:     []string{serverName},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}

	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}

	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  privateKey,
		Leaf:        leaf,
	}, leaf
}
