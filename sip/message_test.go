package sip

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestParseRequestWithCompactHeadersAndBody(t *testing.T) {
	raw := "MESSAGE sip:bob@example.com SIP/2.0\r\n" +
		"v: SIP/2.0/TCP client.example.com;branch=z9hG4bK1\r\n" +
		"f: <sip:alice@example.com>;tag=a\r\n" +
		"t: <sip:bob@example.com>\r\n" +
		"i: call-1\r\n" +
		"CSeq: 42 MESSAGE\r\n" +
		"l: 5\r\n\r\nhello"

	message, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if message.Method != "MESSAGE" || message.RequestURI != "sip:bob@example.com" {
		t.Fatalf("unexpected request line: %#v", message)
	}

	if got := message.Headers.Get("Via"); !strings.Contains(got, "branch=z9hG4bK1") {
		t.Fatalf("Via = %q", got)
	}

	if got := string(message.Body); got != "hello" {
		t.Fatalf("Body = %q", got)
	}

	if key, err := message.TransactionKey(); err != nil || key != "call-1|42|MESSAGE|z9hG4bK1" {
		t.Fatalf("TransactionKey() = %q, %v", key, err)
	}
}

func TestParseConcatenatedMessages(t *testing.T) {
	first := "SIP/2.0 100 Trying\r\nCall-ID: a\r\nCSeq: 1 INVITE\r\nContent-Length: 0\r\n\r\n"
	second := "SIP/2.0 200 OK\r\nCall-ID: a\r\nCSeq: 1 INVITE\r\nContent-Length: 0\r\n\r\n"
	reader := bufio.NewReader(strings.NewReader(first + second))
	for _, code := range []int{100, 200} {
		message, err := ParseReader(reader, DefaultLimits())
		if err != nil {
			t.Fatalf("ParseReader() error = %v", err)
		}

		if message.StatusCode != code {
			t.Fatalf("StatusCode = %d, want %d", message.StatusCode, code)
		}
	}
}

func TestParseRejectsOversizedAndIncompleteMessages(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxHeaderBytes = 20
	_, err := ParseReader(bufio.NewReader(strings.NewReader("OPTIONS sip:x SIP/2.0\r\nLong: 12345678901234567890\r\n\r\n")), limits)
	if !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("error = %v, want ErrMessageTooLarge", err)
	}

	_, err = Parse([]byte("SIP/2.0 200 OK\r\nContent-Length: 4\r\n\r\nab"))
	if !errors.Is(err, ErrMalformedMessage) {
		t.Fatalf("error = %v, want ErrMalformedMessage", err)
	}
}

type fragmentedReader struct {
	data []byte
}

func (r *fragmentedReader) Read(buffer []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}

	buffer[0] = r.data[0]
	r.data = r.data[1:]

	return 1, nil
}

func TestParseHandlesFragmentedStreamAndKeepAlive(t *testing.T) {
	raw := []byte("\r\n\r\nMESSAGE sip:bob@example.com SIP/2.0\r\nCall-ID: fragmented\r\nCSeq: 9 MESSAGE\r\nContent-Length: 4\r\n\r\n\x00\x01\x02\x03")
	reader := bufio.NewReaderSize(&fragmentedReader{data: raw}, 2)

	message, err := ParseReader(reader, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}

	if message.Method != "MESSAGE" || !bytes.Equal(message.Body, []byte{0, 1, 2, 3}) {
		t.Fatalf("fragmented message = %#v", message)
	}
}

func TestParseRejectsPartialEOFAndConflictingContentLength(t *testing.T) {
	_, err := ParseReader(
		bufio.NewReader(strings.NewReader("OPTIONS sip:x SIP/2.0\r\nCall-ID: partial")),
		DefaultLimits(),
	)
	if err == nil {
		t.Fatal("partial header must fail")
	}

	_, err = Parse([]byte(
		"MESSAGE sip:x SIP/2.0\r\n" +
			"Content-Length: 3\r\n" +
			"l: 4\r\n\r\nbody",
	))
	if !errors.Is(err, ErrMalformedMessage) {
		t.Fatalf("conflicting Content-Length error = %v", err)
	}
}

func TestMarshalRoundTripAndRejectsHeaderInjection(t *testing.T) {
	request := NewRequest("OPTIONS", "sip:server.example.com")
	request.Headers.Add("Via", "SIP/2.0/TCP host;branch=z9hG4bK1")
	request.Headers.Add("Call-ID", "call-2")
	request.Headers.Add("CSeq", "1 OPTIONS")
	request.Body = []byte("payload")

	encoded, err := request.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}

	parsed, err := Parse(encoded)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if !bytes.Equal(parsed.Body, request.Body) {
		t.Fatalf("Body = %q", parsed.Body)
	}

	request.Headers.Add("Bad", "safe\r\nInjected: yes")
	if _, err := request.MarshalBinary(); !errors.Is(err, ErrMalformedMessage) {
		t.Fatalf("error = %v, want ErrMalformedMessage", err)
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte("OPTIONS sip:x SIP/2.0\r\nCall-ID: a\r\nCSeq: 1 OPTIONS\r\nContent-Length: 0\r\n\r\n"))
	f.Fuzz(func(t *testing.T, input []byte) {
		limits := Limits{MaxStartLineBytes: 256, MaxHeaderBytes: 2048, MaxHeaders: 32, MaxBodyBytes: 4096}
		_, _ = ParseReader(bufio.NewReader(bytes.NewReader(input)), limits)
	})
}
