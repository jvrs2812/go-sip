package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jvrs2812/go-sip/client"
	"github.com/jvrs2812/go-sip/types"
)

func main() {
	host := flag.String("host", env("SIP_HOST", "127.0.0.1"), "SIP server host")
	port := flag.Int("port", 5060, "SIP server port")
	rtpPort := flag.Int("rtp-port", 6000, "local RTP port; use 0 for an ephemeral port")
	username := flag.String("username", os.Getenv("SIP_USERNAME"), "SIP username")
	password := flag.String("password", os.Getenv("SIP_PASSWORD"), "SIP password")
	advertised := flag.String("advertised-address", os.Getenv("SIP_ADVERTISED_ADDRESS"), "address advertised in SIP and SDP")
	flag.Parse()

	if *username == "" {
		log.Fatal("set -username or SIP_USERNAME")
	}

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
		log.Printf("incoming call from %s", invite.From)
		current.AcceptInvite(invite)
	}

	sipClient.OnStartCall = func(_ *client.Client, packet types.AudioData) {
		log.Printf("first RTP packet from %s using payload type %d", packet.RemoteAddr, packet.PayloadType)
	}

	sipClient.OnAudioReceived = func(_ *client.Client, packet types.AudioData) {
		log.Printf("RTP sequence=%d bytes=%d", packet.SequenceNum, len(packet.Payload))
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
