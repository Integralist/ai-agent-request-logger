package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestProxyForwardsAuthAndCapturesModelRequestProtectsTheSSOHeaderAndCaptureContract.
func TestProxyForwardsAuthAndCapturesModelRequestProtectsTheSSOHeaderAndCaptureContract(t *testing.T) {
	const requestBody = `{"model":"claude-3","system":"follow policy","tools":[{"name":"read","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"hello"}]}`
	const responseBody = `{"type":"message","content":[{"type":"text","text":"hi"}]}`
	var receivedHost string
	var receivedPath string
	var receivedAuth string
	var receivedBeta string
	var receivedBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		receivedHost = req.Host
		receivedPath = req.URL.Path + "?" + req.URL.RawQuery
		receivedAuth = req.Header.Get("Authorization")
		receivedBeta = req.Header.Get("anthropic-beta")
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Errorf("read forwarded body: %v", err)
		}
		receivedBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, responseBody)
	}))
	defer upstream.Close()

	logDir := t.TempDir()
	target := Target{AgentID: "claude-code", AgentLabel: "Claude Code", UpstreamBaseURL: upstream.URL + "/gateway", Renderer: RendererAnthropic}
	handler := newProxyHandler(target, logDir, nil)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://logger.local/v1/messages?beta=true", strings.NewReader(requestBody))
	req.Header.Set("Authorization", "Bearer enterprise-session-token")
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("proxy status = %d, want %d; body %s", rr.Code, http.StatusOK, rr.Body.String())
	}
	wantHost := strings.TrimPrefix(upstream.URL, "http://")
	if receivedHost != wantHost {
		t.Errorf("upstream Host = %q, want %q", receivedHost, wantHost)
	}
	if receivedPath != "/gateway/v1/messages?beta=true" {
		t.Errorf("upstream path = %q, want /gateway/v1/messages?beta=true", receivedPath)
	}
	if receivedAuth != "Bearer enterprise-session-token" {
		t.Errorf("upstream Authorization = %q, want the original session credential", receivedAuth)
	}
	if receivedBeta != "oauth-2025-04-20" {
		t.Errorf("upstream anthropic-beta = %q, want the original OAuth capability", receivedBeta)
	}
	if receivedBody != requestBody {
		t.Errorf("upstream body = %q, want original body", receivedBody)
	}
	if rr.Body.String() != responseBody {
		t.Errorf("response body = %q, want %q", rr.Body.String(), responseBody)
	}

	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("read capture directory: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("capture files = %d, want 3", len(entries))
	}
	var markdown string
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(logDir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		switch filepath.Ext(entry.Name()) {
		case ".md":
			markdown = string(data)
		case ".txt":
			if strings.HasSuffix(entry.Name(), ".request.txt") && string(data) != requestBody {
				t.Errorf("raw request capture = %q, want original bytes", string(data))
			}
			if strings.HasSuffix(entry.Name(), ".response.txt") && string(data) != responseBody {
				t.Errorf("raw response capture = %q, want original bytes", string(data))
			}
		}
	}
	for _, expected := range []string{"<system-prompt>", "follow policy", "### read", "hello", "[REDACTED]"} {
		if !strings.Contains(markdown, expected) {
			t.Errorf("Markdown capture does not contain %q", expected)
		}
	}
	if strings.Contains(markdown, "enterprise-session-token") {
		t.Error("Markdown capture exposed the session credential")
	}
}

// TestProxyRejectsWebSocketUpgradeMakesTheHTTPOnlyBoundaryExplicitToClients.
func TestProxyRejectsWebSocketUpgradeMakesTheHTTPOnlyBoundaryExplicitToClients(t *testing.T) {
	upstreamCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { upstreamCalls++ }))
	defer upstream.Close()
	target := Target{AgentID: "pi", AgentLabel: "Pi", UpstreamBaseURL: upstream.URL, Renderer: RendererOpenAI}
	handler := newProxyHandler(target, t.TempDir(), nil)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://logger.local/v1/responses", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUpgradeRequired {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUpgradeRequired)
	}
	if upstreamCalls != 0 {
		t.Errorf("upstream calls = %d, want 0", upstreamCalls)
	}
}

