package media

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/pion/dtls/v3"
	"github.com/pion/srtp/v3"
)

var (
	ErrInvalidDTLSConfig   = errors.New("invalid DTLS-SRTP configuration")
	ErrFingerprintMismatch = errors.New("DTLS certificate fingerprint mismatch")
	ErrSecureRTPClosed     = errors.New("secure RTP session is closed")
)

type DTLSRole string

const (
	DTLSClient DTLSRole = "client"
	DTLSServer DTLSRole = "server"
)

type DTLSIdentity struct {
	certificate tls.Certificate
	fingerprint string
}

type DTLSSRTPConfig struct {
	Identity          *DTLSIdentity
	RemoteFingerprint string
	Role              DTLSRole
	FlightInterval    time.Duration
}

type SecureRTP struct {
	connection net.PacketConn
	remote     net.Addr
	dtls       *dtls.Conn
	mux        *packetMux
	local      *srtp.Context
	remoteSRTP *srtp.Context

	writeMu   sync.Mutex
	readMu    sync.Mutex
	stateMu   sync.RWMutex
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

func NewDTLSIdentity() (*DTLSIdentity, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate DTLS private key: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate DTLS certificate serial: %w", err)
	}

	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      pkix.Name{CommonName: "go-sip ephemeral DTLS identity"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, fmt.Errorf("create DTLS certificate: %w", err)
	}

	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse DTLS certificate: %w", err)
	}

	certificate := tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  privateKey,
		Leaf:        leaf,
	}

	return &DTLSIdentity{
		certificate: certificate,
		fingerprint: certificateFingerprint(der),
	}, nil
}

func (i *DTLSIdentity) Fingerprint() string {
	if i == nil {
		return ""
	}

	return i.fingerprint
}

func EstablishDTLSSRTP(ctx context.Context, connection net.PacketConn, remote net.Addr, config DTLSSRTPConfig) (*SecureRTP, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if connection == nil || remote == nil || config.Identity == nil || config.RemoteFingerprint == "" {
		return nil, ErrInvalidDTLSConfig
	}

	verifyFingerprint, err := fingerprintVerifier(config.RemoteFingerprint)
	if err != nil {
		return nil, err
	}

	mux := newPacketMux(connection)
	fail := func(err error) (*SecureRTP, error) {
		_ = mux.Close()

		return nil, err
	}

	dtlsConfig := &dtls.Config{
		Certificates:           []tls.Certificate{config.Identity.certificate},
		SRTPProtectionProfiles: []dtls.SRTPProtectionProfile{dtls.SRTP_AES128_CM_HMAC_SHA1_80},
		InsecureSkipVerify:     true, // Authentication is performed by the SDP fingerprint callback below.
		VerifyPeerCertificate:  verifyFingerprint,
		FlightInterval:         config.FlightInterval,
	}

	var dtlsConnection *dtls.Conn

	switch config.Role {
	case DTLSClient:
		dtlsConnection, err = dtls.Client(mux.dtls, remote, dtlsConfig)
	case DTLSServer:
		dtlsConfig.ClientAuth = dtls.RequireAnyClientCert
		dtlsConnection, err = dtls.Server(mux.dtls, remote, dtlsConfig)
	default:
		return fail(fmt.Errorf("%w: role %q", ErrInvalidDTLSConfig, config.Role))
	}

	if err != nil {
		return fail(fmt.Errorf("create DTLS connection: %w", err))
	}

	if err := dtlsConnection.HandshakeContext(ctx); err != nil {
		_ = dtlsConnection.Close()

		return fail(fmt.Errorf("DTLS handshake: %w", err))
	}

	selectedProfile, ok := dtlsConnection.SelectedSRTPProtectionProfile()
	if !ok || selectedProfile != dtls.SRTP_AES128_CM_HMAC_SHA1_80 {
		_ = dtlsConnection.Close()

		return fail(fmt.Errorf("%w: unsupported negotiated SRTP profile", ErrInvalidDTLSConfig))
	}

	state, ok := dtlsConnection.ConnectionState()
	if !ok {
		_ = dtlsConnection.Close()

		return fail(fmt.Errorf("%w: DTLS state unavailable", ErrInvalidDTLSConfig))
	}

	srtpConfig := &srtp.Config{Profile: srtp.ProtectionProfileAes128CmHmacSha1_80}
	if err := srtpConfig.ExtractSessionKeysFromDTLS(&state, config.Role == DTLSClient); err != nil {
		_ = dtlsConnection.Close()

		return fail(fmt.Errorf("derive SRTP keys: %w", err))
	}

	localContext, err := srtp.CreateContext(
		srtpConfig.Keys.LocalMasterKey,
		srtpConfig.Keys.LocalMasterSalt,
		srtpConfig.Profile,
	)
	if err != nil {
		_ = dtlsConnection.Close()

		return fail(fmt.Errorf("create outbound SRTP context: %w", err))
	}

	remoteContext, err := srtp.CreateContext(
		srtpConfig.Keys.RemoteMasterKey,
		srtpConfig.Keys.RemoteMasterSalt,
		srtpConfig.Profile,
	)
	if err != nil {
		_ = dtlsConnection.Close()

		return fail(fmt.Errorf("create inbound SRTP context: %w", err))
	}

	return &SecureRTP{
		connection: mux.media,
		remote:     remote,
		dtls:       dtlsConnection,
		mux:        mux,
		local:      localContext,
		remoteSRTP: remoteContext,
	}, nil
}

