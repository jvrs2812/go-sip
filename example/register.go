package example

import (
	"github.com/jvrs2812/go-sip/client"
)

func RegisterSipExample() {
	c := client.Client{
		IpServer:   "sip.example.com",
		PortServer: 5060,
		Ramal:      "1001",
		Password:   "senha_segura",
	}

	client.RegisterSip(c)
}
