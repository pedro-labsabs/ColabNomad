package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreWritesStateAndCredentials0600(t *testing.T) {
	dir := t.TempDir()
	store := Store{Dir: dir}
	want := RuntimeState{SchemaVersion: 1, DaemonPID: 42, WorkspacePath: "/tmp/work", Services: map[string]ServiceState{"web": {PID: 7}}, Endpoints: map[string]string{"web": "http://127.0.0.1"}, UpdatedAt: time.Unix(123, 0).UTC()}
	creds := Credentials{OpenCodeUser: "user", OpenCodePassword: "password", TerminalUser: "term", TerminalPassword: "secret"}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveCredentials(creds); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"state.json", "credentials.json"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("%s mode = %o", name, info.Mode().Perm())
		}
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != want.SchemaVersion || got.DaemonPID != want.DaemonPID || got.WorkspacePath != want.WorkspacePath || got.Services["web"].PID != 7 {
		t.Fatalf("state round trip: %+v", got)
	}
	gotCreds, err := store.LoadCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if gotCreds != creds {
		t.Fatalf("credentials round trip: %+v", gotCreds)
	}
}

func TestRuntimeStateDoesNotPersistGitHubToken(t *testing.T) {
	token := "tok:@,;\nsecret"
	store := Store{Dir: t.TempDir()}
	if err := store.Save(RuntimeState{WorkspacePath: "/tmp/work"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(store.Dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), token) {
		t.Fatalf("token persisted in runtime state: %q", b)
	}
}
