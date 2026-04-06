package example

import (
	"log"

	"github.com/jvrs2812/go-sip/client"
	"github.com/jvrs2812/go-sip/types"
)

func OnAudioReceivedLog(data types.AudioData) {
	log.Printf("Audio Received Bytes: %d, PayloadType: %d", len(data.Payload), data.PayloadType)
}

func OnInviteReceived(c *client.Client, inviteData types.InviteData) {
	log.Printf("Invite received from: %s", inviteData.From)
	c.AcceptInvite(inviteData)
}

func AcceptCall() {

	c := client.Client{
		IpServer:         "sip.example.com",
		PortServer:       5060,
		Ramal:            "1001",
		Password:         "senha_segura",
		OnInviteReceived: OnInviteReceived,
		PortForRtp:       6000,
		OnAudioReceived:  OnAudioReceivedLog,
	}

	c.WatchEvents()

	client.RegisterSip(c)

}
