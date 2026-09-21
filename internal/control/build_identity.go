package control

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

var (
	processBinaryIdentity    string
	processBinaryIdentityErr error
)

func init() {
	processBinaryIdentity, processBinaryIdentityErr = computeCurrentBinaryIdentity()
}

func computeCurrentBinaryIdentity() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve current executable: %w", err)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open current executable: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash current executable: %w", err)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// CurrentBinaryIdentity is captured once at process startup so an already-running
// daemon keeps the identity of the binary that actually launched it even if the
// on-disk executable is replaced by a newer bootstrap build.
func CurrentBinaryIdentity() (string, error) {
	if processBinaryIdentityErr != nil {
		return "", processBinaryIdentityErr
	}
	return processBinaryIdentity, nil
}
