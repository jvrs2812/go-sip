package presence

import (
	"strings"
	"testing"
	"time"
)

func TestPIDFRoundTrip(t *testing.T) {
	priority := 0.8
	timestamp := time.Date(2026, time.October, 23, 12, 30, 0, 0, time.UTC)
	document := Document{
		Entity: "sip:alice@example.com",
		Tuples: []Tuple{
			{
				ID:    "desk",
				Basic: BasicOpen,
				Contact: &Contact{
					URI:      "sip:alice@desk.example.com",
					Priority: &priority,
				},
				Notes:     []Note{{Language: "pt-BR", Text: "Disponível"}},
				Timestamp: timestamp,
			},
		},
		Notes: []Note{{Language: "en", Text: "Office presence"}},
	}

	encoded, err := MarshalPIDF(document)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(encoded), `xmlns="urn:ietf:params:xml:ns:pidf"`) {
		t.Fatalf("PIDF namespace missing:\n%s", encoded)
	}

	parsed, err := ParsePIDF(encoded)
	if err != nil {
		t.Fatal(err)
	}

	if parsed.Entity != document.Entity || len(parsed.Tuples) != 1 {
		t.Fatalf("parsed document = %#v", parsed)
	}

	tuple := parsed.Tuples[0]
	if tuple.Basic != BasicOpen || tuple.Contact == nil || tuple.Contact.Priority == nil || *tuple.Contact.Priority != priority {
		t.Fatalf("parsed tuple = %#v", tuple)
	}

	if tuple.Timestamp != timestamp || tuple.Notes[0].Language != "pt-BR" {
		t.Fatalf("tuple metadata = %#v", tuple)
	}
}

func TestPIDFValidation(t *testing.T) {
	tests := []struct {
		name     string
		document Document
	}{
		{name: "missing entity", document: Document{}},
		{name: "missing tuple ID", document: Document{Entity: "sip:a@example.com", Tuples: []Tuple{{Basic: BasicOpen}}}},
		{name: "invalid status", document: Document{Entity: "sip:a@example.com", Tuples: []Tuple{{ID: "one", Basic: "busy"}}}},
		{name: "duplicate tuple", document: Document{Entity: "sip:a@example.com", Tuples: []Tuple{{ID: "one", Basic: BasicOpen}, {ID: "one", Basic: BasicClosed}}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := MarshalPIDF(test.document); err == nil {
				t.Fatal("MarshalPIDF() error = nil")
			}
		})
	}
}

func TestParsePIDFRejectsWrongNamespace(t *testing.T) {
	data := []byte(`<presence xmlns="urn:example" entity="sip:a@example.com"></presence>`)

	if _, err := ParsePIDF(data); err == nil {
		t.Fatal("ParsePIDF() error = nil")
	}
}
