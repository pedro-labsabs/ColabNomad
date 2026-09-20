package artifact

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/config"
)

type Installer struct{ Client *http.Client }

func (i Installer) Ensure(ctx context.Context, a config.Artifact, dst string) error {
	algorithm, expected, err := parseIntegrity(a.Integrity)
	if err != nil {
		return err
	}
	client := i.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return fmt.Errorf("create artifact request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download artifact: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download artifact: HTTP status %s", resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("create artifact directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".artifact-*")
	if err != nil {
		return fmt.Errorf("create artifact temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	var hasher hash.Hash = sha256.New()
	if algorithm == "sha512" {
		hasher = sha512.New()
	}
	if _, err := io.Copy(io.MultiWriter(tmp, hasher), resp.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("download artifact body: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close artifact temporary file: %w", err)
	}
	if !equalDigest(hasher.Sum(nil), expected) {
		return fmt.Errorf("artifact integrity mismatch")
	}
	if a.Member == "" {
		if err := os.Chmod(tmpName, 0755); err != nil {
			return fmt.Errorf("chmod artifact: %w", err)
		}
	} else if err := extractMember(tmpName, a.Member, dst); err != nil {
		return err
	}
	if a.Member != "" {
		return nil
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return fmt.Errorf("install artifact: %w", err)
	}
	return nil
}

func parseIntegrity(value string) (string, []byte, error) {
	switch {
	case strings.HasPrefix(value, "sha256:"):
		decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
		if err != nil || len(decoded) != sha256.Size {
			return "", nil, fmt.Errorf("invalid sha256 integrity")
		}
		return "sha256", decoded, nil
	case strings.HasPrefix(value, "sha512-"):
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "sha512-"))
		if err != nil || len(decoded) != sha512.Size {
			return "", nil, fmt.Errorf("invalid sha512 integrity")
		}
		return "sha512", decoded, nil
	default:
		return "", nil, fmt.Errorf("unsupported integrity format")
	}
}

func equalDigest(got, want []byte) bool {
	if len(got) != len(want) {
		return false
	}
	var diff byte
	for n := range got {
		diff |= got[n] ^ want[n]
	}
	return diff == 0
}

func extractMember(archivePath, member, dst string) error {
	if unsafeTarName(member) {
		return fmt.Errorf("archive member traversal: %q", member)
	}
	in, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open artifact archive: %w", err)
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(dst), ".artifact-extract-*")
	if err != nil {
		return fmt.Errorf("create extracted artifact: %w", err)
	}
	outName, found := out.Name(), false
	defer os.Remove(outName)
	buffered := bufio.NewReader(in)
	archiveReader := io.Reader(buffered)
	var gzipReader *gzip.Reader
	if signature, peekErr := buffered.Peek(2); peekErr == nil && signature[0] == 0x1f && signature[1] == 0x8b {
		gzipReader, err = gzip.NewReader(buffered)
		if err != nil {
			out.Close()
			return fmt.Errorf("open gzip artifact archive: %w", err)
		}
		defer gzipReader.Close()
		archiveReader = gzipReader
	}
	tr := tar.NewReader(archiveReader)
	for {
		hdr, readErr := tr.Next()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			out.Close()
			return fmt.Errorf("read artifact archive: %w", readErr)
		}
		if unsafeTarName(hdr.Name) {
			out.Close()
			return fmt.Errorf("archive member traversal: %q", hdr.Name)
		}
		if hdr.Name != member {
			continue
		}
		if found {
			out.Close()
			return fmt.Errorf("duplicate artifact member %q", member)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			out.Close()
			return fmt.Errorf("artifact member is not a regular file")
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return fmt.Errorf("extract artifact member: %w", err)
		}
		found = true
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close extracted artifact: %w", err)
	}
	if !found {
		return fmt.Errorf("artifact member %q not found", member)
	}
	if err := os.Chmod(outName, 0755); err != nil {
		return fmt.Errorf("chmod extracted artifact: %w", err)
	}
	if err := os.Rename(outName, dst); err != nil {
		return fmt.Errorf("install extracted artifact: %w", err)
	}
	return nil
}

func unsafeTarName(name string) bool {
	if name == "" || filepath.IsAbs(name) || strings.Contains(name, `\`) {
		return true
	}
	clean := filepath.Clean(name)
	return clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean != name
}
