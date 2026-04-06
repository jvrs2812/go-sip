package types

type InviteData struct {
	Via    string
	From   string
	To     string
	CallID string
	Cseq   string
}

type AudioData struct {
	Version     int
	PayloadType uint8
	SequenceNum uint16
	Timestamp   uint32
	SSRC        uint32
	Payload     []byte
	RemoteAddr  string
}
