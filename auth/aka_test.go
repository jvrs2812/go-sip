package auth

import (
	"encoding/hex"
	"testing"
)

func TestDeriveAKAv2MD5(t *testing.T) {
	result := AKAResult{
		Response:     decodeHex(t, "01020304"),
		IntegrityKey: decodeHex(t, "101112131415161718191a1b1c1d1e1f"),
		CipherKey:    decodeHex(t, "202122232425262728292a2b2c2d2e2f"),
	}

	keys, err := DeriveAKAv2("AKAv2-MD5", result)
	if err != nil {
		t.Fatal(err)
	}

	if keys.Password != "RNzYRwOHYe+PObZ8FRycMg==" {
		t.Fatalf("password = %q", keys.Password)
	}

	if hex.EncodeToString(keys.IntegrityKey) != "df2ecde13c2796ac7fc58abe17287c6e" {
		t.Fatalf("integrity key = %x", keys.IntegrityKey)
	}

	if hex.EncodeToString(keys.CipherKey) != "0da076227f44827cd5b1fa89015b04d4" {
		t.Fatalf("cipher key = %x", keys.CipherKey)
	}
}

func decodeHex(t *testing.T, value string) []byte {
	t.Helper()

	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}

	return decoded
}
