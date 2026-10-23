package sip

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const Version = "SIP/2.0"

var (
	ErrMalformedMessage = errors.New("malformed SIP message")
	ErrMessageTooLarge  = errors.New("SIP message exceeds configured limit")
)

type Limits struct {
	MaxStartLineBytes int
	MaxHeaderBytes    int
	MaxHeaders        int
	MaxBodyBytes      int
}

func DefaultLimits() Limits {
	return Limits{
		MaxStartLineBytes: 4 << 10,
		MaxHeaderBytes:    64 << 10,
		MaxHeaders:        200,
		MaxBodyBytes:      1 << 20,
	}
}

type Header struct {
	Name  string
	Value string
}

type Headers []Header

func (h Headers) Values(name string) []string {
	name = canonicalHeaderName(name)
	values := make([]string, 0, 1)
	for _, header := range h {
		if strings.EqualFold(canonicalHeaderName(header.Name), name) {
			values = append(values, header.Value)
		}
	}

	return values
}

func (h Headers) Get(name string) string {
	values := h.Values(name)
	if len(values) == 0 {
		return ""
	}

	return values[0]
}

func (h *Headers) Add(name, value string) {
	*h = append(*h, Header{Name: canonicalHeaderName(name), Value: value})
}

func (h *Headers) Set(name, value string) {
	name = canonicalHeaderName(name)
	result := make(Headers, 0, len(*h)+1)
	inserted := false
	for _, header := range *h {
		if strings.EqualFold(canonicalHeaderName(header.Name), name) {
			if !inserted {
				result = append(result, Header{Name: name, Value: value})
				inserted = true
			}

			continue
		}

		result = append(result, header)
	}

	if !inserted {
		result = append(result, Header{Name: name, Value: value})
	}

	*h = result
}

type Message struct {
	Method     string
	RequestURI string
	StatusCode int
	Reason     string
	Version    string
	Headers    Headers
	Body       []byte
}

func NewRequest(method, requestURI string) *Message {
	return &Message{Method: strings.ToUpper(method), RequestURI: requestURI, Version: Version}
}

func NewResponse(statusCode int, reason string) *Message {
	return &Message{StatusCode: statusCode, Reason: reason, Version: Version}
}

func (m *Message) IsRequest() bool { return m != nil && m.Method != "" }

func (m *Message) IsResponse() bool { return m != nil && m.StatusCode != 0 }

func (m *Message) CSeq() (uint32, string, error) {
	value := m.Headers.Get("CSeq")
	parts := strings.Fields(value)
	if len(parts) != 2 {
		return 0, "", fmt.Errorf("%w: invalid CSeq %q", ErrMalformedMessage, value)
	}

	sequence, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		return 0, "", fmt.Errorf("%w: invalid CSeq number: %v", ErrMalformedMessage, err)
	}

	return uint32(sequence), strings.ToUpper(parts[1]), nil
}

func (m *Message) TransactionKey() (string, error) {
	sequence, method, err := m.CSeq()
	if err != nil {
		return "", err
	}

	callID := strings.TrimSpace(m.Headers.Get("Call-ID"))
	if callID == "" {
		return "", fmt.Errorf("%w: missing Call-ID", ErrMalformedMessage)
	}

	branch := ViaBranch(m.Headers.Get("Via"))
	if branch == "" {
		return fmt.Sprintf("%s|%d|%s", callID, sequence, method), nil
	}

	return fmt.Sprintf("%s|%d|%s|%s", callID, sequence, method, branch), nil
}

func ViaBranch(value string) string {
	for _, parameter := range strings.Split(value, ";")[1:] {
		name, parameterValue, found := strings.Cut(strings.TrimSpace(parameter), "=")
		if found && strings.EqualFold(name, "branch") {
			return strings.TrimSpace(parameterValue)
		}
	}

	return ""
}

func (m *Message) MarshalBinary() ([]byte, error) {
	if m == nil {
		return nil, fmt.Errorf("%w: nil message", ErrMalformedMessage)
	}

	version := m.Version
	if version == "" {
		version = Version
	}

	var buffer bytes.Buffer
	if m.IsRequest() {
		if hasNewline(m.Method) || hasNewline(m.RequestURI) || m.Method == "" || m.RequestURI == "" {
			return nil, fmt.Errorf("%w: invalid request line", ErrMalformedMessage)
		}

		fmt.Fprintf(&buffer, "%s %s %s\r\n", strings.ToUpper(m.Method), m.RequestURI, version)
	} else if m.IsResponse() {
		if m.StatusCode < 100 || m.StatusCode > 699 || hasNewline(m.Reason) {
			return nil, fmt.Errorf("%w: invalid status line", ErrMalformedMessage)
		}

		fmt.Fprintf(&buffer, "%s %d %s\r\n", version, m.StatusCode, m.Reason)
	} else {
		return nil, fmt.Errorf("%w: message is neither request nor response", ErrMalformedMessage)
	}

	headers := append(Headers(nil), m.Headers...)
	headers.Set("Content-Length", strconv.Itoa(len(m.Body)))
	for _, header := range headers {
		if header.Name == "" || hasNewline(header.Name) || hasNewline(header.Value) {
			return nil, fmt.Errorf("%w: invalid header", ErrMalformedMessage)
		}

		fmt.Fprintf(&buffer, "%s: %s\r\n", canonicalHeaderName(header.Name), header.Value)
	}

	buffer.WriteString("\r\n")
	buffer.Write(m.Body)
	return buffer.Bytes(), nil
}

func Parse(data []byte) (*Message, error) {
	return ParseReader(bufio.NewReader(bytes.NewReader(data)), DefaultLimits())
}

