package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func fixture(t *testing.T, upstream http.HandlerFunc) (*Gateway, string, string) {
	t.Helper()
	up := httptest.NewTLSServer(upstream)
	t.Cleanup(up.Close)
	return fixtureWithServer(t, up)
}

func fixtureInMemory(t *testing.T, upstream http.HandlerFunc) (*Gateway, string, string) {
	t.Helper()
	up := httptest.NewTestServer(t, upstream)
	// Config validation requires HTTPS; the in-memory client supports TLS.
	up.URL = "https://example.com"
	return fixtureWithServer(t, up)
}

func fixtureWithServer(t *testing.T, up *httptest.Server) (*Gateway, string, string) {
	t.Helper()
	admin := strings.Repeat("a", 40)
	t.Setenv("TEST_ADMIN_TOKEN", admin)
	t.Setenv("TEST_PROVIDER_KEY", "provider-secret")
	c := Config{ModelAccessGroups: testAccessConfig().ModelAccessGroups, DefaultModelAccessGroup: "free", Listen: ":42080", AdminListen: "127.0.0.1:42081", Database: filepath.Join(t.TempDir(), "gateway.db"), BackupDirectory: t.TempDir(), BackupIntervalSeconds: 86400, AdminTokenEnv: "TEST_ADMIN_TOKEN", Providers: map[string]Provider{"upstream": {BaseURL: up.URL, APIKeyEnv: "TEST_PROVIDER_KEY", Accounting: "openrouter"}}, Models: map[string]Route{"alias": testRoute()}, MaxBodyBytes: 1 << 20, MaxResponseBytes: 1 << 20, MaxOutputTokens: 100, GlobalConcurrency: 100, UserConcurrency: 4, UserRequestsPerMinute: 60, RequestTimeoutSeconds: 5, HeaderTimeoutSeconds: 2, StreamIdleSeconds: 1, ShutdownSeconds: 2, ReconcileMaxAttempts: 2}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(c.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	_, key := provision(t, s, "1")
	g := New(c, s, slog.New(slog.NewTextHandler(io.Discard, nil)))
	g.Client.Transport = up.Client().Transport
	return g, key, admin
}
func call(h http.Handler, method, path, key, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func onlyRequest(t *testing.T, g *Gateway) Request {
	t.Helper()
	rows, err := g.Store.History(context.Background(), "", "", "", 100)
	if err != nil || len(rows) != 1 {
		t.Fatalf("requests: %d %v", len(rows), err)
	}
	return rows[0]
}

func TestChatForwardingAndSettlement(t *testing.T) {
	g, key, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer provider-secret" {
			t.Error("wrong upstream credential")
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if string(body["model"]) != `"model"` || string(body["max_tokens"]) != "100" || string(body["response_format"]) != `{"type":"json_object"}` {
			t.Errorf("forwarded body: %s", body)
		}
		if bytes.Contains(body["user"], []byte("spoof")) {
			t.Error("trusted client user field")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"gen-one","model":"model","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":2,"cost":0.025}}`)
	})
	w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, `{"model":"alias","messages":[{"role":"user","content":"hello"}],"user":"spoof","response_format":{"type":"json_object"}}`)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w.Header().Get("X-Request-ID") == "" {
		t.Fatal("no request ID")
	}
	r := onlyRequest(t, g)
	if r.Accounting != "settled" || r.CostUSD == nil || *r.CostUSD != "0.025000000" || r.GenerationID != "gen-one" {
		t.Fatalf("accounting: %+v", r)
	}
	if w := call(g.InferenceHandler(), "GET", "/v1/models", key, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "alias") {
		t.Fatalf("model list: %d %s", w.Code, w.Body)
	}
}

func TestStreamingToolCallsAndFinalUsage(t *testing.T) {
	g, key, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Generation-Id", "gen-stream")
		for _, part := range []string{": heartbeat\r\n\r\n", "data: {\"id\":\"gen-stream\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"lookup\",\"arguments\":\"{}\"}}]}}]}\r\n\r\n", "data: {\"id\":\"gen-stream\",\"choices\":[],\"usage\":{\"cost\":0.1,\"prompt_tokens\":5,\"completion_tokens\":2}}\n\n", "data: [DONE]\n\n"} {
			fmt.Fprint(w, part)
			w.(http.Flusher).Flush()
		}
	})
	w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, `{"model":"alias","messages":[{"role":"user","content":"hello"}],"stream":true,"tools":[{"type":"function","function":{"name":"lookup"}}]}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "tool_calls") || !strings.Contains(w.Body.String(), "[DONE]") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	r := onlyRequest(t, g)
	if r.Accounting != "settled" || r.Lifecycle != "completed" || *r.CostUSD != "0.100000000" {
		t.Fatalf("%+v", r)
	}
	if g.Metrics.FirstTokenCount.Load() != 1 {
		t.Fatal("first token not measured")
	}
}

