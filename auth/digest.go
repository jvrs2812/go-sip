package auth

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	ErrInvalidChallenge     = errors.New("invalid Digest challenge")
	ErrUnsupportedAlgorithm = errors.New("unsupported Digest algorithm")
	ErrUnsupportedQOP       = errors.New("unsupported Digest qop")
)

type Challenge struct {
	Realm     string
	Nonce     string
	Opaque    string
	Algorithm string
	QOP       []string
	Stale     bool
}

type Credentials struct {
	Username string
	Password string
}

type Request struct {
	Method     string
	URI        string
	Body       []byte
	NonceCount uint32
	CNonce     string
}

func ParseChallenge(value string) (Challenge, error) {
	value = strings.TrimSpace(value)
	if len(value) < len("Digest ") || !strings.EqualFold(value[:len("Digest")], "Digest") {
		return Challenge{}, ErrInvalidChallenge
	}

	parameters, err := parseParameters(strings.TrimSpace(value[len("Digest"):]))
	if err != nil {
		return Challenge{}, err
	}

	challenge := Challenge{
		Realm:     parameters["realm"],
		Nonce:     parameters["nonce"],
		Opaque:    parameters["opaque"],
		Algorithm: parameters["algorithm"],
		Stale:     strings.EqualFold(parameters["stale"], "true"),
	}

	if challenge.Realm == "" || challenge.Nonce == "" {
		return Challenge{}, ErrInvalidChallenge
	}

	if challenge.Algorithm == "" {
		challenge.Algorithm = "MD5"
	}

	for _, qop := range strings.Split(parameters["qop"], ",") {
		qop = strings.ToLower(strings.TrimSpace(qop))
		if qop != "" {
			challenge.QOP = append(challenge.QOP, qop)
		}
	}

	return challenge, nil
}

func Authorization(challenge Challenge, credentials Credentials, request Request) (string, error) {
	if challenge.Realm == "" || challenge.Nonce == "" || credentials.Username == "" || request.Method == "" || request.URI == "" {
		return "", ErrInvalidChallenge
	}

	algorithm := challenge.Algorithm
	if algorithm == "" {
		algorithm = "MD5"
	}

	baseAlgorithm := strings.TrimSuffix(strings.ToUpper(algorithm), "-SESS")
	baseAlgorithm = strings.TrimPrefix(baseAlgorithm, "AKAV2-")
	if baseAlgorithm != "MD5" && baseAlgorithm != "SHA-256" {
		return "", fmt.Errorf("%w: %s", ErrUnsupportedAlgorithm, algorithm)
	}

	qop, err := selectQOP(challenge.QOP)
	if err != nil {
		return "", err
	}

	cnonce := request.CNonce
	if (qop != "" || strings.HasSuffix(strings.ToUpper(algorithm), "-SESS")) && cnonce == "" {
		cnonce, err = randomHex(16)
		if err != nil {
			return "", fmt.Errorf("generate cnonce: %w", err)
		}
	}

	ha1 := digest(baseAlgorithm, credentials.Username+":"+challenge.Realm+":"+credentials.Password)
	if strings.HasSuffix(strings.ToUpper(algorithm), "-SESS") {
		ha1 = digest(baseAlgorithm, ha1+":"+challenge.Nonce+":"+cnonce)
	}

	ha2Input := strings.ToUpper(request.Method) + ":" + request.URI
	if qop == "auth-int" {
		ha2Input += ":" + digestBytes(baseAlgorithm, request.Body)
	}

	ha2 := digest(baseAlgorithm, ha2Input)

	responseInput := ha1 + ":" + challenge.Nonce + ":"
	nonceCount := request.NonceCount
	if nonceCount == 0 {
		nonceCount = 1
	}

	nc := fmt.Sprintf("%08x", nonceCount)
	if qop != "" {
		responseInput += nc + ":" + cnonce + ":" + qop + ":"
	}

	response := digest(baseAlgorithm, responseInput+ha2)

	fields := []string{
		fmt.Sprintf("username=%s", quote(credentials.Username)),
		fmt.Sprintf("realm=%s", quote(challenge.Realm)),
		fmt.Sprintf("nonce=%s", quote(challenge.Nonce)),
		fmt.Sprintf("uri=%s", quote(request.URI)),
		fmt.Sprintf("response=%s", quote(response)),
		"algorithm=" + algorithm,
	}

	if challenge.Opaque != "" {
		fields = append(fields, fmt.Sprintf("opaque=%s", quote(challenge.Opaque)))
	}

	if qop != "" {
		fields = append(fields, "qop="+qop, "nc="+nc, fmt.Sprintf("cnonce=%s", quote(cnonce)))
	}

	return "Digest " + strings.Join(fields, ", "), nil
}

