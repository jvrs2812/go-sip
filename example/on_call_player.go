package example

import (
	"log"

	"github.com/hajimehoshi/oto/v2"
	"github.com/jvrs2812/go-sip/client"
	"github.com/jvrs2812/go-sip/types"
	"github.com/zaf/g711"
)

type AudioStream struct {
	data chan byte
}

func (s *AudioStream) Read(p []byte) (n int, err error) {
	for i := 0; i < len(p); i++ {
		p[i] = <-s.data
	}
	return len(p), nil
}

var (
	stream *AudioStream
	player oto.Player
)

func InitAudioDevice() {
	stream = &AudioStream{data: make(chan byte, 8000*2)}

	ctx, ready, err := oto.NewContext(8000, 1, 2)
	if err != nil {
		log.Fatal(err)
	}
	<-ready

	player = ctx.NewPlayer(stream)
	player.Play()
}

func OnAudioReceived(c *client.Client, data types.AudioData) {
	var pcm []byte

	switch data.PayloadType {
	case 0:
		pcm = g711.DecodeUlaw(data.Payload)
	case 8:
		pcm = g711.DecodeAlaw(data.Payload)
	default:
		return
	}
	for _, b := range pcm {
		select {
		case stream.data <- b:
		default:
		}
	}
}

func OnInviteReceivedAudio(c *client.Client, inviteData types.InviteData) {
	log.Printf("Invite received from: %s", inviteData.From)
	InitAudioDevice()
	c.AcceptInvite(inviteData)
}

func main() {
	c := client.Client{
		IpServer:         "sip.example.com",
		PortServer:       5060,
		Ramal:            "1001",
		Password:         "senha_segura",
		OnInviteReceived: OnInviteReceivedAudio,
		OnAudioReceived:  OnAudioReceived,
		PortForRtp:       6000,
	}

	c.WatchEvents()

	client.RegisterSip(c)

	select {}
}
