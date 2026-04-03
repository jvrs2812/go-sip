package internal

import (
	"crypto/md5"
	"fmt"
)

func md5Hash(text string) string {
	hash := md5.Sum([]byte(text))
	return fmt.Sprintf("%x", hash)
}

func CalculateResponse(user, realm, password, method, uri, nonce string) string {
	ha1 := md5Hash(fmt.Sprintf("%s:%s:%s", user, realm, password))

	ha2 := md5Hash(fmt.Sprintf("%s:%s", method, uri))

	return md5Hash(fmt.Sprintf("%s:%s:%s", ha1, nonce, ha2))
}
