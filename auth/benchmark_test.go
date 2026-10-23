package auth

import "testing"

func BenchmarkRegisterDigestSHA256(b *testing.B) {
	challenge := Challenge{
		Realm:     "example.test",
		Nonce:     "benchmark-nonce",
		Algorithm: "SHA-256",
		QOP:       []string{"auth"},
	}
	credentials := Credentials{Username: "1001", Password: "secret"}
	request := Request{
		Method:     "REGISTER",
		URI:        "sip:example.test",
		NonceCount: 1,
		CNonce:     "benchmark-cnonce",
	}

	b.ReportAllocs()

	for index := 0; index < b.N; index++ {
		if _, err := Authorization(challenge, credentials, request); err != nil {
			b.Fatal(err)
		}
	}
}
