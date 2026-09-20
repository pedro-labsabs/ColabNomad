package deps

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/execx"
)

// System resolves external programs and installs supported Debian packages.
type System struct {
	Runner execx.Runner
	GOOS   string
	IsRoot func() bool
	// Debian is an optional test/embedding override for distro detection.
	Debian  bool
	mu      sync.Mutex
	updated bool
}

func (s *System) Ensure(ctx context.Context, binary, aptPackage string) (string, error) {
	if binary == "" || aptPackage == "" {
		return "", fmt.Errorf("binary and apt package are required")
	}
	r := s.Runner
	if r == nil {
		r = execx.OSRunner{}
	}
	if path, err := r.LookPath(binary); err == nil {
		return path, nil
	}
	goos := s.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	root := s.IsRoot
	if root == nil {
		root = func() bool { return os.Geteuid() == 0 }
	}
	if goos != "linux" || !root() || !s.isDebianLike() {
		return "", fmt.Errorf("%s is unavailable; automatic installation requires root on Debian/Ubuntu", binary)
	}
	apt, err := r.LookPath("apt-get")
	if err != nil {
		return "", fmt.Errorf("find apt-get: %w", err)
	}
	s.mu.Lock()
	needUpdate := !s.updated
	if needUpdate {
		s.updated = true
	}
	s.mu.Unlock()
	if needUpdate {
		if _, err := r.Run(ctx, execx.Spec{Path: apt, Args: []string{"-qq", "update"}}); err != nil {
			return "", fmt.Errorf("update apt indexes: %w", err)
		}
	}
	if _, err := r.Run(ctx, execx.Spec{Path: apt, Args: []string{"-qq", "install", "-y", aptPackage}}); err != nil {
		return "", fmt.Errorf("install %s: %w", aptPackage, err)
	}
	path, err := r.LookPath(binary)
	if err != nil {
		return "", fmt.Errorf("find %s after installation: %w", binary, err)
	}
	return path, nil
}

func (s *System) isDebianLike() bool {
	if s.Debian {
		return true
	}
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return false
	}
	v := strings.ToLower(string(b))
	return strings.Contains(v, "id=debian") || strings.Contains(v, "id=ubuntu") || strings.Contains(v, "id_like=debian")
}
