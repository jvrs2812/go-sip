package internal

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
)

type InviteData struct {
	Via    string
	From   string
	To     string
	CallID string
	Cseq   string
}

type Register struct {
	IpLocal   string
	IpServer  string
	Ramal     string
	Cseq      int
	Auth      *SipAuth
	PortLocal int
}

type SipAuth struct {
	Nonce    string
	Realm    string
	Opaque   string
	Password string
}

func RegisterSip(r Register) []byte {

	uri := fmt.Sprintf("sip:%s", r.IpServer)
	authHeader := ""

	if r.Auth != nil && r.Auth.Password != "" && r.Auth.Nonce != "" {
		response := CalculateResponse(r.Ramal, r.Auth.Realm, r.Auth.Password, "REGISTER", uri, r.Auth.Nonce)

		authHeader = fmt.Sprintf(
			"Authorization: Digest username=\"%s\", realm=\"%s\", nonce=\"%s\", uri=\"%s\", response=\"%s\", opaque=\"%s\", algorithm=MD5\r\n",
			r.Ramal, r.Auth.Realm, r.Auth.Nonce, uri, response, r.Auth.Opaque,
		)
	}

	packet := fmt.Sprintf(
		"REGISTER %s SIP/2.0\r\n"+
			"Via: SIP/2.0/TCP %s:5060;branch=z9hG4bK%d\r\n"+
			"From: <sip:%s@%s>;tag=%d\r\n"+
			"To: <sip:%s@%s>\r\n"+
			"Call-ID: %d@%s\r\n"+
			"CSeq: %d REGISTER\r\n"+
			"Contact: <sip:%s@%s:%d;transport=tcp>\r\n"+
			"%s"+
			"Max-Forwards: 70\r\n"+
			"Expires: 3600\r\n"+
			"Content-Length: 0\r\n\r\n",
		uri,
		r.IpLocal, time.Now().Unix(),
		r.Ramal, r.IpServer, time.Now().Unix(),
		r.Ramal, r.IpServer,
		time.Now().Unix(), r.IpLocal,
		r.Cseq,
		r.Ramal, r.IpLocal, r.PortLocal,
		authHeader,
	)

	return []byte(packet)
}

func ParseAuth(response string) *SipAuth {
	auth := &SipAuth{}

	nonceReg := regexp.MustCompile(`nonce="([^"]+)"`)
	realmReg := regexp.MustCompile(`realm="([^"]+)"`)
	opaqueReg := regexp.MustCompile(`opaque="([^"]+)"`)

	if match := nonceReg.FindStringSubmatch(response); len(match) > 1 {
		auth.Nonce = match[1]
	}
	if match := realmReg.FindStringSubmatch(response); len(match) > 1 {
		auth.Realm = match[1]
	}
	if match := opaqueReg.FindStringSubmatch(response); len(match) > 1 {
		auth.Opaque = match[1]
	}

	return auth
}

func Build200OK(optionsMsg string) []byte {

	viaReg := regexp.MustCompile(`(?m)^Via: (.*)`)
	fromReg := regexp.MustCompile(`(?m)^From: (.*)`)
	toReg := regexp.MustCompile(`(?m)^To: (.*)`)
	callIDReg := regexp.MustCompile(`(?m)^Call-ID: (.*)`)
	cseqReg := regexp.MustCompile(`(?m)^CSeq: (.*)`)

	via := viaReg.FindStringSubmatch(optionsMsg)
	from := fromReg.FindStringSubmatch(optionsMsg)
	to := toReg.FindStringSubmatch(optionsMsg)
	callID := callIDReg.FindStringSubmatch(optionsMsg)
	cseq := cseqReg.FindStringSubmatch(optionsMsg)

	if len(via) < 2 || len(from) < 2 || len(to) < 2 || len(callID) < 2 || len(cseq) < 2 {
		log.Println("[SIP] Error to parse headers do OPTIONS")
		return nil
	}
	response := fmt.Sprintf(
		"SIP/2.0 200 OK\r\n"+
			"Via: %s\r\n"+
			"From: %s\r\n"+
			"To: %s\r\n"+
			"Call-ID: %s\r\n"+
			"CSeq: %s\r\n"+
			"User-Agent: jvrs-go-sip\r\n"+
			"Content-Length: 0\r\n\r\n",
		strings.TrimSpace(via[1]),
		strings.TrimSpace(from[1]),
		strings.TrimSpace(to[1]),
		strings.TrimSpace(callID[1]),
		strings.TrimSpace(cseq[1]),
	)

	return []byte(response)
}

