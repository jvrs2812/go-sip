package client

import (
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/jvrs2812/go-sip/internal"
)

type Client struct {
	IpServer   string
	PortServer int
	Ramal      string
	Password   string
}

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
	}

	log.Printf("Registering SIP with %v", register)

	if err := tcp.Send(internal.RegisterSip(register)); err != nil {
		log.Printf("Error to send register: %v", err)
	}

	response, err := tcp.ReadFullResponse(2 * time.Second)
	if err != nil {
		log.Printf("Error read response: %v", err)
		return
	}

	log.Printf("Search Realm And Nonce:\n%s", response)

	if strings.Contains(response, "401 Unauthorized") {
		authData := internal.ParseAuth(response)

		register.Cseq = 2
		register.Auth.Nonce = authData.Nonce
		register.Auth.Realm = authData.Realm
		register.Auth.Opaque = authData.Opaque

		tcp.Send(internal.RegisterSip(register))

		finalResponse, _ := tcp.ReadFullResponse(2 * time.Second)
		log.Printf("Receive Register Server: %s", finalResponse)
	}

}
