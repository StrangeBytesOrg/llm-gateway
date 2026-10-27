package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Metrics struct {
	Requests         atomic.Int64
	Rejected         atomic.Int64
	UpstreamErrors   atomic.Int64
	Active           atomic.Int64
	Completed        atomic.Int64
	AccountingErrors atomic.Int64
	AdmissionNS      atomic.Int64
	AdmissionCount   atomic.Int64
	FirstTokenNS     atomic.Int64
	FirstTokenCount  atomic.Int64
	DurationNS       atomic.Int64
}
type bucket struct {
	active int
	tokens float64
	last   time.Time
}
type limiter struct {
	mu        sync.Mutex
	users     map[string]*bucket
	total     int
	lastSweep time.Time
}

func (l *limiter) acquire(user string, c Config, now time.Time) (func(), string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.lastSweep) > time.Minute {
		for id, b := range l.users {
			if b.active == 0 && now.Sub(b.last) > 5*time.Minute {
				delete(l.users, id)
			}
		}
		l.lastSweep = now
	}
	b := l.users[user]
	if b == nil {
		b = &bucket{tokens: float64(c.UserRequestsPerMinute), last: now}
		l.users[user] = b
	}
	b.tokens = min(float64(c.UserRequestsPerMinute), b.tokens+now.Sub(b.last).Seconds()*float64(c.UserRequestsPerMinute)/60)
	b.last = now
	if l.total >= c.GlobalConcurrency || b.active >= c.UserConcurrency {
		return nil, "concurrency_limited"
	}
	if b.tokens < 1 {
		return nil, "rate_limited"
	}
	b.tokens--
	b.active++
	l.total++
	return func() { l.mu.Lock(); defer l.mu.Unlock(); b.active--; l.total-- }, ""
}

type Gateway struct {
	Config    Config
	Store     *Store
	Client    *http.Client
	Log       *slog.Logger
	Metrics   Metrics
	limits    limiter
	adminHash [32]byte
	closing   atomic.Bool
	handlerMu sync.Mutex
	handlers  sync.WaitGroup
}

func New(c Config, s *Store, log *slog.Logger) *Gateway {
	if log == nil {
		log = slog.Default()
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 200
	transport.MaxIdleConnsPerHost = c.GlobalConcurrency
	transport.MaxConnsPerHost = c.GlobalConcurrency
	transport.ResponseHeaderTimeout = seconds(c.HeaderTimeoutSeconds)
	transport.IdleConnTimeout = 90 * time.Second
	return &Gateway{Config: c, Store: s, Log: log, Client: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, limits: limiter{users: map[string]*bucket{}}, adminHash: sha256.Sum256([]byte(os.Getenv(c.AdminTokenEnv)))}
}

func (g *Gateway) InferenceHandler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	m.HandleFunc("POST /v1/chat/completions", g.chat)
	m.HandleFunc("GET /v1/models", g.models)
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { apiError(w, 404, "not_found") })
	return g.recover(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")
		if r.Method == http.MethodOptions && r.Header.Get("Origin") != "" && r.Header.Get("Access-Control-Request-Method") != "" {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			if headers := r.Header.Get("Access-Control-Request-Headers"); headers != "" {
				w.Header().Set("Access-Control-Allow-Headers", headers)
				w.Header().Add("Vary", "Access-Control-Request-Headers")
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		m.ServeHTTP(w, r)
	}))
}
func (g *Gateway) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if e := recover(); e != nil {
				g.Log.Error("handler panic", "method", r.Method)
				panic(http.ErrAbortHandler)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
func (g *Gateway) beginInference() bool {
	g.handlerMu.Lock()
	defer g.handlerMu.Unlock()
	if g.closing.Load() {
		return false
	}
	g.handlers.Add(1)
	return true
}
func (g *Gateway) StopAdmission() { g.handlerMu.Lock(); g.closing.Store(true); g.handlerMu.Unlock() }
func (g *Gateway) WaitInference(ctx context.Context) error {
	done := make(chan struct{})
	go func() { g.handlers.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func bearer(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if !strings.HasPrefix(v, "Bearer ") {
		return ""
	}
	v = strings.TrimPrefix(v, "Bearer ")
	if len(v) > 256 || strings.ContainsAny(v, " \t\r\n") {
		return ""
	}
	return v
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func apiError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"message": strings.ReplaceAll(code, "_", " "), "type": "gateway_error", "code": code}})
}
func (g *Gateway) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUnauthorized):
		apiError(w, 401, err.Error())
	case errors.Is(err, ErrDisabled):
		apiError(w, 403, err.Error())
	case errors.Is(err, ErrBudget), errors.Is(err, ErrNotStarted):
		apiError(w, 402, err.Error())
	case errors.Is(err, ErrNotFound):
		apiError(w, 404, err.Error())
	case errors.Is(err, ErrConflict):
		apiError(w, 409, err.Error())
	default:
		g.Log.Error("storage operation failed")
		apiError(w, 503, "storage_unavailable")
	}
}
func (g *Gateway) models(w http.ResponseWriter, r *http.Request) {
	if _, err := g.Store.Authenticate(r.Context(), bearer(r)); err != nil {
		g.fail(w, err)
		return
	}
	names := make([]string, 0, len(g.Config.Models))
	for name := range g.Config.Models {
		names = append(names, name)
	}
	sort.Strings(names)
	out := []any{}
	for _, name := range names {
		out = append(out, map[string]any{"id": name, "object": "model", "created": 0, "owned_by": "gateway"})
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": out})
}
func (g *Gateway) metrics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	pending, unresolved, err := g.Store.AccountingCounts(ctx)
	if err != nil {
		g.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	values := []struct {
		name  string
		value int64
		typ   string
	}{
		{"gateway_requests_total", g.Metrics.Requests.Load(), "counter"}, {"gateway_rejections_total", g.Metrics.Rejected.Load(), "counter"}, {"gateway_upstream_errors_total", g.Metrics.UpstreamErrors.Load(), "counter"}, {"gateway_active_requests", g.Metrics.Active.Load(), "gauge"}, {"gateway_completed_total", g.Metrics.Completed.Load(), "counter"}, {"gateway_accounting_errors_total", g.Metrics.AccountingErrors.Load(), "counter"}, {"gateway_accounting_pending", int64(pending), "gauge"}, {"gateway_accounting_unresolved", int64(unresolved), "gauge"},
	}
	for _, v := range values {
		_, _ = fmt.Fprintf(w, "# TYPE %s %s\n%s %d\n", v.name, v.typ, v.name, v.value)
	}
	for _, h := range []struct {
		name       string
		sum, count int64
	}{{"gateway_admission_seconds", g.Metrics.AdmissionNS.Load(), g.Metrics.AdmissionCount.Load()}, {"gateway_first_token_seconds", g.Metrics.FirstTokenNS.Load(), g.Metrics.FirstTokenCount.Load()}, {"gateway_request_seconds", g.Metrics.DurationNS.Load(), g.Metrics.Completed.Load()}} {
		_, _ = fmt.Fprintf(w, "# TYPE %s summary\n%s_sum %.9f\n%s_count %d\n", h.name, h.name, float64(h.sum)/1e9, h.name, h.count)
	}
	stats := g.Store.db.Stats()
	_, _ = fmt.Fprintf(w, "# TYPE gateway_db_wait_seconds_total counter\ngateway_db_wait_seconds_total %.9f\n# TYPE gateway_db_wait_total counter\ngateway_db_wait_total %d\n", stats.WaitDuration.Seconds(), stats.WaitCount)
}
