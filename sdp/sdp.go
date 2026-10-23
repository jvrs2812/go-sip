package sdp

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

var ErrInvalidDescription = errors.New("invalid SDP description")

type Direction string

const (
	SendRecv Direction = "sendrecv"
	SendOnly Direction = "sendonly"
	RecvOnly Direction = "recvonly"
	Inactive Direction = "inactive"
)

type Codec struct {
	PayloadType uint8
	Name        string
	ClockRate   int
	Channels    int
	FMTP        string
}

type Fingerprint struct {
	Algorithm string
	Value     string
}

type Attribute struct {
	Name  string
	Value string
}

type Media struct {
	Type            string
	Port            int
	Protocol        string
	Formats         []uint8
	Connection      net.IP
	Direction       Direction
	Codecs          map[uint8]Codec
	ICEUfrag        string
	ICEPassword     string
	ICEOptions      []string
	Candidates      []string
	EndOfCandidates bool
	Fingerprint     Fingerprint
	Setup           string
	Attributes      []Attribute
}

type Description struct {
	OriginID      uint64
	OriginVersion uint64
	SessionName   string
	Connection    net.IP
	ICEUfrag      string
	ICEPassword   string
	ICEOptions    []string
	Fingerprint   Fingerprint
	Setup         string
	Attributes    []Attribute
	Media         []Media
}

func Parse(data []byte) (Description, error) {
	description := Description{SessionName: "-"}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var current *Media
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		if len(line) < 2 || line[1] != '=' {
			return Description{}, fmt.Errorf("%w: %q", ErrInvalidDescription, line)
		}

		value := line[2:]
		switch line[0] {
		case 'v':
			if value != "0" {
				return Description{}, fmt.Errorf("%w: unsupported version", ErrInvalidDescription)
			}
		case 'o':
			parts := strings.Fields(value)
			if len(parts) != 6 {
				return Description{}, fmt.Errorf("%w: origin", ErrInvalidDescription)
			}

			id, err := strconv.ParseUint(parts[1], 10, 64)
			if err != nil {
				return Description{}, fmt.Errorf("%w: origin ID", ErrInvalidDescription)
			}

			version, err := strconv.ParseUint(parts[2], 10, 64)
			if err != nil {
				return Description{}, fmt.Errorf("%w: origin version", ErrInvalidDescription)
			}

			description.OriginID, description.OriginVersion = id, version
		case 's':
			description.SessionName = value
		case 'c':
			ip, err := parseConnection(value)
			if err != nil {
				return Description{}, err
			}

			if current == nil {
				description.Connection = ip
			} else {
				current.Connection = ip
			}
		case 'm':
			media, err := parseMedia(value)
			if err != nil {
				return Description{}, err
			}

			description.Media = append(description.Media, media)
			current = &description.Media[len(description.Media)-1]
		case 'a':
			if current == nil {
				handled, err := parseSessionAttribute(&description, value)
				if err != nil {
					return Description{}, err
				}

				if !handled {
					attribute, err := parseGenericAttribute(value)
					if err != nil {
						return Description{}, err
					}

					description.Attributes = append(description.Attributes, attribute)
				}

				continue
			}

			handled, err := parseAttribute(current, value)
			if err != nil {
				return Description{}, err
			}

			if !handled {
				attribute, err := parseGenericAttribute(value)
				if err != nil {
					return Description{}, err
				}

				current.Attributes = append(current.Attributes, attribute)
			}
		}
	}

	if len(description.Media) == 0 {
		return Description{}, fmt.Errorf("%w: no media", ErrInvalidDescription)
	}

	for index := range description.Media {
		if description.Media[index].Connection == nil {
			description.Media[index].Connection = description.Connection
		}

		if description.Media[index].Connection == nil {
			return Description{}, fmt.Errorf("%w: media has no connection", ErrInvalidDescription)
		}

		if description.Media[index].Direction == "" {
			description.Media[index].Direction = SendRecv
		}

		inheritSessionAttributes(description, &description.Media[index])

		populateStaticCodecs(&description.Media[index])
	}

	return description, nil
}

