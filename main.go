// Package main implements a test HTTP service for Kubernetes probes.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Global state
var (
	Version     = "dev"
	GitCommit   = "unknown"
	reqCount    uint64
	readyToggle = true
	readyMu     sync.RWMutex
	startTime   time.Time
	instanceID  string
)

// Config from env
type Config struct {
	Port                int
	Prefix              string
	ClusterLabel        string
	RolloutLabel        string
	PodName             string
	Namespace           string
	NodeName            string
	ExtraLatencyMs      int
	LatencyJitterMs     int
	FailLivenessAfterN  int
	FailReadinessAfterN int
	ReadyDelaySeconds   int
	ShutdownTimeoutSec  int
}

func loadConfig() Config {
	return Config{
		Port:                getEnvInt("PORT", 8080),
		Prefix:              getEnv("PREFIX", "dummy"),
		ClusterLabel:        getEnv("CLUSTER_LABEL", "unknown"),
		RolloutLabel:        getEnv("ROLLOUT_LABEL", "v0"),
		PodName:             getEnv("POD_NAME", "unknown"),
		Namespace:           getEnv("NAMESPACE", "unknown"),
		NodeName:            getEnv("NODE_NAME", "unknown"),
		ExtraLatencyMs:      getEnvInt("EXTRA_LATENCY_MS", 0),
		LatencyJitterMs:     getEnvInt("LATENCY_JITTER_MS", 0),
		FailLivenessAfterN:  getEnvInt("FAIL_LIVENESS_AFTER_N_REQUESTS", 0),
		FailReadinessAfterN: getEnvInt("FAIL_READINESS_AFTER_N_REQUESTS", 0),
		ReadyDelaySeconds:   getEnvInt("READY_DELAY_SECONDS", 0),
		ShutdownTimeoutSec:  getEnvInt("SHUTDOWN_TIMEOUT_SECONDS", 5),
	}
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if value, ok := os.LookupEnv(key); ok {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}
	return fallback
}

var (
	requestsTotal *prometheus.CounterVec
	latencyHist   *prometheus.HistogramVec
)

func registerMetrics(prefix string) {
	requestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: prefix + "_test_requests_total",
			Help: "Total HTTP requests handled",
		},
		[]string{"cluster", "pod", "node"},
	)

	latencyHist = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    prefix + "_test_response_latency_seconds",
			Help:    "Response latency distribution",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"cluster"},
	)

	prometheus.MustRegister(requestsTotal, latencyHist)
}

func healthzHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Check query param override
		if r.URL.Query().Get("fail") == "1" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("FAIL (query override)"))
			return
		}

		// Check request count threshold
		if cfg.FailLivenessAfterN > 0 && atomic.LoadUint64(&reqCount) >= uint64(cfg.FailLivenessAfterN) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("FAIL (request threshold)"))
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}
}

func readyzHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		// Check startup delay
		if cfg.ReadyDelaySeconds > 0 {
			elapsed := time.Since(startTime)
			if elapsed < time.Duration(cfg.ReadyDelaySeconds)*time.Second {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = fmt.Fprintf(w, "NOT READY (startup delay: %v remaining)",
					time.Duration(cfg.ReadyDelaySeconds)*time.Second-elapsed)
				return
			}
		}

		// Check toggle state
		readyMu.RLock()
		isReady := readyToggle
		readyMu.RUnlock()

		if !isReady {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("NOT READY (toggled off)"))
			return
		}

		// Check request count threshold
		if cfg.FailReadinessAfterN > 0 && atomic.LoadUint64(&reqCount) >= uint64(cfg.FailReadinessAfterN) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("NOT READY (request threshold)"))
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("READY"))
	}
}

func toggleReadyHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = w.Write([]byte("Only POST allowed"))
			return
		}
		readyMu.Lock()
		readyToggle = !readyToggle
		state := readyToggle
		readyMu.Unlock()

		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "Ready toggled to: %v", state)
	}
}

