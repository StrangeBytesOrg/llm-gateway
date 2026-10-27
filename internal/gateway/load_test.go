package gateway

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var runLoad = flag.Bool("gateway-load", false, "run the opt-in gateway capacity test")
var loadDirectory = flag.String("gateway-load-dir", "", "existing parent directory for load-test SQLite files; defaults to the system temporary directory")

// Run explicitly: go test ./internal/gateway -run TestLoad -count=1 -v -timeout=3m
// GATEWAY_LOAD=1 enables 3,000 users, 20 requests/sec and 100 concurrent streams.
func TestLoad(t *testing.T) {
	if os.Getenv("GATEWAY_LOAD") != "1" && !*runLoad {
		t.Skip("set GATEWAY_LOAD=1 or -args -gateway-load to run the capacity test")
	}
	var active, peak atomic.Int64
	g, _, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": started\n\n")
		w.(http.Flusher).Flush()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
		}
		fmt.Fprintf(w, "data: {\"id\":\"%s\",\"choices\":[{\"delta\":{\"content\":\"ok\"}}],\"usage\":{\"cost\":0.001}}\n\ndata: [DONE]\n\n", ID("gen_"))
	})
	g.Config.RequestTimeoutSeconds = 15
	g.Config.StreamIdleSeconds = 10
	if *loadDirectory != "" {
		dir, err := os.MkdirTemp(*loadDirectory, "gateway-load-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		store, err := OpenStore(filepath.Join(dir, "gateway.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { store.Close() })
		if err = g.Store.Close(); err != nil {
			t.Fatal(err)
		}
		g.Store = store
	}
	keys := make([]string, 3000)
	for i := range keys {
		_, keys[i] = provision(t, g.Store, "100")
	}
	server := httptest.NewServer(g.InferenceHandler())
	defer server.Close()
	client := &http.Client{Timeout: 20 * time.Second}
	defer client.CloseIdleConnections()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	const total = 240
	durations := make([]float64, total)
	var failed atomic.Int64
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 100)
	ticker := time.NewTicker(time.Second / 20)
	defer ticker.Stop()
	started := time.Now()
	for i := range total {
		<-ticker.C
		semaphore <- struct{}{}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-semaphore }()
			req, err := http.NewRequest("POST", server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"alias","messages":[{"role":"user","content":"test"}],"stream":true}`))
			if err != nil {
				t.Error(err)
				failed.Add(1)
				return
			}
			req.Header.Set("Authorization", "Bearer "+keys[i%len(keys)])
			resp, err := client.Do(req)
			if err != nil {
				t.Error(err)
				failed.Add(1)
				return
			}
			defer resp.Body.Close()
			timing := strings.TrimPrefix(resp.Header.Get("Server-Timing"), "gateway;dur=")
			durations[i], err = strconv.ParseFloat(timing, 64)
			if err != nil {
				t.Error(err)
				failed.Add(1)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil || resp.StatusCode != 200 || !strings.Contains(string(body), "[DONE]") || strings.Contains(string(body), "gateway_error") {
				t.Errorf("request %d status=%d read=%v body=%s", i, resp.StatusCode, err, body)
				failed.Add(1)
			}
		}(i)
	}
	wg.Wait()
	runtime.ReadMemStats(&after)
	sort.Float64s(durations)
	p95 := durations[int(float64(total)*0.95)]
	var settled, ledger int
	if err := g.Store.db.QueryRowContext(context.Background(), "SELECT count(*) FROM requests WHERE accounting='settled'").Scan(&settled); err != nil {
		t.Fatal(err)
	}
	if err := g.Store.db.QueryRow("SELECT count(*) FROM ledger").Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	t.Logf("users=3000 requests=%d elapsed=%s peak_streams=%d admission_p95_ms=%.3f heap_before_mb=%.1f heap_after_mb=%.1f db_wait=%s settled=%d ledger=%d", total, time.Since(started).Round(time.Millisecond), peak.Load(), p95, float64(before.HeapAlloc)/(1<<20), float64(after.HeapAlloc)/(1<<20), g.Store.db.Stats().WaitDuration, settled, ledger)
	if failed.Load() != 0 || settled != total || ledger != total {
		t.Fatalf("failed=%d settled=%d ledger=%d", failed.Load(), settled, ledger)
	}
	if peak.Load() < 95 || peak.Load() > 100 {
		t.Errorf("unexpected peak concurrency: %d", peak.Load())
	}
	if p95 >= 50 {
		t.Errorf("admission p95 %.3f ms exceeds 50 ms target", p95)
	}
	if after.HeapAlloc > before.HeapAlloc+256<<20 {
		t.Errorf("heap grew by more than 256 MiB")
	}
}