func CalculateResponse(challenge Challenge, credentials Credentials, request Request) (string, error) {
	header, err := Authorization(challenge, credentials, request)
	if err != nil {
		return "", err
	}

	parameters, err := parseParameters(strings.TrimPrefix(header, "Digest "))
	if err != nil {
		return "", err
	}

	return parameters["response"], nil
}

func selectQOP(options []string) (string, error) {
	if len(options) == 0 {
		return "", nil
	}

	for _, option := range options {
		if strings.EqualFold(option, "auth") {
			return "auth", nil
		}
	}

	for _, option := range options {
		if strings.EqualFold(option, "auth-int") {
			return "auth-int", nil
		}
	}

	return "", fmt.Errorf("%w: %s", ErrUnsupportedQOP, strings.Join(options, ","))
}

func digest(algorithm, value string) string {
	return digestBytes(algorithm, []byte(value))
}

func digestBytes(algorithm string, value []byte) string {
	if strings.EqualFold(algorithm, "SHA-256") {
		sum := sha256.Sum256(value)
		return hex.EncodeToString(sum[:])
	}

	sum := md5.Sum(value) // #nosec G501 -- MD5 is required for legacy SIP Digest interoperability.
	return hex.EncodeToString(sum[:])
}

func randomHex(bytesCount int) (string, error) {
	value := make([]byte, bytesCount)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}

	return hex.EncodeToString(value), nil
}

func quote(value string) string {
	return strconv.Quote(value)
}

func parseParameters(value string) (map[string]string, error) {
	parameters := make(map[string]string)
	for index := 0; index < len(value); {
		for index < len(value) && (value[index] == ' ' || value[index] == '\t' || value[index] == ',') {
			index++
		}

		if index == len(value) {
			break
		}

		nameStart := index
		for index < len(value) && value[index] != '=' && value[index] != ',' {
			index++
		}

		if index == len(value) || value[index] != '=' {
			return nil, ErrInvalidChallenge
		}

		name := strings.ToLower(strings.TrimSpace(value[nameStart:index]))
		index++
		for index < len(value) && (value[index] == ' ' || value[index] == '\t') {
			index++
		}

		if name == "" || index == len(value) {
			return nil, ErrInvalidChallenge
		}

		var parsed string
		if value[index] == '"' {
			index++
			var builder strings.Builder
			closed := false
			for index < len(value) {
				if value[index] == '\\' && index+1 < len(value) {
					builder.WriteByte(value[index+1])
					index += 2
					continue
				}

				if value[index] == '"' {
					index++
					closed = true
					break
				}

				builder.WriteByte(value[index])
				index++
			}

			if !closed {
				return nil, ErrInvalidChallenge
			}

			parsed = builder.String()
		} else {
			valueStart := index
			for index < len(value) && value[index] != ',' {
				index++
			}

			parsed = strings.TrimSpace(value[valueStart:index])
		}

		parameters[name] = parsed
		for index < len(value) && (value[index] == ' ' || value[index] == '\t') {
			index++
		}

		if index < len(value) && value[index] != ',' {
			return nil, ErrInvalidChallenge
		}
	}

	return parameters, nil
}
