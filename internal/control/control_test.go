package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProtocolRoundTrip(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	done := make(chan error, 1)
	go func() {
		done <- serveConn(right, func(r Request) Response {
			return Response{OK: r.Command == "status", Payload: json.RawMessage(`{"ok":true}`)}
		})
	}()
	if err := writeMessage(left, Request{Command: "status"}); err != nil {
		t.Fatal(err)
	}
	var got Response
	if err := readMessage(left, &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK {
		t.Fatalf("response not ok: %#v", got)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestEnsureDaemonRemovesUndialableSocketWithoutKillingStoredPID(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "control.sock")
	if err := os.WriteFile(socket, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	pid := os.Getpid()
	if err := ensureDaemonSocket(dir, pid, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatalf("stale socket remains: %v", err)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("stored live helper was signaled: %v", err)
	}
}

func TestEnsureDaemonRefusesUndialableSocketWithLiveOwner(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "control.sock")
	createUndialableSocket(t, socket)
	writeTestOwner(t, dir, os.Getpid(), currentProcessStartTime(t))
	starts := 0
	if err := ensureDaemonSocket(dir, 0, func() error {
		starts++
		return nil
	}); err == nil {
		t.Fatal("live owner with undialable socket was accepted")
	}
	if starts != 0 {
		t.Fatalf("start invoked %d times", starts)
	}
	if _, err := os.Stat(socket); err != nil {
		t.Fatalf("undialable socket was removed: %v", err)
	}
}

func TestEnsureDaemonReplacesSocketWithDeadOwner(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "control.sock")
	createUndialableSocket(t, socket)
	writeTestOwner(t, dir, 999999, 1)
	starts := 0
	if err := ensureDaemonSocket(dir, 0, func() error {
		starts++
		_, err := net.Listen("unix", socket)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if starts != 1 {
		t.Fatalf("start invoked %d times", starts)
	}
}

func TestEnsureDaemonTreatsWrongCurrentPIDStartTimeAsStale(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "control.sock")
	createUndialableSocket(t, socket)
	writeTestOwner(t, dir, os.Getpid(), currentProcessStartTime(t)+1)
	starts := 0
	if err := ensureDaemonSocket(dir, 0, func() error {
		starts++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if starts != 1 {
		t.Fatalf("start invoked %d times", starts)
	}
}

func TestServerOwnerIdentityModeAndOwnershipSafeClose(t *testing.T) {
	dir := t.TempDir()
	s, err := NewServer(dir, func(Request) Response { return Response{OK: true} })
	if err != nil {
		t.Fatal(err)
	}
	owner := filepath.Join(dir, "daemon.owner")
	info, err := os.Stat(owner)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("owner mode is %o, want 0600", info.Mode().Perm())
	}
	if err := os.WriteFile(owner, []byte("new-owner\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(owner)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new-owner\n" {
		t.Fatalf("new owner was removed: %q", data)
	}
}

func writeTestOwner(t *testing.T, dir string, pid int, startTime uint64) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "daemon.owner"), []byte(fmt.Sprintf("%d %d\n", pid, startTime)), 0600); err != nil {
		t.Fatal(err)
	}
}

func createUndialableSocket(t *testing.T, path string) {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}

func currentProcessStartTime(t *testing.T) uint64 {
	t.Helper()
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(data))
	startTime, err := strconv.ParseUint(fields[21], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return startTime
}

func TestServerCloseRemovesSocketAndNewServerPreservesLiveSocket(t *testing.T) {
	dir := t.TempDir()
	s, err := NewServer(dir, func(Request) Response { return Response{OK: true} })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewServer(dir, nil); err == nil {
		t.Fatal("live daemon socket was replaced")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "control.sock")); !os.IsNotExist(err) {
		t.Fatalf("socket remains: %v", err)
	}
}

func TestServerRejectsMessagesOverOneMiB(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	done := make(chan error, 1)
	go func() { done <- serveConn(right, func(Request) Response { return Response{OK: true} }) }()
	_, _ = left.Write(append([]byte(`{"command":"`), make([]byte, maxMessageSize+1)...))
	_ = left.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("server did not reject oversized message")
	}
}

func TestServerServeStopsWhenContextCancelled(t *testing.T) {
	s, err := NewServer(t.TempDir(), func(Request) Response { return Response{OK: true} })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("serve did not stop")
	}
}

func TestEnsureDaemonSocketSerializesConcurrentStarts(t *testing.T) {
	dir := t.TempDir()
	var starts int
	var listener net.Listener
	start := func() error {
		starts++
		var err error
		listener, err = net.Listen("unix", filepath.Join(dir, "control.sock"))
		if err != nil {
			return err
		}
		return nil
	}
	defer func() {
		if listener != nil {
			listener.Close()
		}
	}()
	done := make(chan error, 2)
	go func() { done <- ensureDaemonSocket(dir, 0, start) }()
	go func() { done <- ensureDaemonSocket(dir, 0, start) }()
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if starts != 1 {
		t.Fatalf("start invoked %d times", starts)
	}
}
