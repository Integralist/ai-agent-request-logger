package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	// defaultBurstThreshold caps duplicate captures without stopping request forwarding.
	defaultBurstThreshold = 20
	// defaultBurstWindow bounds the retry period used by the duplicate-capture guard.
	defaultBurstWindow = 2 * time.Second
	// defaultPrivateDirectoryMode keeps prompt and code captures private to the user.
	defaultPrivateDirectoryMode os.FileMode = 0o700
	// defaultPrivateFileMode keeps each raw and rendered capture private to the user.
	defaultPrivateFileMode os.FileMode = 0o600
	// defaultReadHeaderTimeout bounds time spent waiting for request headers.
	defaultReadHeaderTimeout = 10 * time.Second
	// defaultShutdownTimeout gives active requests a short grace period to finish.
	defaultShutdownTimeout = 5 * time.Second
)

// shutdownTimeoutCause explains why graceful shutdown stops waiting for active requests.
var errShutdownTimeout = errors.New("proxy: graceful shutdown timeout elapsed")

// errorResponse is the JSON shape returned when a request cannot be forwarded.
type errorResponse struct {
	// Error describes why the proxy could not forward the request.
	Error string `json:"error"`
}

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
		p.writeError(req.Context(), w, http.StatusBadRequest, "failed to read request body")
		p.logger.LogAttrs(req.Context(), slog.LevelError, "read_request_body", slog.Any("err", err))
		return
	}
	upstreamURL, err := p.upstreamURL(req.URL)
	if err != nil {
		p.writeError(req.Context(), w, http.StatusBadGateway, "invalid upstream URL")
		p.logger.LogAttrs(req.Context(), slog.LevelError, "build_upstream_url", slog.Any("err", err))
		return
	}
	upstreamReq, err := http.NewRequestWithContext(req.Context(), req.Method, upstreamURL.String(), bytes.NewReader(body))
	if err != nil {
		p.writeError(req.Context(), w, http.StatusBadGateway, "failed to build upstream request")
		p.logger.LogAttrs(req.Context(), slog.LevelError, "build_upstream_request", slog.Any("err", err))
		return
	}
	copyRequestHeaders(upstreamReq, req, len(body))
	upstreamResp, err := p.client.Do(upstreamReq) // #nosec G704 -- the destination is validated from the user's explicit custom URL before the local listener starts.
	if err != nil {
		p.writeError(req.Context(), w, http.StatusBadGateway, "upstream request failed")
		p.logger.LogAttrs(req.Context(), slog.LevelError, "upstream_request", slog.String("upstream", p.target.UpstreamBaseURL), slog.Any("err", err))
		return
	}
	defer upstreamResp.Body.Close()
	copyResponseHeaders(w.Header(), upstreamResp.Header)
	w.WriteHeader(upstreamResp.StatusCode)
	var responseBody bytes.Buffer
	if _, err := io.Copy(io.MultiWriter(w, &responseBody), upstreamResp.Body); err != nil {
		p.logger.LogAttrs(req.Context(), slog.LevelError, "stream_response", slog.Any("err", err))
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
		p.logger.LogAttrs(req.Context(), slog.LevelInfo, "housekeeping_request", slog.String("method", capture.Method), slog.String("path", capture.Path), slog.Int("status", capture.StatusCode))
		return
	}
	if !p.shouldCapture(req.Context(), capture) {
		return
	}
	if err := p.writeCapture(capture); err != nil {
		p.logger.LogAttrs(req.Context(), slog.LevelError, "write_capture", slog.Any("err", err))
		return
	}
	p.logger.LogAttrs(req.Context(), slog.LevelInfo, "capture_written", slog.String("path", p.captureBase(capture)+".md"), slog.Int("status", capture.StatusCode))
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
func (p *proxyHandler) shouldCapture(ctx context.Context, capture Capture) bool {
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
		p.logger.LogAttrs(ctx, slog.LevelWarn, "repeated_upstream_response", slog.String("method", capture.Method), slog.String("path", capture.Path), slog.Int("status", capture.StatusCode), slog.Int("threshold", defaultBurstThreshold), slog.Duration("window", defaultBurstWindow), slog.String("message", "forwarding continues, but duplicate captures are suppressed; check the base URL, model ID, and credentials"))
		p.burst.warned = true
	}
	if p.burst.count%500 == 0 {
		p.logger.LogAttrs(ctx, slog.LevelInfo, "retry_captures_suppressed", slog.Int("count", p.burst.count), slog.String("path", capture.Path))
	}
	return false
}

// writeCapture saves the raw exchange and a readable Markdown rendering with private file permissions.
func (p *proxyHandler) writeCapture(capture Capture) error {
	if err := os.MkdirAll(p.logDir, defaultPrivateDirectoryMode); err != nil {
		return fmt.Errorf("proxy: create log directory: %w", err)
	}
	if err := os.Chmod(p.logDir, defaultPrivateDirectoryMode); err != nil {
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
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, defaultPrivateFileMode)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	if err := file.Chmod(defaultPrivateFileMode); err != nil {
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
func (p *proxyHandler) writeError(ctx context.Context, w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(errorResponse{Error: "ai-agent-request-logger: " + message}); err != nil {
		p.logger.LogAttrs(ctx, slog.LevelError, "write_error_response", slog.Any("err", err))
	}
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
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: defaultReadHeaderTimeout}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeoutCause(context.WithoutCancel(ctx), defaultShutdownTimeout, errShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("proxy: shut down listener: %w", err)
		}
		return nil
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("proxy: listen: %w", err)
		}
		return nil
	}
}
