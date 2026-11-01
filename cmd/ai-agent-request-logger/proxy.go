package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultBurstThreshold = 20
	defaultBurstWindow    = 2 * time.Second
)

// proxyHandler forwards one harness's requests to its configured model provider and records model turns.
type proxyHandler struct {
	// target is the single upstream route selected at startup.
	target Target
	// client forwards requests without following redirects or rewriting auth headers.
	client *http.Client
	// logDir stores private request and response captures.
	logDir string
	// logger reports routing, capture, and retry-loop events.
	logger *slog.Logger
	// mu protects the retry burst state from concurrent requests.
	mu sync.Mutex
	// burst tracks one rapidly repeating request signature.
	burst burstState
}

// burstState records repeated failures so a tight retry loop cannot fill the disk.
type burstState struct {
	// key identifies the method, path, and upstream status being repeated.
	key string
	// start is the beginning of the current time window.
	start time.Time
	// count is the number of matching responses in the window.
	count int
	// warned records whether the threshold warning has already been printed.
	warned bool
}

// newProxyHandler creates a local reverse proxy for one resolved target.
func newProxyHandler(target Target, logDir string, logger *slog.Logger) *proxyHandler {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &proxyHandler{
		target: target,
		client: &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }},
		logDir: logDir,
		logger: logger,
	}
}

// ServeHTTP forwards the request and captures completed model responses.
func (p *proxyHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if isWebSocketUpgrade(req) {
		w.Header().Set("Connection", "close")
		w.WriteHeader(http.StatusUpgradeRequired)
		return
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		p.writeError(w, http.StatusBadRequest, "failed to read request body")
		p.logger.ErrorContext(req.Context(), "read_request_body", "err", err)
		return
	}
	upstreamURL, err := p.upstreamURL(req.URL)
	if err != nil {
		p.writeError(w, http.StatusBadGateway, "invalid upstream URL")
		p.logger.ErrorContext(req.Context(), "build_upstream_url", "err", err)
		return
	}
	upstreamReq, err := http.NewRequestWithContext(req.Context(), req.Method, upstreamURL.String(), bytes.NewReader(body))
	if err != nil {
		p.writeError(w, http.StatusBadGateway, "failed to build upstream request")
		p.logger.ErrorContext(req.Context(), "build_upstream_request", "err", err)
		return
	}
	copyRequestHeaders(upstreamReq, req, len(body))
	upstreamResp, err := p.client.Do(upstreamReq)
	if err != nil {
		p.writeError(w, http.StatusBadGateway, "upstream request failed")
		p.logger.ErrorContext(req.Context(), "upstream_request", "upstream", p.target.UpstreamBaseURL, "err", err)
		return
	}
	defer upstreamResp.Body.Close()
	copyResponseHeaders(w.Header(), upstreamResp.Header)
	w.WriteHeader(upstreamResp.StatusCode)
	var responseBody bytes.Buffer
	if _, err := io.Copy(io.MultiWriter(w, &responseBody), upstreamResp.Body); err != nil {
		p.logger.ErrorContext(req.Context(), "stream_response", "err", err)
		return
	}
	capture := Capture{
		AgentLabel: p.target.AgentLabel, Renderer: p.target.Renderer,
		Timestamp: time.Now().UTC(), Method: req.Method, Path: requestPath(req.URL),
		StatusCode: upstreamResp.StatusCode, RequestHeaders: cloneHeaderMap(req.Header),
		RequestBody: body, RequestEncoding: req.Header.Get("Content-Encoding"),
		ResponseBody: responseBody.Bytes(),
	}
	if !ShouldLogRequest(capture.Method, capture.Path, capture.Renderer) {
		p.logger.InfoContext(req.Context(), "housekeeping_request", "method", capture.Method, "path", capture.Path, "status", capture.StatusCode)
		return
	}
	if !p.shouldCapture(capture) {
		return
	}
	if err := p.writeCapture(capture); err != nil {
		p.logger.ErrorContext(req.Context(), "write_capture", "err", err)
		return
	}
	p.logger.InfoContext(req.Context(), "capture_written", "path", p.captureBase(capture)+".md", "status", capture.StatusCode)
}

// upstreamURL joins the provider base path with the harness request path and query.
func (p *proxyHandler) upstreamURL(requestURL *url.URL) (*url.URL, error) {
	upstream, err := url.Parse(p.target.UpstreamBaseURL)
	if err != nil {
		return nil, fmt.Errorf("proxy: parse upstream URL: %w", err)
	}
	upstream.Path = strings.TrimRight(upstream.Path, "/") + "/" + strings.TrimLeft(requestURL.Path, "/")
	upstream.RawPath = ""
	upstream.RawQuery = requestURL.RawQuery
	return upstream, nil
}

