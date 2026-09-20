//go:build linux

package main

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
)

func daemonize(stateDir string) error {
	if err := os.MkdirAll(filepath.Join(stateDir, "logs"), 0700); err != nil {
		return err
	}
	_, err := syscall.Setsid()
	return err
}
func daemonContext() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}
