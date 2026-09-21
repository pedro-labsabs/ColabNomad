package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pedroteste00000008-stack/ColabNomad/internal/health"
)

func ProbeSSE(ctx context.Context, client *http.Client, endpoint string, auth *health.BasicAuth, firstFrame time.Duration) error {
	return ProbeSSEWithHeaders(ctx, client, endpoint, auth, nil, firstFrame)
}

func ProbeSSEWithHeaders(ctx context.Context, client *http.Client, endpoint string, auth *health.BasicAuth, headers map[string]string, firstFrame time.Duration) error {
	if client == nil {
		client = http.DefaultClient
	}
	requestCtx, requestCancel := context.WithCancel(ctx)
	defer requestCancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create SSE request: %w", err)
	}
	request.Header.Set("Accept", "text/event-stream")
	if auth != nil {
		request.SetBasicAuth(auth.Username, auth.Password)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("SSE request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("SSE returned status %d", response.StatusCode)
	}
	if !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		return fmt.Errorf("SSE returned content type %q", response.Header.Get("Content-Type"))
	}

	frameCtx := ctx
	var cancel context.CancelFunc
	if firstFrame > 0 {
		frameCtx, cancel = context.WithTimeout(ctx, firstFrame)
		defer cancel()
	}
	lines := make(chan string)
	go func() {
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-frameCtx.Done():
				return
			}
		}
		close(lines)
	}()
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				return fmt.Errorf("SSE stream ended before first frame")
			}
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "data:") {
				return nil
			}
			if strings.HasPrefix(line, "event:") {
				return nil
			}
		case <-frameCtx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			requestCancel()
			return fmt.Errorf("streaming timeout waiting for first SSE frame")
		}
	}
}
