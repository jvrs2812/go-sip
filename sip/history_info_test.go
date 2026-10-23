package sip

import "testing"

func TestHistoryInfoParseAndFormat(t *testing.T) {
	entries, err := ParseHistoryInfo([]string{
		`<sip:alice@example.test>;index=1;rc=486, <sip:bob@example.test?Reason=SIP%3Bcause%3D302>;index=1.1`,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 2 || entries[0].Index != "1" || entries[0].Parameters["rc"] != "486" || entries[1].Index != "1.1" {
		t.Fatalf("unexpected entries: %#v", entries)
	}

	formatted, err := FormatHistoryInfo(entries)
	if err != nil {
		t.Fatal(err)
	}

	reparsed, err := ParseHistoryInfo([]string{formatted})
	if err != nil {
		t.Fatal(err)
	}

	if len(reparsed) != 2 || reparsed[1].URI != entries[1].URI {
		t.Fatalf("round trip changed entries: %#v", reparsed)
	}
}

func TestHistoryInfoRejectsInvalidIndex(t *testing.T) {
	_, err := ParseHistoryInfo([]string{`<sip:alice@example.test>;index=1..2`})
	if err == nil {
		t.Fatal("expected invalid History-Info index to fail")
	}
}

func TestHistoryInfoPrivacyAnonymizesRoutingData(t *testing.T) {
	entries := []HistoryInfoEntry{{
		URI:   "sip:alice@example.test",
		Index: "1",
		Parameters: map[string]string{
			"rc":     "486",
			"custom": "retained",
		},
	}}

	privateEntries, err := ApplyHistoryPrivacy(entries, HistoryPrivacyAnonymize, "")
	if err != nil {
		t.Fatal(err)
	}

	if privateEntries[0].URI != "sip:anonymous@anonymous.invalid" || privateEntries[0].Parameters["rc"] != "" || privateEntries[0].Parameters["custom"] != "retained" {
		t.Fatalf("unexpected private History-Info: %#v", privateEntries)
	}

	if entries[0].URI != "sip:alice@example.test" || entries[0].Parameters["rc"] != "486" {
		t.Fatal("privacy processing modified the source entries")
	}
}
