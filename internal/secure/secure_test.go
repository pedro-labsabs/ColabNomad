package secure

import (
	"strings"
	"testing"
)

func TestRedactRemovesEverySecret(t *testing.T) {
	got := Redact("token=abc password=p@ss", "abc", "p@ss")
	if strings.Contains(got, "abc") || strings.Contains(got, "p@ss") {
		t.Fatal(got)
	}
}

func TestRandomPasswordHasRequestedLengthAndURLSafeCharacters(t *testing.T) {
	got, err := RandomPassword(64)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 64 || strings.Trim(got, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") != "" {
		t.Fatalf("invalid password: %q", got)
	}
}
