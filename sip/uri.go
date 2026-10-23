package sip

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

var ErrMalformedURI = errors.New("malformed SIP URI")

type Parameter struct {
	Name  string
	Value string
}

type URI struct {
	Scheme     string
	User       string
	Password   string
	Host       string
	Port       int
	Parameters []Parameter
	Headers    []Parameter
}

func ParseURI(value string) (URI, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\r\n<>") {
		return URI{}, ErrMalformedURI
	}

	scheme, remainder, found := strings.Cut(value, ":")
	if !found || (!strings.EqualFold(scheme, "sip") && !strings.EqualFold(scheme, "sips")) {
		return URI{}, fmt.Errorf("%w: unsupported or missing scheme", ErrMalformedURI)
	}

	if !validPercentEncoding(remainder) {
		return URI{}, fmt.Errorf("%w: invalid percent encoding", ErrMalformedURI)
	}

	address, headerText, _ := strings.Cut(remainder, "?")
	authority, parameterText, _ := strings.Cut(address, ";")

	uri := URI{Scheme: strings.ToLower(scheme)}
	userinfo, hostport, hasUser := strings.Cut(authority, "@")
	if hasUser {
		if userinfo == "" || strings.Contains(userinfo, "@") {
			return URI{}, fmt.Errorf("%w: invalid userinfo", ErrMalformedURI)
		}

		uri.User, uri.Password, _ = strings.Cut(userinfo, ":")
		if uri.User == "" {
			return URI{}, fmt.Errorf("%w: empty user", ErrMalformedURI)
		}
	} else {
		hostport = authority
	}

	host, port, err := parseURIHostPort(hostport)
	if err != nil {
		return URI{}, err
	}

	uri.Host = host
	uri.Port = port
	uri.Parameters, err = parseParameters(parameterText, ";")
	if err != nil {
		return URI{}, err
	}

	uri.Headers, err = parseParameters(headerText, "&")
	if err != nil {
		return URI{}, err
	}

	return uri, nil
}

func (u URI) String() string {
	if u.Scheme == "" || u.Host == "" {
		return ""
	}

	var builder strings.Builder
	builder.WriteString(strings.ToLower(u.Scheme))
	builder.WriteByte(':')

	if u.User != "" {
		builder.WriteString(u.User)
		if u.Password != "" {
			builder.WriteByte(':')
			builder.WriteString(u.Password)
		}

		builder.WriteByte('@')
	}

	if strings.Contains(u.Host, ":") {
		builder.WriteByte('[')
		builder.WriteString(strings.Trim(u.Host, "[]"))
		builder.WriteByte(']')
	} else {
		builder.WriteString(u.Host)
	}

	if u.Port > 0 {
		builder.WriteByte(':')
		builder.WriteString(strconv.Itoa(u.Port))
	}

	writeParameters(&builder, u.Parameters, ';')
	if len(u.Headers) > 0 {
		builder.WriteByte('?')
		writeParameters(&builder, u.Headers, '&')
	}

	return builder.String()
}

func (u URI) Parameter(name string) (string, bool) {
	return parameterValue(u.Parameters, name)
}

func (u URI) Header(name string) (string, bool) {
	return parameterValue(u.Headers, name)
}

type Address struct {
	DisplayName string
	URI         URI
	Parameters  []Parameter
}

func ParseAddress(value string) (Address, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return Address{}, ErrMalformedURI
	}

	opening := strings.IndexByte(value, '<')
	if opening < 0 {
		uri, err := ParseURI(value)
		if err != nil {
			return Address{}, err
		}

		return Address{URI: uri}, nil
	}

	closing := strings.IndexByte(value[opening+1:], '>')
	if closing < 0 {
		return Address{}, fmt.Errorf("%w: unterminated name-address", ErrMalformedURI)
	}
	closing += opening + 1

	displayName, err := parseDisplayName(strings.TrimSpace(value[:opening]))
	if err != nil {
		return Address{}, err
	}

	uri, err := ParseURI(value[opening+1 : closing])
	if err != nil {
		return Address{}, err
	}

	remainder := strings.TrimSpace(value[closing+1:])
	if remainder != "" && !strings.HasPrefix(remainder, ";") {
		return Address{}, fmt.Errorf("%w: invalid address parameters", ErrMalformedURI)
	}

	parameters, err := parseParameters(strings.TrimPrefix(remainder, ";"), ";")
	if err != nil {
		return Address{}, err
	}

	return Address{DisplayName: displayName, URI: uri, Parameters: parameters}, nil
}

