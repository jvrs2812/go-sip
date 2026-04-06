package internal

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/jvrs2812/go-sip/types"
)

type OnAudioReceived func(client interface{}, data types.AudioData)

var (
	rtpConn      net.PacketConn
	outSeq       uint16
	outTimestamp uint32
	sendMu       sync.Mutex
)

func StartRTPListener(ctx context.Context, port int, owner interface{}, callback OnAudioReceived) {
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		log.Printf("[RTP Error] Error to open Port %d: %v\n", port, err)
		return
	}
	defer conn.Close()

	log.Printf("[RTP] Listening in port %d.", port)

	buffer := make([]byte, 2048)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[RTP] Closing listener for port %d...\n", port)
			return
		default:
			conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))

			n, remoteAddr, err := conn.ReadFrom(buffer)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				return
			}

			if n >= 12 {
				version := (buffer[0] >> 6) & 0x03
				payloadType := buffer[1] & 0x7F
				seqNum := binary.BigEndian.Uint16(buffer[2:4])
				timestamp := binary.BigEndian.Uint32(buffer[4:8])
				ssrc := binary.BigEndian.Uint32(buffer[8:12])

				payload := make([]byte, n-12)
				copy(payload, buffer[12:n])

				data := types.AudioData{
					Version:     int(version),
					PayloadType: payloadType,
					SequenceNum: seqNum,
					Timestamp:   timestamp,
					SSRC:        ssrc,
					Payload:     payload,
					RemoteAddr:  remoteAddr.String(),
				}

				if callback != nil {
					callback(owner, data)
				}
			}
		}
	}
}

func SendRTP(payload []byte, source types.AudioData) {
	sendMu.Lock()
	defer sendMu.Unlock()

	if rtpConn == nil {
		return
	}

	log.Printf("[SendRTP] Send audio for %s", source.RemoteAddr)

	remoteAddr, err := net.ResolveUDPAddr("udp", source.RemoteAddr)
	if err != nil {
		log.Printf("[RTP Send] Erro ao resolver endereço: %v", err)
		return
	}

	header := make([]byte, 12)
	header[0] = 0x80
	header[1] = byte(source.PayloadType & 0x7F)
	binary.BigEndian.PutUint16(header[2:4], outSeq)
	binary.BigEndian.PutUint32(header[4:8], outTimestamp)
	binary.BigEndian.PutUint32(header[8:12], source.SSRC)

	packet := append(header, payload...)

	_, err = rtpConn.WriteTo(packet, remoteAddr)
	if err != nil {
		log.Printf("[RTP Send] Error to send: %v", err)
	}

	outSeq++
	outTimestamp += uint32(len(payload))
}