func (s *SecureRTP) ProtectRTP(plaintext []byte) ([]byte, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	protected, err := s.local.EncryptRTP(nil, plaintext, nil)
	if err != nil {
		return nil, fmt.Errorf("protect RTP: %w", err)
	}

	return protected, nil
}

func (s *SecureRTP) UnprotectRTP(ciphertext []byte) ([]byte, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}

	s.readMu.Lock()
	defer s.readMu.Unlock()

	plaintext, err := s.remoteSRTP.DecryptRTP(nil, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("unprotect RTP: %w", err)
	}

	return plaintext, nil
}

func (s *SecureRTP) ProtectRTCP(plaintext []byte) ([]byte, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	protected, err := s.local.EncryptRTCP(nil, plaintext, nil)
	if err != nil {
		return nil, fmt.Errorf("protect RTCP: %w", err)
	}

	return protected, nil
}

func (s *SecureRTP) UnprotectRTCP(ciphertext []byte) ([]byte, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}

	s.readMu.Lock()
	defer s.readMu.Unlock()

	plaintext, err := s.remoteSRTP.DecryptRTCP(nil, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("unprotect RTCP: %w", err)
	}

	return plaintext, nil
}

func (s *SecureRTP) WriteRTP(packet []byte) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}

	protected, err := s.ProtectRTP(packet)
	if err != nil {
		return err
	}

	written, err := s.connection.WriteTo(protected, s.remote)
	if err != nil {
		return fmt.Errorf("write SRTP: %w", err)
	}

	if written != len(protected) {
		return io.ErrShortWrite
	}

	return nil
}

func (s *SecureRTP) ReadRTP(ctx context.Context, buffer []byte) ([]byte, net.Addr, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, nil, err
	}

	if len(buffer) == 0 {
		buffer = make([]byte, 64<<10)
	}

	if ctx == nil {
		ctx = context.Background()
	}

	stop := context.AfterFunc(ctx, func() {
		_ = s.connection.SetReadDeadline(time.Now())
	})
	defer stop()

	if deadline, ok := ctx.Deadline(); ok {
		if err := s.connection.SetReadDeadline(deadline); err != nil {
			return nil, nil, err
		}
	} else {
		if err := s.connection.SetReadDeadline(time.Time{}); err != nil {
			return nil, nil, err
		}
	}

	count, source, err := s.connection.ReadFrom(buffer)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, nil, ctxErr
		}

		return nil, nil, fmt.Errorf("read SRTP: %w", err)
	}

	plaintext, err := s.UnprotectRTP(buffer[:count])
	if err != nil {
		return nil, source, err
	}

	return plaintext, source, nil
}

func (s *SecureRTP) Close() error {
	s.closeOnce.Do(func() {
		s.stateMu.Lock()
		s.closed = true
		s.stateMu.Unlock()

		if s.dtls != nil {
			s.closeErr = s.dtls.Close()
		}

		if s.mux != nil {
			s.closeErr = errors.Join(s.closeErr, s.mux.Close())
		}
	})

	return s.closeErr
}

func (s *SecureRTP) ensureOpen() error {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()

	if s.closed {
		return ErrSecureRTPClosed
	}

	return nil
}

func fingerprintVerifier(expected string) (func([][]byte, [][]*x509.Certificate) error, error) {
	expectedBytes, err := parseFingerprint(expected)
	if err != nil {
		return nil, err
	}

	return func(rawCertificates [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCertificates) == 0 {
			return ErrFingerprintMismatch
		}

		actual := sha256.Sum256(rawCertificates[0])
		if subtle.ConstantTimeCompare(actual[:], expectedBytes) != 1 {
			return ErrFingerprintMismatch
		}

		return nil
	}, nil
}

func parseFingerprint(value string) ([]byte, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(value), ":", "")
	if len(normalized) != sha256.Size*2 {
		return nil, fmt.Errorf("%w: SHA-256 fingerprint", ErrInvalidDTLSConfig)
	}

	decoded, err := hex.DecodeString(normalized)
	if err != nil {
		return nil, fmt.Errorf("%w: SHA-256 fingerprint", ErrInvalidDTLSConfig)
	}

	return decoded, nil
}

func certificateFingerprint(certificate []byte) string {
	digest := sha256.Sum256(certificate)
	parts := make([]string, len(digest))

	for index, value := range digest {
		parts[index] = fmt.Sprintf("%02X", value)
	}

	return strings.Join(parts, ":")
}
