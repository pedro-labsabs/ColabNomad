package artifact

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/config"
)

func TestEnsureRejectsBadDigestWithoutLeavingBinary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("known bytes")) }))
	defer server.Close()
	dst := filepath.Join(t.TempDir(), "tool")
	err := (Installer{}).Ensure(context.Background(), config.Artifact{URL: server.URL, Integrity: "sha256:" + strings.Repeat("0", 64)}, dst)
	if err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("expected integrity error, got %v", err)
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Fatalf("destination exists after failed install: %v", statErr)
	}
}

func TestEnsureSupportsSHA512AndExtractsExactMember(t *testing.T) {
	archive := makeTar(t, map[string]string{"bin/tool": "payload", "other": "not selected"})
	hash := sha512.Sum512(archive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archive) }))
	defer server.Close()
	dst := filepath.Join(t.TempDir(), "tool")
	err := (Installer{}).Ensure(context.Background(), config.Artifact{URL: server.URL, Integrity: "sha512-" + base64.StdEncoding.EncodeToString(hash[:]), Member: "bin/tool"}, dst)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "payload" {
		t.Fatalf("installed %q, err=%v", got, err)
	}
}

func TestEnsureExtractsMemberFromGzipTar(t *testing.T) {
	archive := makeGzipTar(t, makeTar(t, map[string]string{"bin/tool": "gzip payload"}))
	hash := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archive) }))
	defer server.Close()
	dst := filepath.Join(t.TempDir(), "tool")
	err := (Installer{}).Ensure(context.Background(), config.Artifact{URL: server.URL, Integrity: fmt.Sprintf("sha256:%x", hash), Member: "bin/tool"}, dst)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "gzip payload" {
		t.Fatalf("installed %q, err=%v", got, err)
	}
}

func TestEnsureRejectsDuplicateMatchingMembers(t *testing.T) {
	archive := makeTarEntries(t, []tarEntry{{"bin/tool", "first"}, {"bin/tool", "second"}})
	hash := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archive) }))
	defer server.Close()
	err := (Installer{}).Ensure(context.Background(), config.Artifact{URL: server.URL, Integrity: fmt.Sprintf("sha256:%x", hash), Member: "bin/tool"}, filepath.Join(t.TempDir(), "tool"))
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate member error, got %v", err)
	}
}

func TestEnsureRejectsMissingMember(t *testing.T) {
	archive := makeTar(t, map[string]string{"present": "payload"})
	hash := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archive) }))
	defer server.Close()
	err := (Installer{}).Ensure(context.Background(), config.Artifact{URL: server.URL, Integrity: fmt.Sprintf("sha256:%x", hash), Member: "missing"}, filepath.Join(t.TempDir(), "tool"))
	if err == nil || !strings.Contains(err.Error(), "member") {
		t.Fatalf("expected missing member error, got %v", err)
	}
}

func TestEnsureRejectsArchiveTraversal(t *testing.T) {
	archive := makeTar(t, map[string]string{"../escape": "payload"})
	hash := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archive) }))
	defer server.Close()
	err := (Installer{}).Ensure(context.Background(), config.Artifact{URL: server.URL, Integrity: fmt.Sprintf("sha256:%x", hash), Member: "../escape"}, filepath.Join(t.TempDir(), "tool"))
	if err == nil || !strings.Contains(err.Error(), "traversal") {
		t.Fatalf("expected traversal error, got %v", err)
	}
}

func makeTar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	entries := make([]tarEntry, 0, len(files))
	for name, body := range files {
		entries = append(entries, tarEntry{name, body})
	}
	return makeTarEntries(t, entries)
}

type tarEntry struct{ name, body string }

func makeTarEntries(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, entry := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: entry.name, Mode: 0755, Size: int64(len(entry.body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeGzipTar(t *testing.T, archive []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(archive); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