// TestProxyReturnsBadGatewayForAnInvalidTargetURLReportsMisconfigurationClearly.
func TestProxyReturnsBadGatewayForAnInvalidTargetURLReportsMisconfigurationClearly(t *testing.T) {
	handler := newProxyHandler(Target{UpstreamBaseURL: "://invalid"}, t.TempDir(), nil)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://logger.local/v1/messages", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadGateway)
	}
	if !strings.Contains(rr.Body.String(), "invalid upstream URL") {
		t.Errorf("error response = %q, want invalid upstream detail", rr.Body.String())
	}
}

// TestProxyReturnsBadGatewayWhenProviderIsUnavailableKeepsConnectionFailuresVisible.
func TestProxyReturnsBadGatewayWhenProviderIsUnavailableKeepsConnectionFailuresVisible(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	upstreamURL := upstream.URL
	upstream.Close()
	handler := newProxyHandler(Target{UpstreamBaseURL: upstreamURL}, t.TempDir(), nil)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://logger.local/v1/messages", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadGateway)
	}
	if !strings.Contains(rr.Body.String(), "upstream request failed") {
		t.Errorf("error response = %q, want upstream failure detail", rr.Body.String())
	}
}

// TestRetryBurstGuardSuppressesOnlyRapidDuplicateCapturesStopsDiskGrowth.
func TestRetryBurstGuardSuppressesOnlyRapidDuplicateCapturesStopsDiskGrowth(t *testing.T) {
	handler := newProxyHandler(Target{}, t.TempDir(), nil)
	capture := Capture{Method: http.MethodPost, Path: "/v1/messages", StatusCode: http.StatusUnauthorized}
	for range defaultBurstThreshold {
		if !handler.shouldCapture(capture) {
			t.Fatal("burst guard suppressed a request before the threshold")
		}
	}
	if handler.shouldCapture(capture) {
		t.Fatal("burst guard captured a duplicate after the threshold")
	}
	capture.Path = "/v1/other"
	if !handler.shouldCapture(capture) {
		t.Fatal("burst guard suppressed a new request path")
	}
}

// TestWriteCaptureReportsFilesystemErrorsDoesNotSilentlyLoseTheExchange.
func TestWriteCaptureReportsFilesystemErrorsDoesNotSilentlyLoseTheExchange(t *testing.T) {
	logDir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(logDir, []byte("file"), 0o600); err != nil {
		t.Fatalf("create blocking file: %v", err)
	}
	handler := newProxyHandler(Target{AgentID: "pi"}, logDir, nil)
	if err := handler.writeCapture(Capture{Timestamp: time.Now().UTC()}); err == nil {
		t.Fatal("writeCapture() error = nil, want directory creation failure")
	}
}

// TestWriteCaptureRestrictsExistingPathsProtectsPreviouslyCreatedCaptureFiles.
func TestWriteCaptureRestrictsExistingPathsProtectsPreviouslyCreatedCaptureFiles(t *testing.T) {
	logDir := filepath.Join(t.TempDir(), "captures")
	if err := os.Mkdir(logDir, 0o755); err != nil {
		t.Fatalf("create capture directory: %v", err)
	}
	timestamp := time.Date(2026, 8, 6, 12, 0, 0, 123000000, time.UTC)
	requestPath := filepath.Join(logDir, "2026-08-06T12-00-00.123000000_pi.request.txt")
	if err := os.WriteFile(requestPath, []byte("old capture"), 0o644); err != nil {
		t.Fatalf("create existing capture: %v", err)
	}
	handler := newProxyHandler(Target{AgentID: "pi", AgentLabel: "Pi", Renderer: RendererRaw}, logDir, nil)
	if err := handler.writeCapture(Capture{Timestamp: timestamp, RequestBody: []byte("request"), ResponseBody: []byte("response")}); err != nil {
		t.Fatalf("writeCapture() error = %v", err)
	}
	directoryInfo, err := os.Stat(logDir)
	if err != nil {
		t.Fatalf("stat capture directory: %v", err)
	}
	if directoryInfo.Mode().Perm()&0o077 != 0 {
		t.Errorf("capture directory permissions = %o, want private", directoryInfo.Mode().Perm())
	}
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("read capture directory: %v", err)
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatalf("stat %s: %v", entry.Name(), err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s permissions = %o, want private", entry.Name(), info.Mode().Perm())
		}
	}
}