func Build180Ringing(inviteMsg string) []byte {
	viaReg := regexp.MustCompile(`(?m)^Via: (.*)`)
	fromReg := regexp.MustCompile(`(?m)^From: (.*)`)
	toReg := regexp.MustCompile(`(?m)^To: (.*)`)
	callIDReg := regexp.MustCompile(`(?m)^Call-ID: (.*)`)
	cseqReg := regexp.MustCompile(`(?m)^CSeq: (.*)`)

	via := viaReg.FindStringSubmatch(inviteMsg)
	from := fromReg.FindStringSubmatch(inviteMsg)
	to := toReg.FindStringSubmatch(inviteMsg)
	callID := callIDReg.FindStringSubmatch(inviteMsg)
	cseq := cseqReg.FindStringSubmatch(inviteMsg)

	if len(via) < 2 || len(from) < 2 || len(to) < 2 || len(callID) < 2 || len(cseq) < 2 {
		log.Println("[SIP] Error to extract headers for 180 Ringing")
		return nil
	}

	response := fmt.Sprintf(
		"SIP/2.0 180 Ringing\r\n"+
			"Via: %s\r\n"+
			"From: %s\r\n"+
			"To: %s;tag=%d\r\n"+
			"Call-ID: %s\r\n"+
			"CSeq: %s\r\n"+
			"User-Agent: go-sip\r\n"+
			"Content-Length: 0\r\n\r\n",
		strings.TrimSpace(via[1]),
		strings.TrimSpace(from[1]),
		strings.TrimSpace(to[1]),
		time.Now().Unix(),
		strings.TrimSpace(callID[1]),
		strings.TrimSpace(cseq[1]),
	)

	return []byte(response)
}

func ParseInviteIP(inviteMsg string) (InviteData, error) {
	viaReg := regexp.MustCompile(`(?m)^Via: (.*)`)
	fromReg := regexp.MustCompile(`(?m)^From: (.*)`)
	toReg := regexp.MustCompile(`(?m)^To: (.*)`)
	callIDReg := regexp.MustCompile(`(?m)^Call-ID: (.*)`)
	cseqReg := regexp.MustCompile(`(?m)^CSeq: (.*)`)

	via := viaReg.FindStringSubmatch(inviteMsg)
	from := fromReg.FindStringSubmatch(inviteMsg)
	to := toReg.FindStringSubmatch(inviteMsg)
	callID := callIDReg.FindStringSubmatch(inviteMsg)
	cseq := cseqReg.FindStringSubmatch(inviteMsg)

	if via == nil || from == nil || to == nil || callID == nil || cseq == nil {
		return InviteData{}, fmt.Errorf("failed to parse Message from INVITE")
	} else {
		return InviteData{
			From:   strings.TrimSpace(from[1]),
			Via:    strings.TrimSpace(via[1]),
			To:     strings.TrimSpace(to[1]),
			CallID: strings.TrimSpace(callID[1]),
			Cseq:   strings.TrimSpace(cseq[1]),
		}, nil
	}
}

func Build200OKInvite(inviteMsg InviteData, localIP string, rtpPort int) []byte {

	sdp := fmt.Sprintf(
		"v=0\r\n"+
			"o=- %[1]d %[1]d IN IP4 %[2]s\r\n"+
			"s=jvrs-go-sip\r\n"+
			"c=IN IP4 %[2]s\r\n"+
			"t=0 0\r\n"+
			"m=audio %[3]d RTP/AVP 0 8 101\r\n"+
			"a=rtpmap:0 PCMU/8000\r\n"+
			"a=rtpmap:8 PCMA/8000\r\n"+
			"a=rtpmap:101 telephone-event/8000\r\n"+
			"a=fmtp:101 0-16\r\n"+
			"a=sendrecv\r\n",
		time.Now().Unix(), localIP, rtpPort,
	)

	response := fmt.Sprintf(
		"SIP/2.0 200 OK\r\n"+
			"Via: %s\r\n"+
			"From: %s\r\n"+
			"To: %s;tag=%d\r\n"+ // Tag é importante para identificar a sessão
			"Call-ID: %s\r\n"+
			"CSeq: %s\r\n"+
			"Contact: <sip:asterisk@%s:5060;transport=tcp>\r\n"+
			"Content-Type: application/sdp\r\n"+
			"Content-Length: %d\r\n\r\n"+
			"%s",
		strings.TrimSpace(inviteMsg.Via),
		strings.TrimSpace(inviteMsg.From),
		strings.TrimSpace(inviteMsg.To),
		time.Now().Unix(),
		strings.TrimSpace(inviteMsg.CallID),
		strings.TrimSpace(inviteMsg.Cseq),
		localIP,
		len(sdp),
		sdp,
	)

	return []byte(response)
}
