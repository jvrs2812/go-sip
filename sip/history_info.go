package sip

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var ErrInvalidHistoryInfo = errors.New("invalid History-Info header")

type HistoryInfoEntry struct {
	URI        string
	Index      string
	Parameters map[string]string
}

type HistoryPrivacyPolicy string

const (
	HistoryPrivacyPreserve  HistoryPrivacyPolicy = "preserve"
	HistoryPrivacyAnonymize HistoryPrivacyPolicy = "anonymize"
	HistoryPrivacyRemove    HistoryPrivacyPolicy = "remove"
)

func (m *Message) HistoryInfo() ([]HistoryInfoEntry, error) {
	return ParseHistoryInfo(m.Headers.Values("History-Info"))
}

func ParseHistoryInfo(values []string) ([]HistoryInfoEntry, error) {
	entries := make([]HistoryInfoEntry, 0)

	for _, value := range values {
		parts, err := splitHistoryEntries(value)
		if err != nil {
			return nil, err
		}

		for _, part := range parts {
			entry, err := parseHistoryEntry(part)
			if err != nil {
				return nil, err
			}

			entries = append(entries, entry)
		}
	}

	return entries, nil
}

func FormatHistoryInfo(entries []HistoryInfoEntry) (string, error) {
	formatted := make([]string, 0, len(entries))

	for _, entry := range entries {
		if strings.TrimSpace(entry.URI) == "" || strings.ContainsAny(entry.URI, "\r\n<>") {
			return "", ErrInvalidHistoryInfo
		}

		if !validHistoryIndex(entry.Index) {
			return "", ErrInvalidHistoryInfo
		}

		var builder strings.Builder
		fmt.Fprintf(&builder, "<%s>;index=%s", entry.URI, entry.Index)

		parameterNames := make([]string, 0, len(entry.Parameters))
		for name := range entry.Parameters {
			if strings.EqualFold(name, "index") {
				continue
			}

			parameterNames = append(parameterNames, name)
		}

		sort.Strings(parameterNames)

		for _, name := range parameterNames {
			value := entry.Parameters[name]
			if name == "" || strings.ContainsAny(name+value, "\r\n;,=") {
				return "", ErrInvalidHistoryInfo
			}

			builder.WriteByte(';')
			builder.WriteString(name)
			if value != "" {
				builder.WriteByte('=')
				builder.WriteString(value)
			}
		}

		formatted = append(formatted, builder.String())
	}

	return strings.Join(formatted, ", "), nil
}

func ApplyHistoryPrivacy(entries []HistoryInfoEntry, policy HistoryPrivacyPolicy, anonymousURI string) ([]HistoryInfoEntry, error) {
	switch policy {
	case "", HistoryPrivacyPreserve:
		return cloneHistoryEntries(entries), nil
	case HistoryPrivacyRemove:
		return nil, nil
	case HistoryPrivacyAnonymize:
		if anonymousURI == "" {
			anonymousURI = "sip:anonymous@anonymous.invalid"
		}

		if strings.ContainsAny(anonymousURI, "\r\n<>") {
			return nil, ErrInvalidHistoryInfo
		}

		anonymized := cloneHistoryEntries(entries)

		for index := range anonymized {
			anonymized[index].URI = anonymousURI
			delete(anonymized[index].Parameters, "rc")
			delete(anonymized[index].Parameters, "mp")
			delete(anonymized[index].Parameters, "np")
		}

		return anonymized, nil
	default:
		return nil, fmt.Errorf("%w: privacy policy %q", ErrInvalidHistoryInfo, policy)
	}
}

func cloneHistoryEntries(entries []HistoryInfoEntry) []HistoryInfoEntry {
	cloned := make([]HistoryInfoEntry, len(entries))

	for index, entry := range entries {
		parameters := make(map[string]string, len(entry.Parameters))

		for name, value := range entry.Parameters {
			parameters[name] = value
		}

		cloned[index] = HistoryInfoEntry{
			URI:        entry.URI,
			Index:      entry.Index,
			Parameters: parameters,
		}
	}

	return cloned
}

func parseHistoryEntry(value string) (HistoryInfoEntry, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "<") {
		return HistoryInfoEntry{}, ErrInvalidHistoryInfo
	}

	closing := strings.IndexByte(value, '>')
	if closing <= 1 {
		return HistoryInfoEntry{}, ErrInvalidHistoryInfo
	}

	entry := HistoryInfoEntry{
		URI:        value[1:closing],
		Parameters: make(map[string]string),
	}

	parameters := strings.TrimSpace(value[closing+1:])
	for parameters != "" {
		if parameters[0] != ';' {
			return HistoryInfoEntry{}, ErrInvalidHistoryInfo
		}

		parameters = parameters[1:]
		next := strings.IndexByte(parameters, ';')
		var rawParameter string

		if next < 0 {
			rawParameter = parameters
			parameters = ""
		} else {
			rawParameter = parameters[:next]
			parameters = parameters[next:]
		}

		name, parameterValue, found := strings.Cut(strings.TrimSpace(rawParameter), "=")
		name = strings.ToLower(strings.TrimSpace(name))
		parameterValue = strings.TrimSpace(parameterValue)

		if name == "" || (!found && name == "index") {
			return HistoryInfoEntry{}, ErrInvalidHistoryInfo
		}

		if name == "index" {
			entry.Index = parameterValue
		} else {
			entry.Parameters[name] = parameterValue
		}
	}

	if !validHistoryIndex(entry.Index) {
		return HistoryInfoEntry{}, ErrInvalidHistoryInfo
	}

	return entry, nil
}

func splitHistoryEntries(value string) ([]string, error) {
	var entries []string
	start := 0
	inAngle := false
	inQuote := false
	escaped := false

	for index, character := range value {
		if escaped {
			escaped = false

			continue
		}

		if character == '\\' && inQuote {
			escaped = true

			continue
		}

		switch character {
		case '"':
			inQuote = !inQuote
		case '<':
			if !inQuote {
				inAngle = true
			}
		case '>':
			if !inQuote {
				inAngle = false
			}
		case ',':
			if !inQuote && !inAngle {
				entries = append(entries, strings.TrimSpace(value[start:index]))
				start = index + 1
			}
		}
	}

	if inAngle || inQuote || escaped {
		return nil, ErrInvalidHistoryInfo
	}

	last := strings.TrimSpace(value[start:])
	if last != "" {
		entries = append(entries, last)
	}

	return entries, nil
}

func validHistoryIndex(value string) bool {
	if value == "" || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}

	for _, part := range strings.Split(value, ".") {
		if part == "" {
			return false
		}

		for _, character := range part {
			if character < '0' || character > '9' {
				return false
			}
		}
	}

	return true
}