func TestStreamingResponseSizeLimit(t *testing.T) {
	stream := "data: {\"id\":\"gen-limit\",\"choices\":[]}\n\ndata: [DONE]\n\n"
	for _, tc := range []struct {
		name        string
		limit       int64
		wantFailure bool
	}{{"exact limit", int64(len(stream)), false}, {"over limit", int64(len(stream) - 1), true}} {
		t.Run(tc.name, func(t *testing.T) {
			g, key, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("X-Generation-Id", "gen-limit")
				fmt.Fprint(w, stream)
			})
			g.Config.MaxResponseBytes = tc.limit
			w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, `{"model":"alias","messages":[{}],"stream":true}`)
			req := onlyRequest(t, g)
			if tc.wantFailure {
				if !strings.Contains(w.Body.String(), "upstream_response_too_large") || strings.Contains(w.Body.String(), stream) || req.Lifecycle != "failed" || req.Accounting != "pending" || req.GenerationID != "gen-limit" {
					t.Fatalf("body=%q request=%+v", w.Body.String(), req)
				}
			} else if !strings.HasSuffix(w.Body.String(), "data: [DONE]\n\n") || strings.Contains(w.Body.String(), "gen-limit") || req.Lifecycle != "completed" {
				t.Fatalf("body=%q request=%+v", w.Body.String(), req)
			}
		})
	}
}

func TestInterruptedStreamReconciliation(t *testing.T) {
	g, key, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/generation" {
			if r.URL.Query().Get("id") != "gen-interrupted" {
				t.Error("wrong generation lookup")
			}
			fmt.Fprint(w, `{"data":{"id":"gen-interrupted","total_cost":0.2,"native_tokens_prompt":3,"native_tokens_completion":4,"generation_time":100,"finish_reason":"stop"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"gen-interrupted\",\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
	})
	w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, `{"model":"alias","messages":[{}],"stream":true}`)
	if !strings.Contains(w.Body.String(), "upstream_stream_incomplete") {
		t.Fatalf("missing terminal error: %s", w.Body)
	}
	req := onlyRequest(t, g)
	if req.Accounting != "pending" || req.Lifecycle != "failed" {
		t.Fatalf("%+v", req)
	}
	if err := g.reconcileOne(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if err := g.reconcileOne(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	settled, err := g.Store.Request(context.Background(), req.ID)
	if err != nil || settled.Accounting != "settled" || *settled.CostUSD != "0.200000000" {
		t.Fatalf("%+v %v", settled, err)
	}
}

func TestUnknownCostContinueAndFlag(t *testing.T) {
	g, key, admin := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"gen-missing","choices":[],"usage":{"prompt_tokens":1}}`)
	})
	for range 2 {
		w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, `{"model":"alias","messages":[{}]}`)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	rows, err := g.Store.History(context.Background(), "", "pending", "", 10)
	if err != nil || len(rows) != 2 {
		t.Fatal(err)
	}
	for _, req := range rows {
		req.Attempts = 1
		if err := g.Store.Retry(context.Background(), req, 2); err != nil {
			t.Fatal(err)
		}
	}
	w := call(g.AdminHandler(), "GET", "/admin/v1/requests?accounting=unresolved", admin, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "unresolved") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	req := rows[0]
	w = call(g.AdminHandler(), "POST", "/admin/v1/requests/"+req.ID+"/resolve", admin, `{"total_usd":"0.3","reason":"provider export reconciliation"}`)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w = call(g.AdminHandler(), "GET", "/admin/v1/audit", admin, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "request.resolve") {
		t.Fatal(w.Body)
	}
}