func ParseReader(reader *bufio.Reader, limits Limits) (*Message, error) {
	limits = normalizeLimits(limits)
	startLine, err := readLineLimited(reader, limits.MaxStartLineBytes)
	if err != nil {
		return nil, err
	}

	for startLine == "" {
		startLine, err = readLineLimited(reader, limits.MaxStartLineBytes)
		if err != nil {
			return nil, err
		}
	}

	message, err := parseStartLine(startLine)
	if err != nil {
		return nil, err
	}

	headerBytes := 0
	for index := 0; ; index++ {
		if index >= limits.MaxHeaders {
			return nil, fmt.Errorf("%w: too many headers", ErrMessageTooLarge)
		}

		line, readErr := readLineLimited(reader, limits.MaxHeaderBytes-headerBytes)
		if readErr != nil {
			return nil, readErr
		}

		headerBytes += len(line) + 2
		if headerBytes > limits.MaxHeaderBytes {
			return nil, fmt.Errorf("%w: headers", ErrMessageTooLarge)
		}

		if line == "" {
			break
		}

		if line[0] == ' ' || line[0] == '\t' {
			return nil, fmt.Errorf("%w: obsolete folded header", ErrMalformedMessage)
		}

		name, value, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("%w: invalid header line", ErrMalformedMessage)
		}

		message.Headers.Add(strings.TrimSpace(name), strings.TrimSpace(value))
	}

	contentLength := 0
	contentLengths := message.Headers.Values("Content-Length")
	for index, value := range contentLengths {
		parsed, parseErr := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if parseErr != nil || parsed < 0 {
			return nil, fmt.Errorf("%w: invalid Content-Length", ErrMalformedMessage)
		}

		if parsed > int64(limits.MaxBodyBytes) {
			return nil, fmt.Errorf("%w: body", ErrMessageTooLarge)
		}

		if index > 0 && contentLength != int(parsed) {
			return nil, fmt.Errorf("%w: conflicting Content-Length", ErrMalformedMessage)
		}

		contentLength = int(parsed)
	}

	if contentLength > 0 {
		message.Body = make([]byte, contentLength)
		if _, err := io.ReadFull(reader, message.Body); err != nil {
			return nil, fmt.Errorf("%w: incomplete body: %v", ErrMalformedMessage, err)
		}
	}

	return message, nil
}

func parseStartLine(line string) (*Message, error) {
	parts := strings.Fields(line)
	if len(parts) < 3 {
		return nil, fmt.Errorf("%w: invalid start line", ErrMalformedMessage)
	}

	if strings.EqualFold(parts[0], Version) {
		statusCode, err := strconv.Atoi(parts[1])
		if err != nil || statusCode < 100 || statusCode > 699 {
			return nil, fmt.Errorf("%w: invalid status code", ErrMalformedMessage)
		}

		return &Message{Version: Version, StatusCode: statusCode, Reason: strings.Join(parts[2:], " ")}, nil
	}

	if len(parts) != 3 || !strings.EqualFold(parts[2], Version) {
		return nil, fmt.Errorf("%w: invalid request line", ErrMalformedMessage)
	}

	return &Message{Method: strings.ToUpper(parts[0]), RequestURI: parts[1], Version: Version}, nil
}

func readLineLimited(reader *bufio.Reader, maximum int) (string, error) {
	if maximum <= 0 {
		return "", fmt.Errorf("%w: line", ErrMessageTooLarge)
	}

	var buffer bytes.Buffer
	for {
		fragment, prefix, err := reader.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) && buffer.Len() == 0 && len(fragment) == 0 {
				return "", io.EOF
			}

			return "", err
		}

		if buffer.Len()+len(fragment) > maximum {
			return "", fmt.Errorf("%w: line", ErrMessageTooLarge)
		}

		buffer.Write(fragment)
		if !prefix {
			return buffer.String(), nil
		}
	}
}

func normalizeLimits(limits Limits) Limits {
	defaults := DefaultLimits()
	if limits.MaxStartLineBytes <= 0 {
		limits.MaxStartLineBytes = defaults.MaxStartLineBytes
	}

	if limits.MaxHeaderBytes <= 0 {
		limits.MaxHeaderBytes = defaults.MaxHeaderBytes
	}

	if limits.MaxHeaders <= 0 {
		limits.MaxHeaders = defaults.MaxHeaders
	}

	if limits.MaxBodyBytes <= 0 {
		limits.MaxBodyBytes = defaults.MaxBodyBytes
	}

	return limits
}

func hasNewline(value string) bool {
	return strings.ContainsAny(value, "\r\n")
}

func canonicalHeaderName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "v", "via":
		return "Via"
	case "f", "from":
		return "From"
	case "t", "to":
		return "To"
	case "i", "call-id":
		return "Call-ID"
	case "m", "contact":
		return "Contact"
	case "l", "content-length":
		return "Content-Length"
	case "c", "content-type":
		return "Content-Type"
	case "e", "content-encoding":
		return "Content-Encoding"
	case "s", "subject":
		return "Subject"
	case "k", "supported":
		return "Supported"
	case "cseq":
		return "CSeq"
	case "www-authenticate":
		return "WWW-Authenticate"
	case "proxy-authenticate":
		return "Proxy-Authenticate"
	case "authorization":
		return "Authorization"
	case "proxy-authorization":
		return "Proxy-Authorization"
	default:
		parts := strings.Split(strings.TrimSpace(name), "-")
		for index := range parts {
			if parts[index] != "" {
				parts[index] = strings.ToUpper(parts[index][:1]) + strings.ToLower(parts[index][1:])
			}
		}

		return strings.Join(parts, "-")
	}
}
