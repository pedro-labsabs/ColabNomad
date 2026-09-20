package control

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
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
}
