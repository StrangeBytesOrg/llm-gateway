package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestNonStreamingSlowClientReleasesConcurrency(t *testing.T) {
	response := `{"id":"gen-slow","choices":[{"message":{"content":"` + strings.Repeat("x", 8<<20) + `"}}],"usage":{"cost":0.1}}`
	g, key, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, response)
	})
	g.Config.MaxResponseBytes = int64(len(response))
	g.Config.RequestTimeoutSeconds = 1
	g.Config.GlobalConcurrency = 1
	g.Config.UserConcurrency = 1
	server := httptest.NewServer(g.InferenceHandler())
	defer server.Close()
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.(*net.TCPConn).SetReadBuffer(1024); err != nil {
		t.Fatal(err)
	}
	body := `{"model":"alias","messages":[{}]}`
	_, err = fmt.Fprintf(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer %s\r\nContent-Length: %d\r\n\r\n%s", key, len(body), body)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for g.Metrics.Active.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if g.Metrics.Active.Load() == 0 {
		t.Fatal("request did not become active")
	}
	for g.Metrics.Active.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if g.Metrics.Active.Load() != 0 {
		t.Fatal("stalled client retained its concurrency slot")
	}
	if r := onlyRequest(t, g); r.Lifecycle != "completed" || r.Accounting != "settled" {
		t.Fatalf("upstream response was not settled: %+v", r)
	}
	w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, body)
	if w.Code != http.StatusOK {
		t.Fatalf("next request: %d %s", w.Code, w.Body.String())
	}
}

func TestClientDisconnectCancelsUpstream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		upstreamCancelled := make(chan struct{})
		g, key, _ := fixtureInMemory(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("X-Generation-Id", "gen-cancelled")
			fmt.Fprint(w, "data: {\"id\":\"gen-cancelled\",\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			close(upstreamCancelled)
		})
		server := httptest.NewTestServer(t, g.InferenceHandler())
		client := server.Client()
		client.Timeout = 5 * time.Second
		req, err := http.NewRequest("POST", server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"alias","messages":[{}],"stream":true}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+key)
		defer client.CloseIdleConnections()
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		select {
		case <-upstreamCancelled:
		case <-time.After(2 * time.Second):
			t.Fatal("client disconnect did not cancel upstream")
		}
		g.StopAdmission()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err = g.WaitInference(ctx); err != nil {
			t.Fatal(err)
		}
		record := onlyRequest(t, g)
		if record.Lifecycle != "cancelled" || record.Accounting != "pending" || record.GenerationID != "gen-cancelled" {
			t.Fatalf("lost cancellation metadata: %+v", record)
		}
	})
}

func TestStorageFailurePreventsDispatch(t *testing.T) {
	var calls atomic.Int64
	g, key, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	if _, err := g.Store.db.Exec("PRAGMA query_only=ON"); err != nil {
		t.Fatal(err)
	}
	w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, `{"model":"alias","messages":[{}]}`)
	if w.Code != 503 || calls.Load() != 0 {
		t.Fatalf("storage failure: status=%d upstream_calls=%d", w.Code, calls.Load())
	}
}

func TestSQLiteFullRollsBackCharge(t *testing.T) {
	s := testStore(t)
	u, key := provision(t, s, "10")
	ctx := context.Background()
	r, err := s.Admit(ctx, key, "alias", testRoute(), Provider{Accounting: "openrouter"}, time.Now(), testAccessConfig())
	if err != nil {
		t.Fatal(err)
	}
	var pages int
	if err = s.db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", pages)); err != nil {
		t.Fatal(err)
	}
	usage, _ := json.Marshal(map[string]string{"padding": strings.Repeat("x", 1<<20)})
	if err = s.Settle(ctx, r.ID, USDScale, "1", usage, "provider_usage", "", false); err == nil {
		t.Fatal("expected SQLite page exhaustion")
	}
	got, err := s.Request(ctx, r.ID)
	if err != nil || got.Accounting != "pending" || got.Cost != nil {
		t.Fatalf("partial charge persisted: %+v %v", got, err)
	}
	summary, err := s.Summary(ctx, u.ID, time.Now())
	if err != nil || summary.Current.SpentUSD != "0.000000000" {
		t.Fatalf("partial budget charge: %+v %v", summary, err)
	}
	var n int
	if err = s.db.QueryRow("SELECT count(*) FROM ledger").Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial ledger entry: %d %v", n, err)
	}
}

func TestExternalWriterContention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "contended.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, key := provision(t, s, "1")
	if _, err = s.db.Exec("PRAGMA busy_timeout=25"); err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	tx, err := other.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE users SET enabled=enabled"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err = s.Admit(ctx, key, "alias", testRoute(), Provider{Accounting: "openrouter"}, time.Now(), testAccessConfig()); err == nil {
		t.Fatal("admitted while accounting writes were blocked")
	}
	var n int
	if err = s.db.QueryRow("SELECT count(*) FROM requests").Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial admission: %d %v", n, err)
	}
}

func TestCrashRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crashed.db")
	child := exec.Command(os.Args[0], "-test.run=^TestCrashHelper$")
	child.Env = append(os.Environ(), "GATEWAY_CRASH_DB="+path)
	data, err := child.Output()
	if err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err = s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := s.Request(ctx, strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if r.GenerationID != "gen-before-crash" || r.Lifecycle != "interrupted" || r.Accounting != "pending" {
		t.Fatalf("crash lost request: %+v", r)
	}
}
func TestCrashHelper(t *testing.T) {
	path := os.Getenv("GATEWAY_CRASH_DB")
	if path == "" {
		t.Skip("subprocess only")
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	_, key := provision(t, s, "1")
	r, err := s.Admit(context.Background(), key, "alias", testRoute(), Provider{Accounting: "openrouter"}, time.Now(), testAccessConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Generation(context.Background(), r.ID, "gen-before-crash"); err != nil {
		t.Fatal(err)
	}
	fmt.Println(r.ID)
	// Exit without Close, checkpoint, deferred cleanup or graceful shutdown.
	os.Exit(0)
}

func TestResponseBoundsAndHeaderTimeout(t *testing.T) {
	t.Run("bounded response", func(t *testing.T) {
		g, key, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, strings.Repeat("x", 128)) })
		g.Config.MaxResponseBytes = 64
		w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, `{"model":"alias","messages":[{}]}`)
		if w.Code != 502 || onlyRequest(t, g).Accounting != "pending" {
			t.Fatalf("response limit: %d", w.Code)
		}
	})
	t.Run("header timeout", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			g, key, _ := fixtureInMemory(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body); <-r.Context().Done() })
			tr := g.Client.Transport.(*http.Transport).Clone()
			tr.ResponseHeaderTimeout = 25 * time.Millisecond
			g.Client.Transport = tr
			t.Cleanup(tr.CloseIdleConnections)
			w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, `{"model":"alias","messages":[{}]}`)
			if w.Code != 502 || !strings.Contains(w.Body.String(), "upstream_timeout") {
				t.Fatalf("timeout: %d %s", w.Code, w.Body)
			}
		})
	})
}

func TestCompatibleProviderAccounting(t *testing.T) {
	for _, mode := range []string{"reported", "token_rates"} {
		t.Run(mode, func(t *testing.T) {
			g, key, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if !strings.Contains(string(body["stream_options"]), "include_usage") {
					t.Error("stream usage not requested")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"id\":\"gen-compatible\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"cost\":0.003}}\n\ndata: [DONE]\n\n")
			})
			p := g.Config.Providers["upstream"]
			p.Accounting = mode
			g.Config.Providers["upstream"] = p
			r := g.Config.Models["alias"]
			r.InputUSDPerMillion = "100"
			r.OutputUSDPerMillion = "400"
			r.PricingVersion = "v1"
			g.Config.Models["alias"] = r
			w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, `{"model":"alias","messages":[{}],"stream":true}`)
			req := onlyRequest(t, g)
			if w.Code != 200 || req.CostUSD == nil || *req.CostUSD != "0.003000000" {
				t.Fatalf("%d %+v", w.Code, req)
			}
		})
	}
}

func TestPricingRejectsUnsupportedDimensions(t *testing.T) {
	r := Route{InputUSDPerMillion: "1", OutputUSDPerMillion: "2"}
	if _, _, err := costFromUsage(json.RawMessage(`{"prompt_tokens":10,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":5}}`), "token_rates", r); err == nil {
		t.Fatal("silently priced cached tokens")
	}
	n, _, err := costFromUsage(json.RawMessage(`{"prompt_tokens":10,"completion_tokens":2,"completion_tokens_details":{"reasoning_tokens":1}}`), "token_rates", r)
	if err != nil || n != 14_000 {
		t.Fatalf("reasoning was double counted: %d %v", n, err)
	}
}

func TestReconcilePersistedUsageAndProviderSnapshot(t *testing.T) {
	g, key, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("persisted usage should not need an upstream lookup")
	})
	ctx := context.Background()
	provider := g.Config.Providers["upstream"]
	provider.Accounting = "reported"
	req, err := g.Store.Admit(ctx, key, "alias", testRoute(), provider, time.Now(), testAccessConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err = g.Store.ObservedUsage(ctx, req.ID, json.RawMessage(`{"cost":0.2}`)); err != nil {
		t.Fatal(err)
	}
	if err = g.Store.Finish(ctx, req.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	// A restart/configuration change must not reinterpret the admitted request's
	// accounting mode, endpoint, or rate snapshot.
	changed := provider
	changed.Accounting = "token_rates"
	g.Config.Providers["upstream"] = changed
	persisted, err := g.Store.Request(ctx, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.ProviderConfig.Accounting != "reported" {
		t.Fatal("provider configuration was not snapshotted")
	}
	if err = g.reconcileOne(ctx, persisted); err != nil {
		t.Fatal(err)
	}
	got, err := g.Store.Request(ctx, req.ID)
	if err != nil || got.CostUSD == nil || *got.CostUSD != "0.200000000" {
		t.Fatalf("stored cost recovery: %+v %v", got, err)
	}
}

func TestStreamDoesNotBufferUntilCompletion(t *testing.T) {
	finish := make(chan struct{})
	g, key, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": first frame\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-finish:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "data: {\"id\":\"gen-flushed\",\"usage\":{\"cost\":0},\"choices\":[]}\n\ndata: [DONE]\n\n")
	})
	server := httptest.NewServer(g.InferenceHandler())
	defer server.Close()
	client := &http.Client{Timeout: 2 * time.Second}
	defer client.CloseIdleConnections()
	req, _ := http.NewRequest("POST", server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"alias","messages":[{}],"stream":true}`))
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := client.Do(req)
	if err != nil {
		close(finish)
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, len(": keepalive\n\n"))
	if _, err = io.ReadFull(resp.Body, buf); err != nil {
		close(finish)
		t.Fatal(err)
	}
	if string(buf) != ": keepalive\n\n" {
		t.Errorf("unexpected keepalive: %q", buf)
	}
	close(finish)
	if _, err = io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatal(err)
	}
}