func (d Description) Marshal() ([]byte, error) {
	if len(d.Media) == 0 {
		return nil, fmt.Errorf("%w: no media", ErrInvalidDescription)
	}

	connection := d.Connection
	if connection == nil {
		connection = d.Media[0].Connection
	}

	if connection == nil {
		return nil, fmt.Errorf("%w: no connection", ErrInvalidDescription)
	}

	family := "IP4"
	if connection.To4() == nil {
		family = "IP6"
	}

	sessionName := d.SessionName
	if sessionName == "" {
		sessionName = "go-sip"
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "v=0\r\no=- %d %d IN %s %s\r\ns=%s\r\nc=IN %s %s\r\nt=0 0\r\n", d.OriginID, d.OriginVersion, family, connection.String(), sessionName, family, connection.String())
	if err := writeICEAttributes(&builder, d.ICEUfrag, d.ICEPassword, d.ICEOptions); err != nil {
		return nil, err
	}

	if err := writeSecurityAttributes(&builder, d.Fingerprint, d.Setup); err != nil {
		return nil, err
	}

	if err := writeGenericAttributes(&builder, d.Attributes); err != nil {
		return nil, err
	}

	for _, media := range d.Media {
		formats := make([]string, 0, len(media.Formats))
		for _, payloadType := range media.Formats {
			formats = append(formats, strconv.Itoa(int(payloadType)))
		}

		fmt.Fprintf(&builder, "m=%s %d %s %s\r\n", media.Type, media.Port, media.Protocol, strings.Join(formats, " "))
		if media.Connection != nil && !media.Connection.Equal(connection) {
			mediaFamily := "IP4"
			if media.Connection.To4() == nil {
				mediaFamily = "IP6"
			}

			fmt.Fprintf(&builder, "c=IN %s %s\r\n", mediaFamily, media.Connection.String())
		}

		for _, payloadType := range media.Formats {
			codec, ok := media.Codecs[payloadType]
			if !ok || codec.Name == "" {
				continue
			}

			channels := ""
			if codec.Channels > 1 {
				channels = "/" + strconv.Itoa(codec.Channels)
			}

			fmt.Fprintf(&builder, "a=rtpmap:%d %s/%d%s\r\n", payloadType, codec.Name, codec.ClockRate, channels)
			if codec.FMTP != "" {
				fmt.Fprintf(&builder, "a=fmtp:%d %s\r\n", payloadType, codec.FMTP)
			}
		}

		if err := writeICEAttributes(&builder, media.ICEUfrag, media.ICEPassword, media.ICEOptions); err != nil {
			return nil, err
		}

		for _, candidate := range media.Candidates {
			if err := validateAttribute(candidate); err != nil {
				return nil, err
			}

			fmt.Fprintf(&builder, "a=candidate:%s\r\n", strings.TrimPrefix(candidate, "candidate:"))
		}

		if media.EndOfCandidates {
			builder.WriteString("a=end-of-candidates\r\n")
		}

		if err := writeSecurityAttributes(&builder, media.Fingerprint, media.Setup); err != nil {
			return nil, err
		}

		if err := writeGenericAttributes(&builder, media.Attributes); err != nil {
			return nil, err
		}

		direction := media.Direction
		if direction == "" {
			direction = SendRecv
		}

		fmt.Fprintf(&builder, "a=%s\r\n", direction)
	}

	return []byte(builder.String()), nil
}

func NegotiateAudio(offer Description, localIP net.IP, localPort int, supported []Codec) (Description, Codec, error) {
	var offered *Media
	for index := range offer.Media {
		if offer.Media[index].Type == "audio" && offer.Media[index].Port != 0 {
			offered = &offer.Media[index]
			break
		}
	}

	if offered == nil {
		return Description{}, Codec{}, errors.New("SDP offer has no active audio media")
	}

	byName := make(map[string]Codec)
	for _, codec := range supported {
		byName[codecKey(codec)] = codec
	}

	var selected Codec
	for _, payloadType := range offered.Formats {
		candidate, ok := offered.Codecs[payloadType]
		if !ok {
			continue
		}

		if local, exists := byName[codecKey(candidate)]; exists {
			selected = local
			selected.PayloadType = payloadType
			break
		}
	}

	if selected.Name == "" {
		return Description{}, Codec{}, errors.New("SDP offer has no supported audio codec")
	}

	formats := []uint8{selected.PayloadType}
	answerCodecs := map[uint8]Codec{selected.PayloadType: selected}

	for _, payloadType := range offered.Formats {
		codec := offered.Codecs[payloadType]

		if strings.EqualFold(codec.Name, "telephone-event") && codec.ClockRate == selected.ClockRate {
			formats = append(formats, payloadType)
			answerCodecs[payloadType] = codec

			break
		}
	}

	answerDirection := SendRecv
	switch offered.Direction {
	case SendOnly:
		answerDirection = RecvOnly
	case RecvOnly:
		answerDirection = SendOnly
	case Inactive:
		answerDirection = Inactive
	}

	answer := Description{
		OriginID:      offer.OriginID,
		OriginVersion: offer.OriginVersion + 1,
		SessionName:   "go-sip",
		Connection:    localIP,
		Media: []Media{{
			Type:       "audio",
			Port:       localPort,
			Protocol:   offered.Protocol,
			Formats:    formats,
			Connection: localIP,
			Direction:  answerDirection,
			Codecs:     answerCodecs,
		}},
	}

	return answer, selected, nil
}

