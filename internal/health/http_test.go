package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProberRequiresBasicAuthAndStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "terminal" || p != "secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	p := Prober{}
	if _, err := p.Do(context.Background(), Request{URL: srv.URL, WantStatus: 200}); err == nil {
		t.Fatal("missing credentials succeeded")
	}
	if _, err := p.Do(context.Background(), Request{URL: srv.URL, Auth: &BasicAuth{Username: "bad", Password: "no"}, WantStatus: 200}); err == nil {
		t.Fatal("wrong credentials succeeded")
	}
	body, err := p.Do(context.Background(), Request{URL: srv.URL, Auth: &BasicAuth{Username: "terminal", Password: "secret"}, WantStatus: 200, WantContentType: "text/html"})
	if err != nil || string(body) != "ok" {
		t.Fatalf("body=%q err=%v", body, err)
	}
}
