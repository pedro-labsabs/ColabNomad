package control

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
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
