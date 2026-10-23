package presence

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
	"time"
)

const PIDFContentType = "application/pidf+xml"

const pidfNamespace = "urn:ietf:params:xml:ns:pidf"

type Basic string

const (
	BasicOpen   Basic = "open"
	BasicClosed Basic = "closed"
)

type Document struct {
	Entity string
	Tuples []Tuple
	Notes  []Note
}

type Tuple struct {
	ID        string
	Basic     Basic
	Contact   *Contact
	Notes     []Note
	Timestamp time.Time
}

type Contact struct {
	URI      string
	Priority *float64
}

type Note struct {
	Language string
	Text     string
}

func ParsePIDF(data []byte) (Document, error) {
	var wire pidfDocument

	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = true

	if err := decoder.Decode(&wire); err != nil {
		return Document{}, fmt.Errorf("parse PIDF: %w", err)
	}

	if wire.XMLName.Space != pidfNamespace || wire.XMLName.Local != "presence" {
		return Document{}, errors.New("parse PIDF: invalid presence namespace or root element")
	}

	document := Document{
		Entity: strings.TrimSpace(wire.Entity),
		Tuples: make([]Tuple, 0, len(wire.Tuples)),
		Notes:  notesFromWire(wire.Notes),
	}

	for _, wireTuple := range wire.Tuples {
		tuple := Tuple{
			ID:    strings.TrimSpace(wireTuple.ID),
			Basic: Basic(strings.ToLower(strings.TrimSpace(wireTuple.Status.Basic))),
			Notes: notesFromWire(wireTuple.Notes),
		}

		if wireTuple.Contact != nil {
			tuple.Contact = &Contact{
				URI:      strings.TrimSpace(wireTuple.Contact.URI),
				Priority: wireTuple.Contact.Priority,
			}
		}

		if timestamp := strings.TrimSpace(wireTuple.Timestamp); timestamp != "" {
			parsed, err := time.Parse(time.RFC3339Nano, timestamp)
			if err != nil {
				return Document{}, fmt.Errorf("parse PIDF tuple %q timestamp: %w", tuple.ID, err)
			}

			tuple.Timestamp = parsed
		}

		document.Tuples = append(document.Tuples, tuple)
	}

	if err := document.Validate(); err != nil {
		return Document{}, fmt.Errorf("parse PIDF: %w", err)
	}

	return document, nil
}

func MarshalPIDF(document Document) ([]byte, error) {
	if err := document.Validate(); err != nil {
		return nil, fmt.Errorf("marshal PIDF: %w", err)
	}

	wire := pidfDocument{
		XMLName: xml.Name{Space: pidfNamespace, Local: "presence"},
		Entity:  document.Entity,
		Tuples:  make([]pidfTuple, 0, len(document.Tuples)),
		Notes:   notesToWire(document.Notes),
	}

	for _, tuple := range document.Tuples {
		wireTuple := pidfTuple{
			ID: tuple.ID,
			Status: pidfStatus{
				Basic: string(tuple.Basic),
			},
			Notes: notesToWire(tuple.Notes),
		}

		if tuple.Contact != nil {
			wireTuple.Contact = &pidfContact{
				Priority: tuple.Contact.Priority,
				URI:      tuple.Contact.URI,
			}
		}

		if !tuple.Timestamp.IsZero() {
			wireTuple.Timestamp = tuple.Timestamp.Format(time.RFC3339Nano)
		}

		wire.Tuples = append(wire.Tuples, wireTuple)
	}

	encoded, err := xml.MarshalIndent(wire, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal PIDF: %w", err)
	}

	result := make([]byte, 0, len(xml.Header)+len(encoded)+1)
	result = append(result, xml.Header...)
	result = append(result, encoded...)
	result = append(result, '\n')

	return result, nil
}

func (document Document) Validate() error {
	if strings.TrimSpace(document.Entity) == "" {
		return errors.New("presence entity is required")
	}

	seenIDs := make(map[string]struct{}, len(document.Tuples))

	for index, tuple := range document.Tuples {
		if strings.TrimSpace(tuple.ID) == "" {
			return fmt.Errorf("tuple %d ID is required", index)
		}

		if _, exists := seenIDs[tuple.ID]; exists {
			return fmt.Errorf("tuple ID %q is duplicated", tuple.ID)
		}

		seenIDs[tuple.ID] = struct{}{}

		if tuple.Basic != BasicOpen && tuple.Basic != BasicClosed {
			return fmt.Errorf("tuple %q has invalid basic status %q", tuple.ID, tuple.Basic)
		}

		if tuple.Contact != nil {
			if strings.TrimSpace(tuple.Contact.URI) == "" {
				return fmt.Errorf("tuple %q contact URI is required", tuple.ID)
			}

			if tuple.Contact.Priority != nil && (*tuple.Contact.Priority < 0 || *tuple.Contact.Priority > 1) {
				return fmt.Errorf("tuple %q contact priority must be between 0 and 1", tuple.ID)
			}
		}
	}

	return nil
}

type pidfDocument struct {
	XMLName xml.Name    `xml:"urn:ietf:params:xml:ns:pidf presence"`
	Entity  string      `xml:"entity,attr"`
	Tuples  []pidfTuple `xml:"tuple"`
	Notes   []pidfNote  `xml:"note"`
}

type pidfTuple struct {
	ID        string       `xml:"id,attr"`
	Status    pidfStatus   `xml:"status"`
	Contact   *pidfContact `xml:"contact,omitempty"`
	Notes     []pidfNote   `xml:"note"`
	Timestamp string       `xml:"timestamp,omitempty"`
}

type pidfStatus struct {
	Basic string `xml:"basic"`
}

type pidfContact struct {
	Priority *float64 `xml:"priority,attr,omitempty"`
	URI      string   `xml:",chardata"`
}

type pidfNote struct {
	Language string `xml:"http://www.w3.org/XML/1998/namespace lang,attr,omitempty"`
	Text     string `xml:",chardata"`
}

func notesFromWire(notes []pidfNote) []Note {
	result := make([]Note, 0, len(notes))

	for _, note := range notes {
		result = append(result, Note{
			Language: note.Language,
			Text:     strings.TrimSpace(note.Text),
		})
	}

	return result
}

func notesToWire(notes []Note) []pidfNote {
	result := make([]pidfNote, 0, len(notes))

	for _, note := range notes {
		result = append(result, pidfNote{
			Language: note.Language,
			Text:     note.Text,
		})
	}

	return result
}