func TelephoneEventPayload(description Description) (uint8, bool) {
	for _, mediaDescription := range description.Media {
		if mediaDescription.Type != "audio" || mediaDescription.Port == 0 {
			continue
		}

		for _, payloadType := range mediaDescription.Formats {
			codec := mediaDescription.Codecs[payloadType]

			if strings.EqualFold(codec.Name, "telephone-event") {
				return payloadType, true
			}
		}
	}

	return 0, false
}

func parseConnection(value string) (net.IP, error) {
	parts := strings.Fields(value)
	if len(parts) != 3 || parts[0] != "IN" || (parts[1] != "IP4" && parts[1] != "IP6") {
		return nil, fmt.Errorf("%w: connection", ErrInvalidDescription)
	}

	ip := net.ParseIP(strings.Split(parts[2], "/")[0])
	if ip == nil {
		return nil, fmt.Errorf("%w: connection address", ErrInvalidDescription)
	}

	return ip, nil
}

func parseMedia(value string) (Media, error) {
	parts := strings.Fields(value)
	if len(parts) < 4 {
		return Media{}, fmt.Errorf("%w: media", ErrInvalidDescription)
	}

	portValue := strings.Split(parts[1], "/")[0]
	port, err := strconv.Atoi(portValue)
	if err != nil || port < 0 || port > 65535 {
		return Media{}, fmt.Errorf("%w: media port", ErrInvalidDescription)
	}

	media := Media{Type: parts[0], Port: port, Protocol: parts[2], Codecs: make(map[uint8]Codec)}
	for _, value := range parts[3:] {
		payloadType, err := strconv.ParseUint(value, 10, 7)
		if err != nil {
			return Media{}, fmt.Errorf("%w: payload type", ErrInvalidDescription)
		}

		media.Formats = append(media.Formats, uint8(payloadType))
	}

	return media, nil
}

func parseAttribute(media *Media, value string) (bool, error) {
	switch Direction(value) {
	case SendRecv, SendOnly, RecvOnly, Inactive:
		media.Direction = Direction(value)

		return true, nil
	}

	if strings.HasPrefix(value, "rtpmap:") {
		payloadAndEncoding := strings.TrimPrefix(value, "rtpmap:")
		payloadText, encoding, found := strings.Cut(payloadAndEncoding, " ")
		if !found {
			return true, fmt.Errorf("%w: rtpmap", ErrInvalidDescription)
		}

		payloadType, err := strconv.ParseUint(payloadText, 10, 7)
		if err != nil {
			return true, fmt.Errorf("%w: rtpmap payload", ErrInvalidDescription)
		}

		parts := strings.Split(encoding, "/")
		if len(parts) < 2 || len(parts) > 3 {
			return true, fmt.Errorf("%w: rtpmap encoding", ErrInvalidDescription)
		}

		clockRate, err := strconv.Atoi(parts[1])
		if err != nil || clockRate <= 0 {
			return true, fmt.Errorf("%w: rtpmap clock", ErrInvalidDescription)
		}

		channels := 1
		if len(parts) == 3 {
			channels, err = strconv.Atoi(parts[2])
			if err != nil || channels <= 0 {
				return true, fmt.Errorf("%w: rtpmap channels", ErrInvalidDescription)
			}
		}

		media.Codecs[uint8(payloadType)] = Codec{PayloadType: uint8(payloadType), Name: parts[0], ClockRate: clockRate, Channels: channels}
		return true, nil
	}

	if strings.HasPrefix(value, "fmtp:") {
		payloadAndFormat := strings.TrimPrefix(value, "fmtp:")
		payloadText, format, found := strings.Cut(payloadAndFormat, " ")
		if !found {
			return true, fmt.Errorf("%w: fmtp", ErrInvalidDescription)
		}

		payloadType, err := strconv.ParseUint(payloadText, 10, 7)
		if err != nil {
			return true, fmt.Errorf("%w: fmtp payload", ErrInvalidDescription)
		}

		codec := media.Codecs[uint8(payloadType)]
		codec.PayloadType = uint8(payloadType)
		codec.FMTP = format
		media.Codecs[uint8(payloadType)] = codec

		return true, nil
	}

	if strings.HasPrefix(value, "ice-ufrag:") {
		media.ICEUfrag = strings.TrimSpace(strings.TrimPrefix(value, "ice-ufrag:"))

		return true, requireAttributeValue("ice-ufrag", media.ICEUfrag)
	}

	if strings.HasPrefix(value, "ice-pwd:") {
		media.ICEPassword = strings.TrimSpace(strings.TrimPrefix(value, "ice-pwd:"))

		return true, requireAttributeValue("ice-pwd", media.ICEPassword)
	}

	if strings.HasPrefix(value, "ice-options:") {
		media.ICEOptions = strings.Fields(strings.TrimPrefix(value, "ice-options:"))

		return true, nil
	}

	if strings.HasPrefix(value, "candidate:") {
		candidate := strings.TrimSpace(strings.TrimPrefix(value, "candidate:"))
		if err := requireAttributeValue("candidate", candidate); err != nil {
			return true, err
		}

		media.Candidates = append(media.Candidates, candidate)

		return true, nil
	}

	if value == "end-of-candidates" {
		media.EndOfCandidates = true

		return true, nil
	}

	if strings.HasPrefix(value, "fingerprint:") {
		fingerprint, err := parseFingerprint(strings.TrimPrefix(value, "fingerprint:"))
		if err != nil {
			return true, err
		}

		media.Fingerprint = fingerprint

		return true, nil
	}

	if strings.HasPrefix(value, "setup:") {
		media.Setup = strings.TrimSpace(strings.TrimPrefix(value, "setup:"))

		return true, validateSetup(media.Setup)
	}

	return false, nil
}

