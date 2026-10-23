package types

import "github.com/jvrs2812/go-sip/sip"

type InviteData struct {
	Via      string
	From     string
	To       string
	CallID   string
	Cseq     string
	Contact  string
	Body     []byte
	Request  *sip.Message
	LocalTag string
}

type AudioData struct {
	Version     int
	PayloadType uint8
	SequenceNum uint16
	Timestamp   uint32
	SSRC        uint32
	Payload     []byte
	RemoteAddr  string
	Marker      bool
}