type InfoResponse struct {
	Hostname    string `json:"hostname"`
	PodName     string `json:"pod_name"`
	Namespace   string `json:"namespace"`
	NodeName    string `json:"node_name"`
	ClusterName  string `json:"cluster_name"`
	RolloutLabel string `json:"rollout_label"`
	Version      string `json:"version"`
	GitCommit   string `json:"git_commit"`
	Environment string `json:"environment"`
	InstanceID  string `json:"instance_id"`
	CurrentTime string `json:"current_time_utc"`
	Uptime      string `json:"uptime"`
	Readiness   bool   `json:"readiness"`
}

func infoHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		hostname, _ := os.Hostname()
		readyMu.RLock()
		readiness := readyToggle
		readyMu.RUnlock()
		info := InfoResponse{
			Hostname:    hostname,
			PodName:     cfg.PodName,
			Namespace:   cfg.Namespace,
			NodeName:    cfg.NodeName,
			ClusterName:  cfg.ClusterLabel,
			RolloutLabel: cfg.RolloutLabel,
			Version:      Version,
			GitCommit:   GitCommit,
			Environment: getEnv("ENVIRONMENT", "development"),
			InstanceID:  instanceID,
			CurrentTime: time.Now().UTC().Format(time.RFC3339),
			Uptime:      time.Since(startTime).Truncate(time.Millisecond).String(),
			Readiness:   readiness,
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(info)
	}
}

func rootHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		start := time.Now()

		// Increment request counter
		atomic.AddUint64(&reqCount, 1)

		// Apply extra latency
		if cfg.ExtraLatencyMs > 0 {
			delay := cfg.ExtraLatencyMs
			if cfg.LatencyJitterMs > 0 {
				delay += rand.IntN(cfg.LatencyJitterMs)
			}
			time.Sleep(time.Duration(delay) * time.Millisecond)
		}

		// Record metrics
		requestsTotal.WithLabelValues(cfg.ClusterLabel, cfg.PodName, cfg.NodeName).Inc()
		latencyHist.WithLabelValues(cfg.ClusterLabel).Observe(time.Since(start).Seconds())

		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "Hello from %s (cluster: %s, node: %s)\n",
			cfg.PodName, cfg.ClusterLabel, cfg.NodeName)
	}
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (rr *responseRecorder) WriteHeader(code int) {
	rr.status = code
	rr.ResponseWriter.WriteHeader(code)
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rr := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rr, r)
		entry, _ := json.Marshal(struct {
			Timestamp string `json:"timestamp"`
			Method    string `json:"method"`
			Endpoint  string `json:"endpoint"`
			Status    int    `json:"status"`
		}{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Method:    r.Method,
			Endpoint:  r.URL.Path,
			Status:    rr.status,
		})
		fmt.Println(string(entry))
	})
}

func main() {
	startTime = time.Now()
	instanceID = fmt.Sprintf("%d-%d", time.Now().UnixNano(), rand.Uint64())

	cfg := loadConfig()
	registerMetrics(cfg.Prefix)

	mux := http.NewServeMux()
	mux.HandleFunc("/", rootHandler(cfg))
	mux.HandleFunc("/healthz", healthzHandler(cfg))
	mux.HandleFunc("/readyz", readyzHandler(cfg))
	mux.HandleFunc("/toggle-ready", toggleReadyHandler())
	mux.HandleFunc("/info", infoHandler(cfg))
	mux.Handle("/metrics", promhttp.Handler())

	addr := fmt.Sprintf(":%d", cfg.Port)
	fmt.Printf("Starting http-probe-test-app on %s\n", addr)
	fmt.Printf("Config: prefix=%s, cluster=%s, pod=%s, node=%s\n",
		cfg.Prefix, cfg.ClusterLabel, cfg.PodName, cfg.NodeName)

	srv := &http.Server{
		Addr:    addr,
		Handler: logMiddleware(mux),
	}

	// Shutdown on SIGINT/SIGTERM
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
			os.Exit(1)
		}
	}()

	sig := <-shutdown
	fmt.Printf("Received %v, starting graceful shutdown (timeout: %ds)...\n", sig, cfg.ShutdownTimeoutSec)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ShutdownTimeoutSec)*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Graceful shutdown failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Server stopped gracefully")
}
