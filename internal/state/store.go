package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Store struct{ Dir string }

func (s Store) Load() (RuntimeState, error) {
	var value RuntimeState
	if err := loadJSON(filepath.Join(s.Dir, "state.json"), &value); err != nil {
		return value, fmt.Errorf("load runtime state: %w", err)
	}
	return value, nil
}

func (s Store) Save(value RuntimeState) error {
	return s.saveJSON("state.json", value)
}

func (s Store) LoadCredentials() (Credentials, error) {
	var value Credentials
	if err := loadJSON(filepath.Join(s.Dir, "credentials.json"), &value); err != nil {
		return value, fmt.Errorf("load credentials: %w", err)
	}
	return value, nil
}

func (s Store) SaveCredentials(value Credentials) error { return s.saveJSON("credentials.json", value) }

func (s Store) saveJSON(name string, value any) error {
	if s.Dir == "" {
		return fmt.Errorf("state directory is empty")
	}
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", name, err)
	}
	tmp, err := os.CreateTemp(s.Dir, "."+name+"-*")
	if err != nil {
		return fmt.Errorf("create temporary %s: %w", name, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temporary %s: %w", name, err)
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", name, err)
	}
	if err := os.Rename(tmpName, filepath.Join(s.Dir, name)); err != nil {
		return fmt.Errorf("replace %s: %w", name, err)
	}
	return syncDir(s.Dir)
}

func loadJSON(path string, dst any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	return nil
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