// shouldCapture suppresses duplicate captures during a tight retry loop but still forwards every request.
func (p *proxyHandler) shouldCapture(capture Capture) bool {
	now := time.Now()
	key := fmt.Sprintf("%s %s %d", capture.Method, capture.Path, capture.StatusCode)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.burst.key != key || now.Sub(p.burst.start) > defaultBurstWindow {
		p.burst = burstState{key: key, start: now, count: 1}
		return true
	}
	p.burst.count++
	if p.burst.count <= defaultBurstThreshold {
		return true
	}
	if !p.burst.warned {
		p.logger.Warn("repeated_upstream_response", "method", capture.Method, "path", capture.Path, "status", capture.StatusCode, "threshold", defaultBurstThreshold, "window", defaultBurstWindow, "message", "forwarding continues, but duplicate captures are suppressed; check the base URL, model ID, and credentials")
		p.burst.warned = true
	}
	if p.burst.count%500 == 0 {
		p.logger.Info("retry_captures_suppressed", "count", p.burst.count, "path", capture.Path)
	}
	return false
}

// writeCapture saves the raw exchange and a readable Markdown rendering with private file permissions.
func (p *proxyHandler) writeCapture(capture Capture) error {
	if err := os.MkdirAll(p.logDir, 0o700); err != nil {
		return fmt.Errorf("proxy: create log directory: %w", err)
	}
	if err := os.Chmod(p.logDir, 0o700); err != nil {
		return fmt.Errorf("proxy: restrict log directory: %w", err)
	}
	base := p.captureBase(capture)
	files := []struct {
		name string
		body []byte
	}{
		{name: base + ".request.txt", body: capture.RequestBody},
		{name: base + ".response.txt", body: capture.ResponseBody},
		{name: base + ".md", body: []byte(RenderMarkdown(capture))},
	}
	for _, file := range files {
		if err := writePrivateFile(filepath.Join(p.logDir, file.name), file.body); err != nil {
			return fmt.Errorf("proxy: write capture %s: %w", file.name, err)
		}
	}
	return nil
}

// writePrivateFile writes sensitive capture bytes after forcing private file permissions.
func writePrivateFile(path string, body []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("restrict file permissions: %w", err)
	}
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		return fmt.Errorf("write file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close file: %w", err)
	}
	return nil
}

// captureBase creates a filesystem-safe name that sorts chronologically.
func (p *proxyHandler) captureBase(capture Capture) string {
	stamp := capture.Timestamp.UTC().Format("2006-01-02T15-04-05.000000000")
	return stamp + "_" + safeName(p.target.AgentID)
}

// safeName replaces filename separators and punctuation in an identifier.
func safeName(value string) string {
	var out strings.Builder
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' {
			out.WriteRune(char)
		} else {
			out.WriteByte('-')
		}
	}
	return strings.Trim(out.String(), "-")
}

// writeError returns a small JSON error when the request cannot be forwarded.
func (p *proxyHandler) writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "ai-agent-request-logger: " + message})
}

// copyRequestHeaders preserves authentication and protocol headers while removing hop-by-hop metadata.
func copyRequestHeaders(dst *http.Request, src *http.Request, bodyLength int) {
	for key, values := range src.Header {
		if isHopHeader(key) {
			continue
		}
		dst.Header[key] = append([]string(nil), values...)
	}
	removeConnectionHeaders(dst.Header, src.Header)
	dst.Header.Set("Accept-Encoding", "identity")
	dst.ContentLength = int64(bodyLength)
	dst.Host = dst.URL.Host
}

// copyResponseHeaders forwards end-to-end response headers and drops connection-specific ones.
func copyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		if isHopHeader(key) {
			continue
		}
		dst[key] = append([]string(nil), values...)
	}
	removeConnectionHeaders(dst, src)
}

// isWebSocketUpgrade detects upgrade requests that this HTTP-only proxy cannot tunnel.
func isWebSocketUpgrade(req *http.Request) bool {
	return strings.EqualFold(req.Header.Get("Upgrade"), "websocket") && strings.Contains(strings.ToLower(req.Header.Get("Connection")), "upgrade")
}

// isHopHeader identifies headers that apply only to one transport connection.
func isHopHeader(key string) bool {
	switch strings.ToLower(key) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

// removeConnectionHeaders also strips headers named by the Connection header.
func removeConnectionHeaders(dst, source http.Header) {
	for _, value := range source.Values("Connection") {
		for name := range strings.SplitSeq(value, ",") {
			dst.Del(strings.TrimSpace(name))
		}
	}
}

// cloneHeaderMap makes a detached capture copy of the incoming header values.
func cloneHeaderMap(source http.Header) map[string][]string {
	clone := make(map[string][]string, len(source))
	for key, values := range source {
		clone[key] = append([]string(nil), values...)
	}
	return clone
}

// requestPath retains the query string in the endpoint recorded in the capture.
func requestPath(requestURL *url.URL) string {
	if requestURL.RawQuery == "" {
		return requestURL.Path
	}
	return requestURL.Path + "?" + requestURL.RawQuery
}

// serveUntilCancelled runs the local listener until it is stopped or fails.
func serveUntilCancelled(ctx context.Context, address string, handler http.Handler) error {
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("proxy: shut down listener: %w", err)
		}
		return nil
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("proxy: listen: %w", err)
		}
		return nil
	}
}
