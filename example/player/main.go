package main

import (
	"context"
	"flag"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hajimehoshi/oto/v2"
	"github.com/jvrs2812/go-sip/client"
	"github.com/jvrs2812/go-sip/media"
	"github.com/jvrs2812/go-sip/types"
)

type audioStream struct {
	data chan byte
}

func (stream *audioStream) Read(buffer []byte) (int, error) {
	for index := range buffer {
		value, ok := <-stream.data

		if !ok {
			return index, io.EOF
		}

		buffer[index] = value
	}

	return len(buffer), nil
}

func main() {
	host := flag.String("host", env("SIP_HOST", "127.0.0.1"), "SIP server host")
	port := flag.Int("port", 5060, "SIP server port")
	rtpPort := flag.Int("rtp-port", 6000, "local RTP port")
	username := flag.String("username", os.Getenv("SIP_USERNAME"), "SIP username")
	password := flag.String("password", os.Getenv("SIP_PASSWORD"), "SIP password")
	advertised := flag.String("advertised-address", os.Getenv("SIP_ADVERTISED_ADDRESS"), "address advertised in SIP and SDP")
	flag.Parse()

	if *username == "" {
		log.Fatal("set -username or SIP_USERNAME")
	}

	stream := &audioStream{data: make(chan byte, 32000)}
	audioContext, ready, err := oto.NewContext(8000, 1, 2)

	if err != nil {
		log.Fatal(err)
	}

	<-ready

	player := audioContext.NewPlayer(stream)
	player.Play()
	defer player.Close()

	sipClient, err := client.NewClient(client.Config{
		ServerHost:         *host,
		ServerPort:         *port,
		RTPPort:            *rtpPort,
		Username:           *username,
		Password:           *password,
		AdvertisedAddress:  *advertised,
		TransactionTimeout: 10 * time.Second,
	})

	if err != nil {
		log.Fatal(err)
	}

	defer sipClient.Close()

	sipClient.OnInviteReceived = func(current *client.Client, invite types.InviteData) {
		current.AcceptInvite(invite)
	}

	sipClient.OnAudioReceived = func(_ *client.Client, packet types.AudioData) {
		var pcm []byte

		switch packet.PayloadType {
		case 0:
			pcm = (media.PCMU{}).Decode(packet.Payload)

		case 8:
			pcm = (media.PCMA{}).Decode(packet.Payload)

		default:
			return
		}

		for _, value := range pcm {
			select {
			case stream.data <- value:
			default:
			}
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := sipClient.Register(ctx); err != nil {
		log.Fatal(err)
	}

	<-ctx.Done()
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}

	return fallback
}
