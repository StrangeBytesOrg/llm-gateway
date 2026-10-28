package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func loadRequestParamsConfig(t *testing.T, setting string) (Config, error) {
	t.Helper()
	t.Setenv("GATEWAY_ADMIN_TOKEN", strings.Repeat("a", 40))
	t.Setenv("OPENROUTER_API_KEY", "test")
	data, err := os.ReadFile("../../config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "    model: openai/gpt-4o-mini\n", "    model: openai/gpt-4o-mini\n"+setting, 1))
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return LoadConfig(path)
}

func TestRequestSettingsConfigValues(t *testing.T) {
	c, err := loadRequestParamsConfig(t, `    request_params:
      provider:
        only: [openai]
        allow_fallbacks: false
        max_price: {prompt: "0.15"}
      reasoning: {effort: low}
      temperature: 0
      plugins: [{id: web, enabled: false}]
      future_option: {items: [null, true, 9007199254740993, [], {}]}
`)
	if err != nil {
		t.Fatal(err)
	}
	settings := c.Models["default"].RequestParams
	want := RequestParams{
		"provider":      json.RawMessage(`{"allow_fallbacks":false,"max_price":{"prompt":"0.15"},"only":["openai"]}`),
		"reasoning":     json.RawMessage(`{"effort":"low"}`),
		"temperature":   json.RawMessage(`0`),
		"plugins":       json.RawMessage(`[{"enabled":false,"id":"web"}]`),
		"future_option": json.RawMessage(`{"items":[null,true,9007199254740993,[],{}]}`),
	}
	if !reflect.DeepEqual(settings, want) {
		t.Fatalf("settings: %s; want %s", settings, want)
	}
	for _, setting := range []string{"", "    request_params: {}\n"} {
		c, err := loadRequestParamsConfig(t, setting)
		if err != nil || len(c.Models["default"].RequestParams) != 0 {
			t.Fatalf("empty settings: %+v %v", c, err)
		}
	}
}

func TestRequestSettingsConfigRejectsInvalidSettings(t *testing.T) {
	cases := map[string]string{
		"null": "null", "scalar": "hello", "array": "[]",
		"duplicate":       "{provider: {only: [openai], only: [anthropic]}}",
		"non-string key":  "{provider: {1: value}}",
		"non-finite":      "{temperature: .inf}",
		"nan":             "{temperature: .nan}",
		"timestamp":       "{future_option: 2026-10-27}",
		"tag":             "{future_option: !custom value}",
		"recursive alias": "{future_option: &loop [*loop]}",
	}
	for _, field := range requestReservedFields {
		cases["reserved "+field] = "{" + field + ": null}"
	}
	for name, setting := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := loadRequestParamsConfig(t, "    request_params: "+setting+"\n")
			if err == nil || !strings.Contains(err.Error(), "model default request_params") {
				t.Fatalf("expected contextual error, got %v", err)
			}
		})
	}
	_, err := loadRequestParamsConfig(t, "    openruter: {}\n")
	if err == nil || !strings.Contains(err.Error(), "field openruter") {
		t.Fatalf("lost strict route validation: %v", err)
	}
}

func TestRequestSettingsConfigRejectsInvalidJSON(t *testing.T) {
	c, err := loadRequestParamsConfig(t, "")
	if err != nil {
		t.Fatal(err)
	}
	route := c.Models["default"]
	route.RequestParams = RequestParams{"temperature": json.RawMessage(`NaN`)}
	c.Models["default"] = route
	if err := c.Validate(); err == nil {
		t.Fatal("accepted invalid programmatic JSON")
	}
}

func TestRequestSettingsYAMLAliases(t *testing.T) {
	c, err := loadRequestParamsConfig(t, `    <<: {request_params: {temperature: 0}}
`)
	if err != nil || string(c.Models["default"].RequestParams["temperature"]) != "0" {
		t.Fatalf("lost merged route setting: %+v %v", c.Models["default"], err)
	}
	_, err = loadRequestParamsConfig(t, "    <<: {request_params: null}\n")
	if err == nil || !strings.Contains(err.Error(), "model default request_params") {
		t.Fatalf("accepted null in merged route: %v", err)
	}
	c, err = loadRequestParamsConfig(t, `    request_params:
      provider:
        only: &providers [openai]
        order: *providers
`)
	if err != nil || string(c.Models["default"].RequestParams["provider"]) != `{"only":["openai"],"order":["openai"]}` {
		t.Fatalf("lost aliased setting: %+v %v", c.Models["default"], err)
	}
}

