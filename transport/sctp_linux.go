//go:build linux && !386

package transport

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/ishidawataru/sctp"
)

type sctpDialResult struct {
	connection *sctp.SCTPConn
	err        error
}

type sctpConnection struct {
	*sctp.SCTPConn
}

func (sctpConnection) SetDeadline(time.Time) error {
	return nil
}

func (sctpConnection) SetReadDeadline(time.Time) error {
	return nil
}

func (sctpConnection) SetWriteDeadline(time.Time) error {
	return nil
}

var _ net.Conn = sctpConnection{}

func dialSCTP(ctx context.Context, config Config) (Transport, error) {
	remoteAddress, err := sctp.ResolveSCTPAddr("sctp", config.RemoteAddr)
	if err != nil {
		return nil, fmt.Errorf("resolve remote SCTP address: %w", err)
	}

	var localAddress *sctp.SCTPAddr

	if config.LocalAddr != "" {
		localAddress, err = sctp.ResolveSCTPAddr("sctp", config.LocalAddr)
		if err != nil {
			return nil, fmt.Errorf("resolve local SCTP address: %w", err)
		}
	}

	timeout := config.DialTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	timeoutMilliseconds := timeout.Milliseconds()
	if timeoutMilliseconds < 1 {
		timeoutMilliseconds = 1
	}

	if timeoutMilliseconds > 65535 {
		timeoutMilliseconds = 65535
	}

	result := make(chan sctpDialResult, 1)

	go func() {
		connection, dialErr := sctp.DialSCTPExt("sctp", localAddress, remoteAddress, sctp.InitMsg{
			NumOstreams:    1,
			MaxInstreams:   1,
			MaxAttempts:    1,
			MaxInitTimeout: uint16(timeoutMilliseconds),
		})

		select {
		case result <- sctpDialResult{connection: connection, err: dialErr}:
		case <-ctx.Done():
			if connection != nil {
				_ = connection.Close()
			}
		}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case dialResult := <-result:
		if dialResult.err != nil {
			return nil, fmt.Errorf("dial SCTP: %w", dialResult.err)
		}

		return newConnection(sctpConnection{SCTPConn: dialResult.connection}, true, config), nil
	}
}
