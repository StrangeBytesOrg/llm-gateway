package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestChatRejectsAmbiguousJSON(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"duplicate model", `{"model":"forbidden","model":"alias","messages":[{}]}`},
		{"escaped duplicate", `{"model":"alias","mo\u0064el":"alias","messages":[{}]}`},
		{"nested duplicate", `{"model":"alias","messages":[{"content":"first","content":"second"}]}`},
		{"extension duplicate", `{"model":"alias","messages":[{}],"extension":{"x":1,"x":2}}`},
		{"invalid UTF-8", "{\"model\":\"alias\",\"messages\":[{\"content\":\"\xff\"}]}"},
		{"trailing value", `{"model":"alias","messages":[{}]} {}`},
		{"trailing garbage", `{"model":"alias","messages":[{}]} x`},
		{"null", `null`},
		{"body limit", `{"model":"alias","messages":[{}]}`},
		{"truncated", `{"model":"alias","messages":[`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			g, key, _ := fixtureInMemory(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
			if tc.name == "body limit" {
				g.Config.MaxBodyBytes = 16
			}
			w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
			if calls.Load() != 0 {
				t.Fatal("invalid request reached provider")
			}
			rows, err := g.Store.History(context.Background(), "", "", "", 100)
			if err != nil || len(rows) != 0 {
				t.Fatalf("invalid request admitted: %d rows, %v", len(rows), err)
			}
		})
	}
}

func TestChatPreservesProviderExtensions(t *testing.T) {
	const extension = `{"number":9007199254740993,"decimal":0.1234567890123456789,"text":"hello 世界"}`
	g, key, _ := fixtureInMemory(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if string(body["extension"]) != extension {
			t.Errorf("extension changed: %s", body["extension"])
		}
		fmt.Fprint(w, `{"id":"gen-extension","choices":[],"usage":{"cost":0.0000000015}}`)
	})
	w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, `{"model":"alias","messages":[{}],"extension":`+extension+`} `)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	r := onlyRequest(t, g)
	if r.CostUSD == nil || *r.CostUSD != "0.000000002" {
		t.Fatalf("decimal accounting changed: %+v", r)
	}
}