func TestRequestSettingsForwarding(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, accounting := range []string{"openrouter", "reported"} {
			t.Run(fmt.Sprintf("stream=%t/accounting=%s", streaming, accounting), func(t *testing.T) {
				settings := RequestParams{
					"provider":      json.RawMessage(`{"only":["openai"],"allow_fallbacks":false}`),
					"reasoning":     json.RawMessage(`{"effort":"low"}`),
					"temperature":   json.RawMessage(`0`),
					"plugins":       json.RawMessage(`[{"id":"web","enabled":false}]`),
					"future_option": json.RawMessage(`null`),
					"transforms":    json.RawMessage(`[]`),
				}
				g, key, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
					var body map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					for field, want := range settings {
						if string(body[field]) != string(want) {
							t.Errorf("%s = %s; want %s", field, body[field], want)
						}
					}
					if string(body["top_p"]) != "0.9" || string(body["max_tokens"]) != "100" || string(body["model"]) != `"model"` || string(body["user"]) == `"spoof"` {
						t.Errorf("lost client field or gateway control: %s", body)
					}
					if streaming {
						if string(body["stream"]) != "true" {
							t.Errorf("lost streaming: %s", body)
						}
						if accounting == "reported" && string(body["stream_options"]) != `{"include_usage":true}` {
							t.Errorf("lost usage options: %s", body)
						}
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, "data: {\"id\":\"gen-settings\",\"choices\":[],\"usage\":{\"cost\":0.01}}\n\ndata: [DONE]\n\n")
					} else {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, `{"id":"gen-settings","choices":[],"usage":{"cost":0.01}}`)
					}
				})
				route := g.Config.Models["alias"]
				route.RequestParams = settings
				g.Config.Models["alias"] = route
				provider := g.Config.Providers[route.Provider]
				provider.Accounting = accounting
				g.Config.Providers[route.Provider] = provider
				before, err := json.Marshal(settings)
				if err != nil {
					t.Fatal(err)
				}
				for range 2 {
					body := fmt.Sprintf(`{"model":"alias","messages":[{}],"stream":%t,"user":"spoof","reasoning":{"effort":"high","exclude":true},"temperature":1,"plugins":[],"future_option":true,"top_p":0.9}`, streaming)
					w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, body)
					if w.Code != 200 {
						t.Fatalf("%d %s", w.Code, w.Body)
					}
				}
				after, err := json.Marshal(settings)
				if err != nil || string(before) != string(after) {
					t.Fatalf("mutated settings: %s %v", after, err)
				}
				requests, err := g.Store.History(context.Background(), "", "", "", 100)
				if err != nil || len(requests) != 2 {
					t.Fatalf("history: %v %v", requests, err)
				}
				for _, request := range requests {
					if request.Accounting != "settled" || !reflect.DeepEqual(request.Route.RequestParams, settings) {
						t.Fatalf("lost settings or settlement: %+v", request)
					}
				}
			})
		}
	}
}

func TestRequestSettingsRoutingOverridesRemainForbidden(t *testing.T) {
	var reached bool
	g, key, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) { reached = true })
	route := g.Config.Models["alias"]
	route.RequestParams = RequestParams{"provider": json.RawMessage(`{"only":["openai"]}`)}
	g.Config.Models["alias"] = route
	for _, field := range []string{"provider", "models", "route", "transforms"} {
		body := fmt.Sprintf(`{"model":"alias","messages":[{}],%q:null}`, field)
		w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, body)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "routing_override_forbidden") {
			t.Fatalf("%s: %d %s", field, w.Code, w.Body)
		}
	}
	if reached {
		t.Fatal("forbidden request reached upstream")
	}
}

func TestRequestSettingsAliasIsolation(t *testing.T) {
	var bodies []map[string]json.RawMessage
	g, key, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[],"usage":{"cost":0.01}}`)
	})
	route := testRoute()
	route.RequestParams = RequestParams{"temperature": json.RawMessage(`0`)}
	g.Config.Models["configured"] = route
	g.Config.ModelAccessGroups["free"] = ModelAccessGroup{Models: []string{"alias", "configured"}}
	for _, alias := range []string{"configured", "alias", "configured"} {
		w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, fmt.Sprintf(`{"model":%q,"messages":[{}],"temperature":1}`, alias))
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
	for i, want := range []string{"0", "1", "0"} {
		if string(bodies[i]["temperature"]) != want {
			t.Fatalf("request %d: %s", i, bodies[i])
		}
	}
}
