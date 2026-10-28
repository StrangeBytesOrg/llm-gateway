package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestListenerHeaderLimits(t *testing.T) {
	for _, tc := range []struct {
		name   string
		server func(string, http.Handler) *http.Server
		limit  int
	}{
		{"inference", newInferenceServer, 128},
		{"admin", newAdminServer, 64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusNoContent)
			})
			s := httptest.NewTestServer(t, h)
			s.Config = tc.server("", h)
			client := s.Client()
			for _, count := range []int{tc.limit, tc.limit + 1} {
				req, err := http.NewRequest("GET", s.URL, nil)
				if err != nil {
					t.Fatal(err)
				}
				// Host is one header value. Disable the client's automatic additions.
				req.Header["User-Agent"] = nil
				req.Header["Accept-Encoding"] = []string{"identity"}
				for range count - 2 {
					req.Header.Add("X-Test", "a")
				}
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				want := http.StatusNoContent
				if count > tc.limit {
					want = http.StatusRequestHeaderFieldsTooLarge
				}
				if resp.StatusCode != want {
					t.Fatalf("%d header values: status %d, want %d", count, resp.StatusCode, want)
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("handler called %d times", calls.Load())
			}
		})
	}
}
