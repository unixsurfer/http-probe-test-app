package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func resetGlobalState() {
	readyMu.Lock()
	readyToggle = true
	readyMu.Unlock()
	atomic.StoreUint64(&reqCount, 0)
	startTime = time.Now()
}

func TestHealthzHandler(t *testing.T) {
	resetGlobalState()
	cfg := Config{}
	handler := healthzHandler(cfg)

	t.Run("default success", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/healthz", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if status := rr.Code; status != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
		}
		if body := rr.Body.String(); body != "OK" {
			t.Errorf("handler returned unexpected body: got %v want %v", body, "OK")
		}
	})

	t.Run("fail override", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/healthz?fail=1", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if status := rr.Code; status != http.StatusInternalServerError {
			t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusInternalServerError)
		}
	})

	t.Run("fail after N requests", func(t *testing.T) {
		resetGlobalState()
		cfgN := Config{FailLivenessAfterN: 2}
		h := healthzHandler(cfgN)

		// First request - count 0
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("GET", "/healthz", nil))
		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}

		// Simulate 2 requests to root
		atomic.StoreUint64(&reqCount, 2)

		rr = httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("GET", "/healthz", nil))
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 after 2 requests, got %d", rr.Code)
		}
	})
}

func TestReadyzHandler(t *testing.T) {
	resetGlobalState()
	cfg := Config{}
	handler := readyzHandler(cfg)

	t.Run("default ready", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/readyz", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if status := rr.Code; status != http.StatusOK {
			t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
		}
	})

	t.Run("toggled off", func(t *testing.T) {
		readyMu.Lock()
		readyToggle = false
		readyMu.Unlock()

		req := httptest.NewRequest("GET", "/readyz", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if status := rr.Code; status != http.StatusServiceUnavailable {
			t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusServiceUnavailable)
		}
	})

	t.Run("startup delay", func(t *testing.T) {
		resetGlobalState()
		cfgDelay := Config{ReadyDelaySeconds: 10}
		h := readyzHandler(cfgDelay)

		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("GET", "/readyz", nil))
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("expected 503 during delay, got %d", rr.Code)
		}
	})
}

func TestToggleReadyHandler(t *testing.T) {
	resetGlobalState()
	handler := toggleReadyHandler()

	t.Run("only POST allowed", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/toggle-ready", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", rr.Code)
		}
	})

	t.Run("toggle works", func(t *testing.T) {
		readyMu.Lock()
		readyToggle = true
		readyMu.Unlock()

		req := httptest.NewRequest("POST", "/toggle-ready", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		readyMu.RLock()
		isReady := readyToggle
		readyMu.RUnlock()
		if isReady != false {
			t.Error("readyToggle should be false after toggle")
		}
	})
}

func TestInfoHandler(t *testing.T) {
	resetGlobalState()
	Version = "1.2.3"
	GitCommit = "abc1234"
	cfg := Config{PodName: "test-pod", ClusterLabel: "test-cluster", RolloutLabel: "v1"}
	handler := infoHandler(cfg)

	req := httptest.NewRequest("GET", "/info", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp InfoResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal info response: %v", err)
	}

	if resp.Version != "1.2.3" || resp.GitCommit != "abc1234" || resp.PodName != "test-pod" {
		t.Errorf("unexpected info response content: %+v", resp)
	}
	if resp.RolloutLabel != "v1" {
		t.Errorf("unexpected rollout_label: got %q want %q", resp.RolloutLabel, "v1")
	}
}

func TestRootHandler(t *testing.T) {
	resetGlobalState()
	registerMetrics("test")
	cfg := Config{PodName: "test-pod"}
	handler := rootHandler(cfg)

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	if count := atomic.LoadUint64(&reqCount); count != 1 {
		t.Errorf("expected reqCount 1, got %d", count)
	}

	if !strings.Contains(rr.Body.String(), "Hello from test-pod") {
		t.Errorf("unexpected body: %s", rr.Body.String())
	}
}

func TestLoadConfig(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("PREFIX", "custom")

	cfg := loadConfig()
	if cfg.Port != 9090 || cfg.Prefix != "custom" {
		t.Errorf("loadConfig failed: %+v", cfg)
	}
}

func TestGracefulShutdown(t *testing.T) {
	resetGlobalState()
	registerMetrics("shutdown_test")
	cfg := Config{Port: 0, ShutdownTimeoutSec: 2}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthzHandler(cfg))

	srv := &http.Server{
		Handler: mux,
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			t.Errorf("unexpected serve error: %v", err)
		}
	}()

	// Verify server is running
	resp, err := http.Get("http://" + ln.Addr().String() + "/healthz")
	if err != nil {
		t.Fatalf("server not responding: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Trigger graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ShutdownTimeoutSec)*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}

	// Verify server is no longer accepting connections
	_, err = http.Get("http://" + ln.Addr().String() + "/healthz")
	if err == nil {
		t.Error("expected connection error after shutdown")
	}
}


