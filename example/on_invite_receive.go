package example

import (
	"log"

	"github.com/jvrs2812/go-sip/client"
	"github.com/jvrs2812/go-sip/internal"
)

func OnInviteReceived(inviteData internal.InviteData) {
	log.Printf("Invite received from IP: %s with ramal: %s", inviteData.IpReceived, inviteData.RamalReceived)
}

func InviteReceive() {
	c := client.Client{
		IpServer:         "sip.example.com",
		PortServer:       5060,
		PortLocal:        5060,
		Ramal:            "1001",
		Password:         "senha_segura",
		OnInviteReceived: OnInviteReceived,
	}

	c.WatchEvents()

	client.RegisterSip(c)
}
