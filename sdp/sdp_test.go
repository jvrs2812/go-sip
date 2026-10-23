package sdp

import (
	"net"
	"strings"
	"testing"
)

func TestParseAndNegotiateAudio(t *testing.T) {
	offer, err := Parse([]byte("v=0\r\no=- 1 1 IN IP4 192.0.2.10\r\ns=test\r\nc=IN IP4 192.0.2.10\r\nt=0 0\r\nm=audio 4000 RTP/AVP 111 8 0\r\na=rtpmap:111 opus/48000/2\r\na=sendonly\r\n"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	answer, selected, err := NegotiateAudio(offer, net.ParseIP("198.51.100.20"), 6000, []Codec{{Name: "PCMU", ClockRate: 8000, Channels: 1}, {Name: "PCMA", ClockRate: 8000, Channels: 1}})
	if err != nil {
		t.Fatalf("NegotiateAudio() error = %v", err)
	}

	if selected.Name != "PCMA" || selected.PayloadType != 8 || answer.Media[0].Direction != RecvOnly {
		t.Fatalf("selected = %#v, answer = %#v", selected, answer)
	}

	encoded, err := answer.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	if !strings.Contains(string(encoded), "m=audio 6000 RTP/AVP 8") || !strings.Contains(string(encoded), "a=recvonly") {
		t.Fatalf("answer = %s", encoded)
	}
}

func TestParseRejectsMissingConnection(t *testing.T) {
	_, err := Parse([]byte("v=0\r\no=- 1 1 IN IP4 192.0.2.10\r\ns=test\r\nt=0 0\r\nm=audio 4000 RTP/AVP 0\r\n"))
	if err == nil {
		t.Fatal("Parse() expected an error")
	}
}

func TestParseAndMarshalICEAndDTLSSRTP(t *testing.T) {
	raw := "v=0\r\n" +
		"o=- 1 1 IN IP4 192.0.2.10\r\n" +
		"s=-\r\n" +
		"c=IN IP4 192.0.2.10\r\n" +
		"t=0 0\r\n" +
		"a=ice-ufrag:sessionUfrag\r\n" +
		"a=ice-pwd:sessionPassword123456789\r\n" +
		"a=ice-options:trickle\r\n" +
		"a=fingerprint:sha-256 AA:BB:CC\r\n" +
		"a=setup:actpass\r\n" +
		"m=audio 49170 UDP/TLS/RTP/SAVP 0\r\n" +
		"a=candidate:1 1 UDP 2130706431 192.0.2.10 49170 typ host\r\n" +
		"a=end-of-candidates\r\n" +
		"a=sendrecv\r\n"

	description, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}

	media := description.Media[0]
	if media.ICEUfrag != "sessionUfrag" || media.ICEPassword != "sessionPassword123456789" {
		t.Fatalf("session ICE attributes were not inherited: %#v", media)
	}

	if len(media.Candidates) != 1 || !media.EndOfCandidates {
		t.Fatalf("ICE candidates were not parsed: %#v", media)
	}

	if media.Fingerprint.Algorithm != "sha-256" || media.Fingerprint.Value != "AA:BB:CC" || media.Setup != "actpass" {
		t.Fatalf("DTLS attributes were not parsed: %#v", media)
	}

	encoded, err := description.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	for _, expected := range []string{
		"a=ice-ufrag:sessionUfrag\r\n",
		"a=candidate:1 1 UDP 2130706431 192.0.2.10 49170 typ host\r\n",
		"a=end-of-candidates\r\n",
		"a=fingerprint:sha-256 AA:BB:CC\r\n",
		"a=setup:actpass\r\n",
	} {
		if !strings.Contains(string(encoded), expected) {
			t.Fatalf("marshaled SDP does not contain %q:\n%s", expected, encoded)
		}
	}
}

func TestNegotiateILBCRespectsMode(t *testing.T) {
	offer, err := Parse([]byte(
		"v=0\r\n" +
			"o=- 1 1 IN IP4 192.0.2.10\r\n" +
			"s=-\r\n" +
			"c=IN IP4 192.0.2.10\r\n" +
			"t=0 0\r\n" +
			"m=audio 4000 RTP/AVP 102\r\n" +
			"a=rtpmap:102 iLBC/8000\r\n" +
			"a=fmtp:102 mode=30\r\n",
	))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = NegotiateAudio(offer, net.ParseIP("198.51.100.1"), 6000, []Codec{{Name: "iLBC", ClockRate: 8000, Channels: 1, FMTP: "mode=20"}})
	if err == nil {
		t.Fatal("mode=20 must not match a mode=30-only iLBC offer")
	}

	answer, selected, err := NegotiateAudio(offer, net.ParseIP("198.51.100.1"), 6000, []Codec{{Name: "iLBC", ClockRate: 8000, Channels: 1, FMTP: "mode=30"}})
	if err != nil {
		t.Fatal(err)
	}

	if selected.FMTP != "mode=30" || answer.Media[0].Codecs[102].FMTP != "mode=30" {
		t.Fatalf("unexpected iLBC negotiation: selected=%#v answer=%#v", selected, answer)
	}
}
