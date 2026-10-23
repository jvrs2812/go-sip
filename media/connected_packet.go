package media

import (
	"errors"
	"net"
	"time"
)

type connectedPacketConn struct {
	connection net.Conn
}

func NewConnectedPacketConn(connection net.Conn) (net.PacketConn, error) {
	if connection == nil {
		return nil, errors.New("connected packet adapter requires a connection")
	}

	return &connectedPacketConn{connection: connection}, nil
}

func (c *connectedPacketConn) ReadFrom(buffer []byte) (int, net.Addr, error) {
	count, err := c.connection.Read(buffer)

	return count, c.connection.RemoteAddr(), err
}

func (c *connectedPacketConn) WriteTo(buffer []byte, address net.Addr) (int, error) {
	if address != nil && c.connection.RemoteAddr() != nil && address.String() != c.connection.RemoteAddr().String() {
		return 0, errors.New("connected packet adapter cannot write to a different peer")
	}

	return c.connection.Write(buffer)
}

func (c *connectedPacketConn) Close() error {
	return c.connection.Close()
}

func (c *connectedPacketConn) LocalAddr() net.Addr {
	return c.connection.LocalAddr()
}

func (c *connectedPacketConn) SetDeadline(deadline time.Time) error {
	return c.connection.SetDeadline(deadline)
}

func (c *connectedPacketConn) SetReadDeadline(deadline time.Time) error {
	return c.connection.SetReadDeadline(deadline)
}

func (c *connectedPacketConn) SetWriteDeadline(deadline time.Time) error {
	return c.connection.SetWriteDeadline(deadline)
}
