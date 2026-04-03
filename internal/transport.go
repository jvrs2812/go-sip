package internal

import (
	"fmt"
	"regexp"
	"time"
)

type Register struct {
	IpLocal  string
	IpServer string
	Ramal    string
	Cseq     int
	Auth     *SipAuth
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
			"Contact: <sip:%s@%s;transport=tcp>\r\n"+
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
		r.Ramal, r.IpLocal,
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
