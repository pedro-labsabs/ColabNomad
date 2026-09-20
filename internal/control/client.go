package control

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

type Client struct {
	Socket  string
	Timeout time.Duration
}

func (c Client) Do(req Request) (Response, error) {
	var out Response
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	conn, err := net.DialTimeout("unix", c.Socket, timeout)
	if err != nil {
		return out, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if err = writeMessage(conn, req); err != nil {
		return out, err
	}
	if err = readMessage(conn, &out); err != nil {
		return out, err
	}
	if !out.OK {
		return out, fmt.Errorf("control: %s", out.Error)
	}
	return out, nil
}

func ensureDaemonSocket(stateDir string, _ int, start func() error) error {
	path := stateDir + "/control.sock"
	if c, err := net.DialTimeout("unix", path, 100*time.Millisecond); err == nil {
		c.Close()
		return nil
	}
	if err := removeSocket(path); err != nil {
		return err
	}
	return start()
}
func removeSocket(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// EnsureDaemon reuses a reachable daemon and otherwise starts a detached
// instance. It deliberately does not inspect or signal any saved PID.
func EnsureDaemon(stateDir string) error {
	socket := filepath.Join(stateDir, "control.sock")
	if c, err := net.DialTimeout("unix", socket, 100*time.Millisecond); err == nil {
		return c.Close()
	}
	if err := removeSocket(socket); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "logs"), 0700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(stateDir, "logs", "daemon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	cmd := exec.Command(os.Args[0], "daemon", "--state-dir", stateDir)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return err
	}
	_ = logFile.Close()
	if err := cmd.Process.Release(); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("unix", socket, 100*time.Millisecond); err == nil {
			return c.Close()
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("daemon control socket did not become ready")
}

func (c Client) DoContext(ctx context.Context, req Request) (Response, error) {
	var out Response
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return out, err
	}
	defer conn.Close()
	if err = writeMessage(conn, req); err != nil {
		return out, err
	}
	if err = readMessage(conn, &out); err != nil {
		return out, err
	}
	if !out.OK {
		return out, fmt.Errorf("control: %s", out.Error)
	}
	return out, nil
}
