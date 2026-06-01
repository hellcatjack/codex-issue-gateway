package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

func VerifySignature(body []byte, header string, secret []byte) error {
	if header == "" {
		return fmt.Errorf("signature missing")
	}
	if !strings.HasPrefix(header, "sha256=") {
		return fmt.Errorf("signature scheme invalid")
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, "sha256="))
	if err != nil {
		return fmt.Errorf("signature encoding invalid")
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	want := mac.Sum(nil)
	if !hmac.Equal(got, want) {
		return fmt.Errorf("signature invalid")
	}
	return nil
}