func parseSessionAttribute(description *Description, value string) (bool, error) {
	if strings.HasPrefix(value, "ice-ufrag:") {
		description.ICEUfrag = strings.TrimSpace(strings.TrimPrefix(value, "ice-ufrag:"))

		return true, requireAttributeValue("ice-ufrag", description.ICEUfrag)
	}

	if strings.HasPrefix(value, "ice-pwd:") {
		description.ICEPassword = strings.TrimSpace(strings.TrimPrefix(value, "ice-pwd:"))

		return true, requireAttributeValue("ice-pwd", description.ICEPassword)
	}

	if strings.HasPrefix(value, "ice-options:") {
		description.ICEOptions = strings.Fields(strings.TrimPrefix(value, "ice-options:"))

		return true, nil
	}

	if strings.HasPrefix(value, "fingerprint:") {
		fingerprint, err := parseFingerprint(strings.TrimPrefix(value, "fingerprint:"))
		if err != nil {
			return true, err
		}

		description.Fingerprint = fingerprint

		return true, nil
	}

	if strings.HasPrefix(value, "setup:") {
		description.Setup = strings.TrimSpace(strings.TrimPrefix(value, "setup:"))

		return true, validateSetup(description.Setup)
	}

	return false, nil
}

func parseGenericAttribute(value string) (Attribute, error) {
	name, attributeValue, found := strings.Cut(value, ":")
	name = strings.TrimSpace(name)

	if name == "" || strings.ContainsAny(name, " \t\r\n") {
		return Attribute{}, fmt.Errorf("%w: attribute name", ErrInvalidDescription)
	}

	if !found {
		attributeValue = ""
	}

	if err := validateAttribute(attributeValue); err != nil {
		return Attribute{}, err
	}

	return Attribute{Name: name, Value: attributeValue}, nil
}

func writeGenericAttributes(builder *strings.Builder, attributes []Attribute) error {
	for _, attribute := range attributes {
		if attribute.Name == "" || strings.ContainsAny(attribute.Name, " :\t\r\n") {
			return fmt.Errorf("%w: attribute name", ErrInvalidDescription)
		}

		if err := validateAttribute(attribute.Value); err != nil {
			return err
		}

		fmt.Fprintf(builder, "a=%s", attribute.Name)
		if attribute.Value != "" {
			fmt.Fprintf(builder, ":%s", attribute.Value)
		}

		builder.WriteString("\r\n")
	}

	return nil
}

