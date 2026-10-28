package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRequestParamsConfiguration(t *testing.T) {
	c, err := loadRequestParamsConfig(t, "    request_params: {temperature: 0, reasoning: {effort: low}, extra: [null, false, 9007199254740993]}\n")
	if err != nil {
		t.Fatal(err)
	}
	want := RequestParams{"temperature": json.RawMessage(`0`), "reasoning": json.RawMessage(`{"effort":"low"}`), "extra": json.RawMessage(`[null,false,9007199254740993]`)}
	if !reflect.DeepEqual(c.Models["default"].RequestParams, want) {
		t.Fatalf("lost values: %+v", c.Models["default"])
	}
	for _, settings := range []string{"", "    request_params: {}\n", "    <<: {request_params: {temperature: 0}}\n", "    request_params: {extra: &items [false, null], copied: *items}\n"} {
		if _, err := loadRequestParamsConfig(t, settings); err != nil {
			t.Fatal(err)
		}
	}
	cases := []string{"null", "hello", "[]", "{extra: {key: 1, key: 2}}", "{extra: {1: value}}", "{extra: .inf}", "{extra: .nan}", "{extra: 2026-10-27}", "{extra: !custom value}", "{extra: &loop [*loop]}"}
	for _, field := range requestReservedFields {
		cases = append(cases, "{"+field+": null}")
	}
	for _, settings := range cases {
		t.Run(settings, func(t *testing.T) {
			if _, err := loadRequestParamsConfig(t, "    request_params: "+settings+"\n"); err == nil || !strings.Contains(err.Error(), "model default request_params") {
				t.Fatalf("expected contextual rejection, got %v", err)
			}
		})
	}
	for _, settings := range []string{"    openrouter: {}\n", "    openrouter: null\n", "    request_params: {}\n    openrouter: {}\n", "    <<: {openrouter: {temperature: 0}}\n"} {
		if _, err := loadRequestParamsConfig(t, settings); err == nil || !strings.Contains(err.Error(), "field openrouter") {
			t.Fatalf("accepted removed config field: %v", err)
		}
	}
	route := c.Models["default"]
	route.RequestParams = RequestParams{"extra": json.RawMessage(`NaN`)}
	c.Models["default"] = route
	if c.Validate() == nil {
		t.Fatal("accepted invalid programmatic JSON")
	}
	if _, err := loadRequestParamsConfig(t, "    request_param: {}\n"); err == nil {
		t.Fatal("accepted unknown route field")
	}
}

