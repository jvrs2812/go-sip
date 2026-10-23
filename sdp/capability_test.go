package sdp

import "testing"

func TestCapabilityNegotiationSelectsCompatibleConfiguration(t *testing.T) {
	raw := "v=0\r\n" +
		"o=- 1 1 IN IP4 192.0.2.10\r\n" +
		"s=-\r\n" +
		"c=IN IP4 192.0.2.10\r\n" +
		"t=0 0\r\n" +
		"a=csup:tcap acap\r\n" +
		"a=tcap:1 RTP/AVP UDP/TLS/RTP/SAVP\r\n" +
		"a=acap:1 ptime:20\r\n" +
		"a=pcfg:1 t=1 a=1\r\n" +
		"m=audio 4000 RTP/AVP 0\r\n"

	description, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}

	capabilities, err := ParseCapabilities(description.Attributes)
	if err != nil {
		t.Fatal(err)
	}

	selected, err := capabilities.Select(
		map[string]bool{"UDP/TLS/RTP/SAVP": true},
		func(attribute Attribute) bool {
			return attribute.Name == "ptime" && attribute.Value == "20"
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if selected.ID != 1 || selected.Transport != "UDP/TLS/RTP/SAVP" || len(selected.Attributes) != 1 {
		t.Fatalf("unexpected selection: %#v", selected)
	}

	encoded, err := description.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	parsedAgain, err := Parse(encoded)
	if err != nil {
		t.Fatal(err)
	}

	if len(parsedAgain.Attributes) != len(description.Attributes) {
		t.Fatalf("generic capabilities were not preserved: %#v", parsedAgain.Attributes)
	}
}