func inheritSessionAttributes(description Description, media *Media) {
	if media.ICEUfrag == "" {
		media.ICEUfrag = description.ICEUfrag
	}

	if media.ICEPassword == "" {
		media.ICEPassword = description.ICEPassword
	}

	if len(media.ICEOptions) == 0 {
		media.ICEOptions = append([]string(nil), description.ICEOptions...)
	}

	if media.Fingerprint.Algorithm == "" {
		media.Fingerprint = description.Fingerprint
	}

	if media.Setup == "" {
		media.Setup = description.Setup
	}
}

func writeICEAttributes(builder *strings.Builder, ufrag, password string, options []string) error {
	attributes := []struct {
		name  string
		value string
	}{
		{name: "ice-ufrag", value: ufrag},
		{name: "ice-pwd", value: password},
	}

	for _, attribute := range attributes {
		name := attribute.name
		value := attribute.value
		if value == "" {
			continue
		}

		if err := validateAttribute(value); err != nil {
			return err
		}

		fmt.Fprintf(builder, "a=%s:%s\r\n", name, value)
	}

	if len(options) > 0 {
		value := strings.Join(options, " ")
		if err := validateAttribute(value); err != nil {
			return err
		}

		fmt.Fprintf(builder, "a=ice-options:%s\r\n", value)
	}

	return nil
}

func writeSecurityAttributes(builder *strings.Builder, fingerprint Fingerprint, setup string) error {
	if fingerprint.Algorithm != "" || fingerprint.Value != "" {
		if fingerprint.Algorithm == "" || fingerprint.Value == "" {
			return fmt.Errorf("%w: incomplete fingerprint", ErrInvalidDescription)
		}

		if err := validateAttribute(fingerprint.Algorithm + " " + fingerprint.Value); err != nil {
			return err
		}

		fmt.Fprintf(builder, "a=fingerprint:%s %s\r\n", fingerprint.Algorithm, fingerprint.Value)
	}

	if setup != "" {
		if err := validateSetup(setup); err != nil {
			return err
		}

		fmt.Fprintf(builder, "a=setup:%s\r\n", setup)
	}

	return nil
}

func parseFingerprint(value string) (Fingerprint, error) {
	parts := strings.Fields(value)
	if len(parts) != 2 {
		return Fingerprint{}, fmt.Errorf("%w: fingerprint", ErrInvalidDescription)
	}

	return Fingerprint{Algorithm: strings.ToLower(parts[0]), Value: strings.ToUpper(parts[1])}, nil
}

func validateSetup(value string) error {
	switch value {
	case "actpass", "active", "passive", "holdconn":
		return nil
	default:
		return fmt.Errorf("%w: setup", ErrInvalidDescription)
	}
}

func requireAttributeValue(name, value string) error {
	if value == "" {
		return fmt.Errorf("%w: %s", ErrInvalidDescription, name)
	}

	return validateAttribute(value)
}

func validateAttribute(value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("%w: invalid attribute", ErrInvalidDescription)
	}

	return nil
}

func populateStaticCodecs(media *Media) {
	if media.Type != "audio" {
		return
	}

	static := map[uint8]Codec{
		0: {PayloadType: 0, Name: "PCMU", ClockRate: 8000, Channels: 1},
		3: {PayloadType: 3, Name: "GSM", ClockRate: 8000, Channels: 1},
		8: {PayloadType: 8, Name: "PCMA", ClockRate: 8000, Channels: 1},
		9: {PayloadType: 9, Name: "G722", ClockRate: 8000, Channels: 1},
	}

	for _, payloadType := range media.Formats {
		if _, exists := media.Codecs[payloadType]; exists {
			continue
		}

		if codec, exists := static[payloadType]; exists {
			media.Codecs[payloadType] = codec
		}
	}
}

func codecKey(codec Codec) string {
	key := fmt.Sprintf("%s/%d/%d", strings.ToUpper(codec.Name), codec.ClockRate, max(codec.Channels, 1))

	if strings.EqualFold(codec.Name, "iLBC") {
		mode := "30"

		for _, parameter := range strings.Split(codec.FMTP, ";") {
			name, value, found := strings.Cut(strings.TrimSpace(parameter), "=")
			if found && strings.EqualFold(name, "mode") {
				mode = strings.TrimSpace(value)
			}
		}

		key += "/mode=" + mode
	}

	return key
}

func SortCodecs(codecs []Codec) {
	sort.SliceStable(codecs, func(i, j int) bool { return codecs[i].PayloadType < codecs[j].PayloadType })
}
