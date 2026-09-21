package browserauth

import (
	"crypto/subtle"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

const (
	LoginPath         = "/__colabnomad/login"
	HealthPath        = "/__colabnomad/health"
	SessionCookieName = "colabnomad_session"
)

type Config struct {
	BackendURL   string
	Username     string
	Password     string
	SessionToken string
}

type handler struct {
	username     string
	password     string
	sessionToken string
	proxy        *httputil.ReverseProxy
}

func NewHandler(cfg Config) (http.Handler, error) {
	backend, err := url.Parse(cfg.BackendURL)
	if err != nil || backend.Host == "" || (backend.Scheme != "http" && backend.Scheme != "https") || backend.User != nil || !isLoopbackHost(backend.Hostname()) {
		return nil, fmt.Errorf("invalid backend URL: loopback HTTP(S) endpoint required")
	}
	if cfg.Username == "" || cfg.Password == "" || cfg.SessionToken == "" {
		return nil, fmt.Errorf("gateway credentials and session token are required")
	}
	proxy := httputil.NewSingleHostReverseProxy(backend)
	originalDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		originalDirector(r)
		r.Host = backend.Host
		stripCookie(r, SessionCookieName)
		r.Header.Del("Authorization")
		r.SetBasicAuth(cfg.Username, cfg.Password)
		if origin := r.Header.Get("Origin"); origin != "" {
			r.Header.Set("Origin", backend.Scheme+"://"+backend.Host)
		}
	}
	proxy.FlushInterval = -1
	proxy.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Del("WWW-Authenticate")
		return nil
	}
	return &handler{username: cfg.Username, password: cfg.Password, sessionToken: cfg.SessionToken, proxy: proxy}, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == HealthPath {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
		return
	}
	if r.URL.Path == LoginPath {
		h.serveLogin(w, r)
		return
	}
	if !h.authenticated(r) {
		if r.Method == http.MethodGet && acceptsHTML(r) {
			h.renderLogin(w, http.StatusOK, "")
			return
		}
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	h.proxy.ServeHTTP(w, r)
}

func (h *handler) serveLogin(w http.ResponseWriter, r *http.Request) {
	setLoginSecurityHeaders(w.Header())
	if r.Method != http.MethodPost {
		if r.Method == http.MethodGet {
			h.renderLogin(w, http.StatusOK, "")
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderLogin(w, http.StatusBadRequest, "Invalid request")
		return
	}
	if !secureEqual(r.Form.Get("username"), h.username) || !secureEqual(r.Form.Get("password"), h.password) {
		h.renderLogin(w, http.StatusUnauthorized, "Invalid username or password")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    h.sessionToken,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	w.Header().Set("Location", "/")
	w.WriteHeader(http.StatusSeeOther)
}

func (h *handler) authenticated(r *http.Request) bool {
	if cookie, err := r.Cookie(SessionCookieName); err == nil && secureEqual(cookie.Value, h.sessionToken) {
		return true
	}
	username, password, ok := r.BasicAuth()
	return ok && secureEqual(username, h.username) && secureEqual(password, h.password)
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func stripCookie(r *http.Request, name string) {
	cookies := r.Cookies()
	r.Header.Del("Cookie")
	for _, cookie := range cookies {
		if cookie.Name == name {
			continue
		}
		r.AddCookie(cookie)
	}
}

func secureEqual(got, want string) bool {
	if len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func acceptsHTML(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return accept == "" || strings.Contains(accept, "text/html")
}

func setLoginSecurityHeaders(h http.Header) {
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
}

var loginTemplate = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>ColabNomad Login</title>
<style>body{font-family:system-ui,sans-serif;max-width:28rem;margin:10vh auto;padding:1.5rem}form{display:grid;gap:.8rem}input,button{font:inherit;padding:.7rem}.error{color:#a00}</style></head>
<body><h1>ColabNomad</h1><p>Sign in to OpenCode.</p>{{if .}}<p class="error">{{.}}</p>{{end}}
<form method="post" action="` + LoginPath + `"><label>Username <input name="username" autocomplete="username" required></label><label>Password <input name="password" type="password" autocomplete="current-password" required></label><button type="submit">Sign in</button></form></body></html>`))

func (h *handler) renderLogin(w http.ResponseWriter, status int, message string) {
	setLoginSecurityHeaders(w.Header())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = loginTemplate.Execute(w, message)
}
