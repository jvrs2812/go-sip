package internal

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type tcpConnection struct {
	addr      string
	conn      net.Conn
	mu        sync.Mutex
	OnMessage chan string
}

var (
	connections = make(map[string]*tcpConnection)
	mapMu       sync.Mutex
)

func (t *tcpConnection) ReadFullResponse(timeout time.Duration) (string, error) {
	conn, err := t.getConn()
	if err != nil {
		return "", err
	}

	//conn.SetReadDeadline(time.Now().Add(timeout))

	fullResponse := ""
	tmp := make([]byte, 2048)

	for {
		n, err := conn.Read(tmp)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				return fullResponse, fmt.Errorf("timeout receive response SIP")
			}
			return fullResponse, err
		}

		fullResponse += string(tmp[:n])

		if byteCount := len(fullResponse); byteCount >= 4 && fullResponse[byteCount-4:] == "\r\n\r\n" {
			break
		}

		if len(fullResponse) > 8192 {
			break
		}
	}

	log.Printf("[tcpConnection] Response received (%d bytes)", len(fullResponse))
	return fullResponse, nil
}

func GetTCP(addr string) *tcpConnection {
	mapMu.Lock()
	defer mapMu.Unlock()

	if conn, ok := connections[addr]; ok {
		return conn
	}

	tc := &tcpConnection{
		addr:      addr,
		OnMessage: make(chan string, 100),
	}
	connections[addr] = tc
	log.Printf("[tcpConnection] Created singleton for %s", addr)
	return tc
}

func (t *tcpConnection) getConn() (net.Conn, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.conn != nil {
		return t.conn, nil
	}

	var err error
	for i := 1; i <= 3; i++ {
		log.Printf("[tcpConnection] Trying connection (%d/3) to %s...", i, t.addr)
		t.conn, err = net.Dial("tcp", t.addr)
		if err == nil {
			log.Printf("[tcpConnection] Connection established with %s", t.addr)
			return t.conn, nil
		}
		log.Printf("[tcpConnection] Error connecting: %v. Trying again in %d seconds...", err, i)
		time.Sleep(time.Duration(i) * time.Second)
	}

	return nil, fmt.Errorf("failed to connect after 3 attempts: %w", err)
}

func (t *tcpConnection) Send(data []byte) error {
	conn, err := t.getConn()
	if err != nil {
		log.Printf("[tcpConnection] Error obtaining connection: %v", err)
		return err
	}

	_, err = conn.Write(data)
	if err != nil {
		log.Printf("[tcpConnection] Error sending data: %v. reconnecting...", err)
		t.mu.Lock()
		if t.conn != nil {
			t.conn.Close()
			t.conn = nil
		}
		t.mu.Unlock()
		return err
	}

	log.Printf("[tcpConnection] Data sent successfully (%d bytes)", len(data))
	return nil
}

func (t *tcpConnection) StartDispatcher() {
	go func() {
		log.Printf("[Dispatcher] Iniciado para %s", t.addr)
		for {
			conn, err := t.getConn()
			if err != nil {
				log.Printf("[Dispatcher] Error connecting: %v. Trying in 2s...", err)
				time.Sleep(2 * time.Second)
				continue
			}
			reader := bufio.NewReader(conn)

			for {
				msg, err := t.parseSipMessage(reader)
				if err != nil {
					log.Printf("[Dispatcher] Connection Lost: %v", err)
					t.mu.Lock()
					if t.conn != nil {
						t.conn.Close()
						t.conn = nil
					}
					t.mu.Unlock()
					break
				}

				if msg != "" {
					t.OnMessage <- msg
				}
			}
		}
	}()
}

func (t *tcpConnection) parseSipMessage(reader *bufio.Reader) (string, error) {
	header := ""
	contentLength := 0

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}

		header += line

		if strings.Contains(strings.ToLower(line), "content-length:") {
			parts := strings.Split(line, ":")
			if len(parts) > 1 {
				val := strings.TrimSpace(parts[1])
				contentLength, _ = strconv.Atoi(val)
			}
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}

	body := ""
	if contentLength > 0 {
		buf := make([]byte, contentLength)
		_, err := io.ReadFull(reader, buf)
		if err != nil {
			return header, err
		}
		body = string(buf)
	}

	return header + body, nil
}
