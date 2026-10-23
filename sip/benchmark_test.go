package sip

import "testing"

func BenchmarkParseInvite(b *testing.B) {
	payload := []byte("INVITE sip:1001@example.test SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 192.0.2.10:5060;branch=z9hG4bK-benchmark\r\n" +
		"From: <sip:2002@example.test>;tag=remote\r\n" +
		"To: <sip:1001@example.test>\r\n" +
		"Call-ID: benchmark-call\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"Content-Type: application/sdp\r\n" +
		"Content-Length: 0\r\n\r\n")

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))

	for index := 0; index < b.N; index++ {
		if _, err := Parse(payload); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalInvite(b *testing.B) {
	request := NewRequest("INVITE", "sip:1001@example.test")
	request.Headers.Add("Via", "SIP/2.0/UDP 192.0.2.10:5060;branch=z9hG4bK-benchmark")
	request.Headers.Add("From", "<sip:2002@example.test>;tag=remote")
	request.Headers.Add("To", "<sip:1001@example.test>")
	request.Headers.Add("Call-ID", "benchmark-call")
	request.Headers.Add("CSeq", "1 INVITE")

	b.ReportAllocs()

	for index := 0; index < b.N; index++ {
		if _, err := request.MarshalBinary(); err != nil {
			b.Fatal(err)
		}
	}
}