func TestUpstreamRejectionAndMalformedResponse(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		accounting string
	}{{"rejected", 429, `{"error":"busy"}`, "settled"}, {"ambiguous", 503, `{"error":"down"}`, "pending"}, {"malformed", 200, `not json`, "pending"}} {
		t.Run(tc.name, func(t *testing.T) {
			g, key, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) })
			w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, `{"model":"alias","messages":[{}]}`)
			if w.Code < 400 {
				t.Fatal(w.Code)
			}
			req := onlyRequest(t, g)
			if req.Accounting != tc.accounting {
				t.Fatalf("%+v", req)
			}
		})
	}
}

func TestAuthenticationAndValidation(t *testing.T) {
	var calls atomic.Int64
	g, key, admin := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"id":"gen","usage":{"cost":0},"choices":[]}`)
	})
	for _, tc := range []struct {
		key, body string
		status    int
	}{
		{"", `{}`, 401}, {admin, `{}`, 401}, {key, `{"model":"unknown","messages":[{}]}`, 403}, {key, `{"model":"alias","messages":[{}],"provider":{}}`, 400}, {key, `{"model":"alias","messages":[{}],"max_tokens":101}`, 400}, {key, `{"model":"alias","messages":[{}],"stream":null}`, 400}, {key, `{"model":"alias","messages":[]}`, 400}, {key, `null`, 400}, {key, `{"model":"alias","messages":[{}]} {}`, 400},
	} {
		if w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", tc.key, tc.body); w.Code != tc.status {
			t.Errorf("%s: %d %s", tc.body, w.Code, w.Body)
		}
	}
	if w := call(g.AdminHandler(), "GET", "/admin/v1/users", key, ""); w.Code != 401 {
		t.Fatal("virtual key accessed admin")
	}
	if calls.Load() != 0 {
		t.Fatal("invalid requests reached upstream")
	}
	if w := call(g.AdminHandler(), "POST", "/admin/v1/users", admin, `{"id":"test","budget_usd":"1","cadence":"weekly","anchor":"2025-01-01T00:00:00Z"}`); w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	g.StopAdmission()
	if w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, `{}`); w.Code != 503 {
		t.Fatal(w.Code)
	}
}

func TestStreamIdleCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cancelled := make(chan struct{})
		g, key, _ := fixtureInMemory(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, ": waiting\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			close(cancelled)
		})
		w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, `{"model":"alias","messages":[{}],"stream":true}`)
		select {
		case <-cancelled:
		case <-time.After(2 * time.Second):
			t.Fatal("upstream was not cancelled")
		}
		if !strings.Contains(w.Body.String(), "upstream_stream_incomplete") {
			t.Fatal(w.Body)
		}
	})
}

func TestLimiter(t *testing.T) {
	c := Config{GlobalConcurrency: 2, UserConcurrency: 1, UserRequestsPerMinute: 2}
	l := limiter{users: map[string]*bucket{}}
	now := time.Now()
	release, code := l.acquire("a", c, now)
	if release == nil {
		t.Fatal(code)
	}
	if f, code := l.acquire("a", c, now); f != nil || code != "concurrency_limited" {
		t.Fatal(code)
	}
	release()
	release, code = l.acquire("a", c, now)
	if release == nil {
		t.Fatal(code)
	}
	release()
	if f, code := l.acquire("a", c, now); f != nil || code != "rate_limited" {
		t.Fatal(code)
	}
	release, code = l.acquire("a", c, now.Add(time.Minute))
	if release == nil {
		t.Fatal(code)
	}
	release()
}

func TestSSEFramingAndBound(t *testing.T) {
	var events []string
	p := SSEParser{MaxEventBytes: 100, OnEvent: func(b []byte) error { events = append(events, string(b)); return nil }}
	for _, b := range []byte(": heartbeat\r\n\r\ndata: one\rdata: two\r\n\r\ndata: [DONE]\n\n") {
		if err := p.Feed([]byte{b}); err != nil {
			t.Fatal(err)
		}
	}
	if len(events) != 2 || events[0] != "one\ntwo" || events[1] != "[DONE]" {
		t.Fatal(events)
	}
	if err := p.Feed([]byte(strings.Repeat("x", 101))); err == nil {
		t.Fatal("unbounded event accepted")
	}
}
func FuzzSSE(f *testing.F) {
	f.Add([]byte("data: {\"usage\":{\"cost\":0.1}}\n\ndata: [DONE]\n\n"), uint8(7))
	f.Add([]byte(":heartbeat\r\n\r\n"), uint8(1))
	f.Fuzz(func(t *testing.T, data []byte, width uint8) {
		p := SSEParser{MaxEventBytes: 4096, OnEvent: func(b []byte) error { return nil }}
		n := int(width) + 1
		for len(data) > 0 {
			end := min(n, len(data))
			if err := p.Feed(data[:end]); err != nil {
				return
			}
			data = data[end:]
		}
		if len(p.line)+len(p.data) > 8192 {
			t.Fatal("parser exceeded bound")
		}
	})
}
func FuzzUsage(f *testing.F) {
	f.Add([]byte(`{"cost":0.1}`))
	f.Add([]byte(`{"cost":1e99999999}`))
	f.Fuzz(func(t *testing.T, data []byte) { _, _, _ = costFromUsage(data, "openrouter", Route{}) })
}

func TestChatOutputTokenLimits(t *testing.T) {
	for _, cap := range []int{0, 100} {
		t.Run(fmt.Sprintf("cap_%d", cap), func(t *testing.T) {
			cases := []struct {
				name, fields string
				status       int
			}{{"omitted", "", http.StatusOK}}
			for _, field := range []string{"max_tokens", "max_completion_tokens"} {
				for _, value := range []string{"1", "100", "32768", "0", "-1", "null", "1.5", `"100"`} {
					status := http.StatusBadRequest
					if value == "1" || value == "100" || (cap == 0 && value == "32768") {
						status = http.StatusOK
					}
					cases = append(cases, struct {
						name, fields string
						status       int
					}{
						field + "_" + value, fmt.Sprintf(`,"%s":%s`, field, value), status,
					})
				}
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					var forwarded atomic.Bool
					g, key, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
						forwarded.Store(true)
						var body map[string]json.RawMessage
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						var want map[string]json.RawMessage
						if err := json.Unmarshal([]byte(`{`+strings.TrimPrefix(tc.fields, ",")+`}`), &want); err != nil {
							t.Fatal(err)
						}
						if tc.fields == "" && cap > 0 {
							want["max_tokens"] = json.RawMessage(fmt.Sprint(cap))
						}
						for _, field := range []string{"max_tokens", "max_completion_tokens"} {
							if string(body[field]) != string(want[field]) {
								t.Errorf("%s: got %s, want %s", field, body[field], want[field])
							}
						}
						fmt.Fprint(w, `{"id":"gen-limit","choices":[],"usage":{"cost":0}}`)
					})
					g.Config.MaxOutputTokens = cap
					w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, `{"model":"alias","messages":[{}]`+tc.fields+`}`)
					if w.Code != tc.status {
						t.Fatalf("got %d, want %d: %s", w.Code, tc.status, w.Body)
					}
					if forwarded.Load() != (tc.status == http.StatusOK) {
						t.Fatalf("unexpected forwarding: %v", forwarded.Load())
					}
				})
			}
		})
	}
}
