package secure

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
)

const urlSafeAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"

func RandomPassword(n int) (string, error) {
	if n < 0 {
		return "", errors.New("password length cannot be negative")
	}
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate password: %w", err)
	}
	for i := range raw {
		raw[i] = urlSafeAlphabet[int(raw[i]&63)]
	}
	return string(raw), nil
}

func Redact(text string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[REDACTED]")
		}
	}
	return text
}
