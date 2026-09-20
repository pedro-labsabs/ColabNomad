package health

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type BasicAuth struct{ Username, Password string }
type Request struct {
	URL             string
	Auth            *BasicAuth
	Headers         map[string]string
	WantStatus      int
	WantContentType string
}
type Prober struct{ Client *http.Client }

func (p Prober) Do(ctx context.Context, req Request) ([]byte, error) {
	if req.URL == "" {
		return nil, fmt.Errorf("probe URL is required")
	}
	c := p.Client
	if c == nil {
		c = http.DefaultClient
	}
	h, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("create probe request: %w", err)
	}
	if req.Auth != nil {
		h.SetBasicAuth(req.Auth.Username, req.Auth.Password)
	}
	for k, v := range req.Headers {
		h.Header.Set(k, v)
	}
	res, err := c.Do(h)
	if err != nil {
		return nil, fmt.Errorf("probe request: %w", err)
	}
	defer res.Body.Close()
	body, readErr := io.ReadAll(res.Body)
	if readErr != nil {
		return nil, fmt.Errorf("read probe response: %w", readErr)
	}
	if req.WantStatus != 0 && res.StatusCode != req.WantStatus {
		return nil, fmt.Errorf("probe returned status %d, want %d", res.StatusCode, req.WantStatus)
	}
	if req.WantContentType != "" && !strings.HasPrefix(strings.ToLower(res.Header.Get("Content-Type")), strings.ToLower(req.WantContentType)) {
		return nil, fmt.Errorf("probe returned content type %q, want %q", res.Header.Get("Content-Type"), req.WantContentType)
	}
	return body, nil
}
