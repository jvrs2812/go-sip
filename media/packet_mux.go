package media

import (
	"net"
	"os"
	"sync"
	"time"
)

type muxPacket struct {
	data   []byte
	source net.Addr
}

type packetMux struct {
	connection net.PacketConn
	dtls       *muxPacketConn
	media      *muxPacketConn

	writeMu   sync.Mutex
	stateMu   sync.RWMutex
	readError error
	done      chan struct{}
	doneOnce  sync.Once
	closeOnce sync.Once
}

type muxPacketConn struct {
	mux     *packetMux
	packets chan muxPacket

	deadlineMu sync.RWMutex
	deadline   time.Time
	updated    chan struct{}
}

func newPacketMux(connection net.PacketConn) *packetMux {
	mux := &packetMux{
		connection: connection,
		done:       make(chan struct{}),
	}

	mux.dtls = &muxPacketConn{
		mux:     mux,
		packets: make(chan muxPacket, 128),
		updated: make(chan struct{}, 1),
	}
	mux.media = &muxPacketConn{
		mux:     mux,
		packets: make(chan muxPacket, 128),
		updated: make(chan struct{}, 1),
	}

	go mux.readLoop()

	return mux
}

func (m *packetMux) readLoop() {
	buffer := make([]byte, 64<<10)

	for {
		count, source, err := m.connection.ReadFrom(buffer)
		if err != nil {
			m.stateMu.Lock()
			m.readError = err
			m.stateMu.Unlock()
			m.signalDone()

			return
		}

		if count == 0 {
			continue
		}

		packet := muxPacket{
			data:   append([]byte(nil), buffer[:count]...),
			source: source,
		}

		destination := m.media.packets
		if packet.data[0] >= 20 && packet.data[0] <= 63 {
			destination = m.dtls.packets
		}

		select {
		case destination <- packet:
		case <-m.done:
			return
		}
	}
}

func (m *packetMux) Close() error {
	var closeErr error

	m.closeOnce.Do(func() {
		m.signalDone()
		closeErr = m.connection.Close()
	})

	return closeErr
}

func (m *packetMux) signalDone() {
	m.doneOnce.Do(func() {
		close(m.done)
	})
}

func (m *packetMux) error() error {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()

	if m.readError != nil {
		return m.readError
	}

	return net.ErrClosed
}

func (c *muxPacketConn) ReadFrom(buffer []byte) (int, net.Addr, error) {
	for {
		deadline := c.readDeadline()
		var timer *time.Timer
		var timeout <-chan time.Time

		if !deadline.IsZero() {
			delay := time.Until(deadline)
			if delay <= 0 {
				return 0, nil, os.ErrDeadlineExceeded
			}

			timer = time.NewTimer(delay)
			timeout = timer.C
		}

		select {
		case packet := <-c.packets:
			if timer != nil {
				timer.Stop()
			}

			count := copy(buffer, packet.data)

			return count, packet.source, nil
		case <-c.updated:
			if timer != nil {
				timer.Stop()
			}

			continue
		case <-timeout:
			return 0, nil, os.ErrDeadlineExceeded
		case <-c.mux.done:
			if timer != nil {
				timer.Stop()
			}

			return 0, nil, c.mux.error()
		}
	}
}

func (c *muxPacketConn) WriteTo(buffer []byte, address net.Addr) (int, error) {
	c.mux.writeMu.Lock()
	defer c.mux.writeMu.Unlock()

	select {
	case <-c.mux.done:
		return 0, c.mux.error()
	default:
	}

	return c.mux.connection.WriteTo(buffer, address)
}

func (c *muxPacketConn) Close() error {
	return nil
}

func (c *muxPacketConn) LocalAddr() net.Addr {
	return c.mux.connection.LocalAddr()
}

func (c *muxPacketConn) SetDeadline(deadline time.Time) error {
	if err := c.SetReadDeadline(deadline); err != nil {
		return err
	}

	return c.SetWriteDeadline(deadline)
}

func (c *muxPacketConn) SetReadDeadline(deadline time.Time) error {
	c.deadlineMu.Lock()
	c.deadline = deadline
	c.deadlineMu.Unlock()

	select {
	case c.updated <- struct{}{}:
	default:
	}

	return nil
}

func (c *muxPacketConn) SetWriteDeadline(deadline time.Time) error {
	return c.mux.connection.SetWriteDeadline(deadline)
}

func (c *muxPacketConn) readDeadline() time.Time {
	c.deadlineMu.RLock()
	defer c.deadlineMu.RUnlock()

	return c.deadline
}
