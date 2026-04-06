package client

import (
	"context"
	"log"
	"strconv"
	"strings"

	"github.com/jvrs2812/go-sip/internal"
	"github.com/jvrs2812/go-sip/types"
)

type Client struct {
	IpServer          string
	PortServer        int
	PortForRtp        int
	Ramal             string
	Password          string
	OnInviteReceived  func(c *Client, inviteData types.InviteData)
	OnAudioReceived   func(c *Client, data types.AudioData)
	cancelRtpListener context.CancelFunc
}

type OnInviteReceived func(c *Client, inviteData types.InviteData)

func RegisterSip(client Client) {

	tcp := internal.GetTCP(client.IpServer + ":" + strconv.Itoa(client.PortServer))

	ipLocal, err := internal.GetInterfaceIP()

	if err != nil {
		panic(err)
	}

	register := internal.Register{
		IpLocal:  ipLocal,
		IpServer: client.IpServer,
		Ramal:    client.Ramal,
		Cseq:     1,
		Auth: &internal.SipAuth{
			Password: client.Password,
			Nonce:    "",
			Realm:    "",
			Opaque:   "",
		},
		PortServer: client.PortServer,
	}

	log.Printf("Registering SIP with %v", register)

	if err := tcp.Send(internal.RegisterSip(register)); err != nil {
		log.Printf("Error to send register: %v", err)
	}

	response := <-tcp.OnMessage
	log.Printf("Response received: %s", response)
}

func (c *Client) HandleAuth(response401 string) {
	tcp := internal.GetTCP(c.IpServer + ":" + strconv.Itoa(c.PortServer))
	ipLocal, _ := internal.GetInterfaceIP()

	authData := internal.ParseAuth(response401)

	reg := internal.Register{
		IpLocal:    ipLocal,
		IpServer:   c.IpServer,
		Ramal:      c.Ramal,
		Cseq:       2,
		PortServer: c.PortServer,
		Auth: &internal.SipAuth{
			Password: c.Password,
			Nonce:    authData.Nonce,
			Realm:    authData.Realm,
			Opaque:   authData.Opaque,
		},
	}

	log.Printf("[SIP] Sending REGISTER with Hash MD5 (CSeq 2)...")
	tcp.Send(internal.RegisterSip(reg))
}

func (c *Client) AcceptInvite(inviteData types.InviteData) {
	ctx, cancel := context.WithCancel(context.Background())
	c.cancelRtpListener = cancel

	go internal.StartRTPListener(ctx, c.PortForRtp, c.OnAudioReceived)

	log.Println("[Client] Accepting INVITE...")
	tcp := internal.GetTCP(c.IpServer + ":" + strconv.Itoa(c.PortServer))

	ipLocal, _ := internal.GetInterfaceIP()

	accept := internal.Build200OKInvite(inviteData, ipLocal, c.PortForRtp)

	tcp.Send(accept)

}

func (c *Client) SendAudio(payload []byte, source types.AudioData) {
	internal.SendRTP(payload, source)
}

func (c *Client) WatchEvents() {
	tcp := internal.GetTCP(c.IpServer + ":" + strconv.Itoa(c.PortServer))
	tcp.StartDispatcher()

	go func() {
		log.Println("[Client] Watch Event Start...")
		for msg := range tcp.OnMessage {
			if strings.TrimSpace(msg) == "" {
				continue
			}

			log.Println("[WatchEvents] Message received:", msg)

			if strings.Contains(msg, "401 Unauthorized") {
				log.Println("[WatchEvents] Receive 401. ReAuthenticating...")
				c.HandleAuth(msg)
				continue
			}

			if strings.Contains(msg, "200 OK") && strings.Contains(msg, "CSeq: 2 REGISTER") {
				log.Println("[WatchEvents] Registered successfully with authentication!")
				continue
			}

			if strings.Contains(msg, "INVITE") {
				log.Println("[WatchEvents] INVITE RECEIVED !!!!")
				log.Printf("Data of INVITE: %s", msg)
				ringingPacket := internal.Build180Ringing(msg)
				if ringingPacket != nil {
					tcp.Send(ringingPacket)
					log.Println("[WatchEvents] 180 Ringing send successfully!")
				}
				inviteData, err := internal.ParseInviteIP(msg)
				if err != nil {
					log.Printf("[WatchEvents] Error parsing INVITE IP: %v", err)
					continue
				}

				if c.OnInviteReceived != nil {
					c.OnInviteReceived(c, inviteData)
				}

				continue
			}

			if strings.Contains(msg, "OPTIONS") {
				log.Println("[WatchEvents] Response Keep-alive (OPTIONS)")
				response := internal.Build200OK(msg)

				if err := tcp.Send(response); err != nil {
					log.Printf("[WatchEvents] Error To Sending OPTIONS: %v", err)
				} else {
					log.Println("[WatchEvents] 200 OK Sending OPTIONS!")
				}

				continue
			}

			if strings.Contains(msg, "BYE") {
				if c.cancelRtpListener != nil {
					c.cancelRtpListener()
				}
				log.Println("[WatchEvents] Call ended (BYE received)")
				continue
			}
		}
	}()
}