func TestCompatibleConnections(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, mode := range []string{"reported", "token_rates"} {
			t.Run(fmt.Sprintf("stream=%t/%s", streaming, mode), func(t *testing.T) {
				settings := RequestParams{"reasoning": json.RawMessage(`{"effort":"low"}`), "temperature": json.RawMessage(`0`)}
				usage := `{"prompt_tokens":10,"completion_tokens":5,"cost":0.003}`
				handler := func(model, credential, prefix string) http.HandlerFunc {
					return func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path != prefix+"/chat/completions" || r.Header.Get("Authorization") != "Bearer "+credential {
							t.Errorf("wrong connection: %s %s", r.URL.Path, r.Header.Get("Authorization"))
						}
						var body map[string]json.RawMessage
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						if string(body["model"]) != fmt.Sprintf("%q", model) || string(body["reasoning"]) != string(settings["reasoning"]) || string(body["temperature"]) != "0" || string(body["top_p"]) != "0.9" || string(body["max_tokens"]) != "100" || string(body["user"]) == `"spoof"` {
							t.Errorf("wrong payload: %s", body)
						}
						if streaming {
							if string(body["stream_options"]) != `{"include_usage":true,"other":false}` {
								t.Errorf("wrong stream options: %s", body["stream_options"])
							}
							w.Header().Set("Content-Type", "text/event-stream")
							fmt.Fprintf(w, "data: {\"id\":\"gen-test\",\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: {\"id\":\"gen-test\",\"choices\":[],\"usage\":%s}\n\ndata: [DONE]\n\n", usage)
						} else {
							w.Header().Set("Content-Type", "application/json")
							fmt.Fprintf(w, `{"id":"gen-test","model":%q,"choices":[],"usage":%s}`, model, usage)
						}
					}
				}
				g, key, _ := fixture(t, handler("first/model", "provider-secret", "/inference/v1"))
				p := g.Config.Providers["upstream"]
				p.BaseURL += "/inference/v1/"
				p.Accounting = mode
				g.Config.Providers["upstream"] = p
				second := httptest.NewTLSServer(handler("second/model", "second-secret", "/v1/openai"))
				defer second.Close()
				// Both httptest servers use the same test certificate trusted by the fixture client.
				t.Setenv("SECOND_PROVIDER_KEY", "second-secret")
				g.Config.Providers["second"] = Provider{BaseURL: second.URL + "/v1/openai", APIKeyEnv: "SECOND_PROVIDER_KEY", Accounting: mode}
				for _, alias := range []string{"alias", "second"} {
					route := testRoute()
					if alias == "second" {
						route.Provider = "second"
						route.Model = "second/model"
					} else {
						route.Model = "first/model"
					}
					route.RequestParams = settings
					route.InputUSDPerMillion = "100"
					route.OutputUSDPerMillion = "400"
					route.PricingVersion = "test-v1"
					g.Config.Models[alias] = route
				}
				g.Config.ModelAccessGroups["free"] = ModelAccessGroup{Models: []string{"alias", "second"}}
				if err := g.Config.Validate(); err != nil {
					t.Fatal(err)
				}
				for _, alias := range []string{"alias", "second", "alias"} {
					body := fmt.Sprintf(`{"model":%q,"messages":[{}],"stream":%t,"stream_options":{"include_usage":false,"other":false},"reasoning":{"effort":"high","exclude":true},"temperature":1,"top_p":0.9,"user":"spoof"}`, alias, streaming)
					w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, body)
					if w.Code != 200 {
						t.Fatalf("%d %s", w.Code, w.Body)
					}
					if streaming && !strings.Contains(w.Body.String(), "hello") {
						t.Fatal("lost stream content")
					}
					if !streaming && !strings.Contains(w.Body.String(), `"model":"`+g.Config.Models[alias].Model+`"`) {
						t.Fatal("upstream model not preserved")
					}
				}
				requests, err := g.Store.History(context.Background(), "", "", "", 100)
				if err != nil || len(requests) != 3 {
					t.Fatalf("history: %v %v", requests, err)
				}
				for _, req := range requests {
					if req.Accounting != "settled" || req.CostUSD == nil || *req.CostUSD != "0.003000000" || !reflect.DeepEqual(req.Route.RequestParams, settings) {
						t.Fatalf("settlement/snapshot: %+v", req)
					}
				}
				if string(settings["reasoning"]) != `{"effort":"low"}` {
					t.Fatal("mutated settings")
				}
				for _, field := range []string{"provider", "models", "route", "transforms"} {
					w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, fmt.Sprintf(`{"model":"alias","messages":[{}],%q:null}`, field))
					if w.Code != 400 || !strings.Contains(w.Body.String(), "routing_override_forbidden") {
						t.Fatalf("routing override accepted: %d %s", w.Code, w.Body)
					}
				}
			})
		}
	}
}

func TestCompatibleMissingUsage(t *testing.T) {
	for _, mode := range []string{"reported", "token_rates"} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", mode, streaming), func(t *testing.T) {
				g, key, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/chat/completions" {
						t.Error("unexpected generation lookup")
					}
					if streaming {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, "data: {\"id\":\"gen-incomplete\",\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
					} else {
						fmt.Fprint(w, `{"id":"gen-missing","choices":[]}`)
					}
				})
				p := g.Config.Providers["upstream"]
				p.Accounting = mode
				g.Config.Providers["upstream"] = p
				w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, fmt.Sprintf(`{"model":"alias","messages":[{}],"stream":%t}`, streaming))
				req := onlyRequest(t, g)
				if w.Code != 200 || req.Accounting != "pending" {
					t.Fatalf("%d %+v", w.Code, req)
				}
				if streaming && !strings.Contains(w.Body.String(), "upstream_stream_incomplete") {
					t.Fatal("missing incomplete stream error")
				}
				if err := g.reconcileOne(context.Background(), req); err == nil {
					t.Fatal("settled without usage")
				}
				if err := g.Store.Retry(context.Background(), req, 1); err != nil {
					t.Fatal(err)
				}
				stored, err := g.Store.Request(context.Background(), req.ID)
				if err != nil || stored.Accounting != "unresolved" {
					t.Fatalf("%+v %v", stored, err)
				}
			})
		}
	}
}

func TestRequestParamsSnapshots(t *testing.T) {
	s := testStore(t)
	_, key := provision(t, s, "1")
	for _, field := range []string{"configured", "absent"} {
		route := testRoute()
		params := RequestParams{"extra": json.RawMessage(`9007199254740993`)}
		if field == "configured" {
			route.RequestParams = params
		}
		req, err := s.Admit(context.Background(), key, "alias", route, Provider{Accounting: "reported"}, time.Now(), testAccessConfig())
		if err != nil {
			t.Fatal(err)
		}
		params["extra"] = json.RawMessage(`0`)
		stored, err := s.Request(context.Background(), req.ID)
		if err != nil {
			t.Fatal(err)
		}
		if field == "configured" {
			if string(stored.Route.RequestParams["extra"]) != "9007199254740993" {
				t.Fatalf("lost request settings snapshot: %+v", stored.Route)
			}
		} else if stored.Route.RequestParams != nil {
			t.Fatalf("unexpected request settings: %+v", stored.Route)
		}
	}
}
