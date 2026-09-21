package control

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const ownerFileName = "daemon.owner"

type ownerIdentity struct {
	PID       int
	StartTime uint64
}

func currentOwnerIdentity() (ownerIdentity, error) {
	return readProcessIdentity(os.Getpid())
}

func readProcessIdentity(pid int) (ownerIdentity, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return ownerIdentity{}, err
	}
	close := strings.LastIndexByte(string(data), ')')
	if close < 0 {
		return ownerIdentity{}, fmt.Errorf("invalid process stat")
	}
	fields := strings.Fields(string(data)[close+1:])
	if len(fields) <= 19 {
		return ownerIdentity{}, fmt.Errorf("invalid process stat")
	}
	startTime, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return ownerIdentity{}, fmt.Errorf("parse process start time: %w", err)
	}
	return ownerIdentity{PID: pid, StartTime: startTime}, nil
}

func ownerPath(stateDir string) string {
	return filepath.Join(stateDir, ownerFileName)
}

func readOwner(path string) (ownerIdentity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ownerIdentity{}, err
	}
	var identity ownerIdentity
	if _, err := fmt.Sscanf(string(data), "%d %d", &identity.PID, &identity.StartTime); err != nil || identity.PID <= 0 {
		return ownerIdentity{}, fmt.Errorf("invalid daemon owner identity")
	}
	return identity, nil
}

func ownerAlive(identity ownerIdentity) bool {
	current, err := readProcessIdentity(identity.PID)
	return err == nil && current == identity
}

func writeOwner(path string, identity ownerIdentity) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".daemon.owner-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := fmt.Fprintf(tmp, "%d %d\n", identity.PID, identity.StartTime); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func sameOwner(path string, identity ownerIdentity) bool {
	got, err := readOwner(path)
	return err == nil && got == identity
}