func (a Address) String() string {
	uri := a.URI.String()
	if uri == "" {
		return ""
	}

	var builder strings.Builder
	if a.DisplayName != "" {
		builder.WriteString(strconv.Quote(a.DisplayName))
		builder.WriteByte(' ')
	}

	builder.WriteByte('<')
	builder.WriteString(uri)
	builder.WriteByte('>')
	writeParameters(&builder, a.Parameters, ';')

	return builder.String()
}

func (a Address) Parameter(name string) (string, bool) {
	return parameterValue(a.Parameters, name)
}

func (m *Message) ParsedRequestURI() (URI, error) {
	if m == nil || !m.IsRequest() {
		return URI{}, fmt.Errorf("%w: message is not a request", ErrMalformedURI)
	}

	return ParseURI(m.RequestURI)
}

func parseURIHostPort(value string) (string, int, error) {
	if value == "" {
		return "", 0, fmt.Errorf("%w: host is required", ErrMalformedURI)
	}

	host := value
	portText := ""

	if strings.HasPrefix(value, "[") {
		closing := strings.IndexByte(value, ']')
		if closing < 0 {
			return "", 0, fmt.Errorf("%w: unterminated IPv6 host", ErrMalformedURI)
		}

		host = value[1:closing]
		remainder := value[closing+1:]
		if remainder != "" {
			if !strings.HasPrefix(remainder, ":") {
				return "", 0, fmt.Errorf("%w: invalid host suffix", ErrMalformedURI)
			}

			portText = strings.TrimPrefix(remainder, ":")
		}

		if net.ParseIP(host) == nil {
			return "", 0, fmt.Errorf("%w: invalid IPv6 host", ErrMalformedURI)
		}
	} else {
		if strings.Count(value, ":") > 1 {
			return "", 0, fmt.Errorf("%w: IPv6 host must be bracketed", ErrMalformedURI)
		}

		if parsedHost, parsedPort, found := strings.Cut(value, ":"); found {
			host = parsedHost
			portText = parsedPort
		}
	}

	if host == "" || strings.ContainsAny(host, " \t") {
		return "", 0, fmt.Errorf("%w: invalid host", ErrMalformedURI)
	}

	if portText == "" {
		return host, 0, nil
	}

	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("%w: invalid port", ErrMalformedURI)
	}

	return host, port, nil
}

func parseParameters(value, separator string) ([]Parameter, error) {
	if value == "" {
		return nil, nil
	}

	parts := strings.Split(value, separator)
	parameters := make([]Parameter, 0, len(parts))
	for _, part := range parts {
		name, parameterValue, hasValue := strings.Cut(part, "=")
		if name == "" || strings.ContainsAny(name, " \t\r\n") {
			return nil, fmt.Errorf("%w: invalid parameter", ErrMalformedURI)
		}

		if !hasValue {
			parameterValue = ""
		}

		if strings.ContainsAny(parameterValue, "\r\n") {
			return nil, fmt.Errorf("%w: invalid parameter value", ErrMalformedURI)
		}

		parameters = append(parameters, Parameter{Name: name, Value: parameterValue})
	}

	return parameters, nil
}

func parseDisplayName(value string) (string, error) {
	if value == "" {
		return "", nil
	}

	if strings.HasPrefix(value, "\"") {
		displayName, err := strconv.Unquote(value)
		if err != nil {
			return "", fmt.Errorf("%w: invalid quoted display name", ErrMalformedURI)
		}

		return displayName, nil
	}

	if strings.ContainsAny(value, "\"") {
		return "", fmt.Errorf("%w: invalid display name", ErrMalformedURI)
	}

	return value, nil
}

func writeParameters(builder *strings.Builder, parameters []Parameter, separator byte) {
	for index, parameter := range parameters {
		if separator == ';' || index > 0 {
			builder.WriteByte(separator)
		}

		builder.WriteString(parameter.Name)
		if parameter.Value != "" {
			builder.WriteByte('=')
			builder.WriteString(parameter.Value)
		}
	}
}

func parameterValue(parameters []Parameter, name string) (string, bool) {
	for _, parameter := range parameters {
		if strings.EqualFold(parameter.Name, name) {
			return parameter.Value, true
		}
	}

	return "", false
}

func validPercentEncoding(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] != '%' {
			continue
		}

		if index+2 >= len(value) || !isHex(value[index+1]) || !isHex(value[index+2]) {
			return false
		}

		index += 2
	}

	return true
}

func isHex(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
}
