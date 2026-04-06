package example

import (
	"log"

	"github.com/jvrs2812/go-sip/client"
	"github.com/jvrs2812/go-sip/internal"
)

func onInviteReceivedExample(c *client.Client, inviteData internal.InviteData) {
	log.Printf("Invite received from %s", inviteData.From)
}

func InviteReceive() {
	c := client.Client{
		IpServer:         "sip.example.com",
		PortServer:       5060,
		PortLocal:        5060,
		Ramal:            "1001",
		Password:         "senha_segura",
		OnInviteReceived: onInviteReceivedExample,
	}

	c.WatchEvents()

	client.RegisterSip(c)
}
