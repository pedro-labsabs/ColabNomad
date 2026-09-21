package control

import (
	"context"
	"encoding/json"
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
	return ensureDaemonSocketWithLiveCheck(stateDir, start, nil)
}

func ensureCompatibleDaemon(stateDir, binaryIdentity string, start func() error) error {
	if binaryIdentity == "" {
		return fmt.Errorf("current daemon binary identity is empty")
	}
	return ensureDaemonSocketWithLiveCheck(stateDir, start, func(path string) (bool, error) {
		identityClient := Client{Socket: path, Timeout: 500 * time.Millisecond}
		resp, err := identityClient.Do(Request{Command: "identity"})
		if err == nil {
			var identity struct {
				Binary string `json:"binary"`
			}
			if jsonErr := json.Unmarshal(resp.Payload, &identity); jsonErr == nil && identity.Binary == binaryIdentity {
				return true, nil
			}
		}
		downClient := Client{Socket: path, Timeout: 30 * time.Second}
		if _, downErr := downClient.Do(Request{Command: "down"}); downErr != nil {
			return false, fmt.Errorf("stop stale daemon: %w", downErr)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			conn, dialErr := net.DialTimeout("unix", path, 100*time.Millisecond)
			if dialErr == nil {
				_ = conn.Close()
				time.Sleep(25 * time.Millisecond)
				continue
			}
			owner, ownerErr := readOwner(ownerPath(stateDir))
			if os.IsNotExist(ownerErr) || (ownerErr == nil && !ownerAlive(owner)) {
				return false, nil
			}
			if ownerErr != nil {
				return false, fmt.Errorf("verify stale daemon owner: %w", ownerErr)
			}
			time.Sleep(25 * time.Millisecond)
		}
		return false, fmt.Errorf("stale daemon did not stop within timeout")
	})
}

func ensureDaemonSocketWithLiveCheck(stateDir string, start func() error, liveCheck func(string) (bool, error)) error {
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(stateDir, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	path := stateDir + "/control.sock"
	if c, err := net.DialTimeout("unix", path, 100*time.Millisecond); err == nil {
		_ = c.Close()
		if liveCheck == nil {
			return nil
		}
		reuse, checkErr := liveCheck(path)
		if checkErr != nil {
			return checkErr
		}
		if reuse {
			return nil
		}
	}
	owner := ownerPath(stateDir)
	hasOwner, err := verifySocketReplacement(path, owner)
	if err != nil {
		return err
	}
	if err := removeSocket(path); err != nil {
		return err
	}
	if hasOwner {
		if err := removeSocket(owner); err != nil {
			return err
		}
	}
	return start()
}
func removeSocket(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// EnsureDaemon reuses only a daemon launched by the same binary identity and
// otherwise asks the stale daemon to shut down before starting a replacement.
// It deliberately does not inspect or signal any saved PID.
func EnsureDaemon(stateDir string) error {
	binaryIdentity, err := CurrentBinaryIdentity()
	if err != nil {
		return err
	}
	return ensureCompatibleDaemon(stateDir, binaryIdentity, func() error {
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
		socket := filepath.Join(stateDir, "control.sock")
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if c, err := net.DialTimeout("unix", socket, 100*time.Millisecond); err == nil {
				return c.Close()
			}
			time.Sleep(25 * time.Millisecond)
		}
		return fmt.Errorf("daemon control socket did not become ready")
	})
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
