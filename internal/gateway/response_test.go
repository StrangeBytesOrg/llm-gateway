package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func equalResponseJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatalf("invalid output JSON: %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

// Unknown fields occur at every object level. Approved opaque strings are data,
// even when their contents look like accounting fields.
const privateMessage = `{
 "role":"assistant","content":"{\"cost\":123}","refusal":null,"private":"__PRIVATE__",
 "tool_calls":[{"id":"call-1","index":0,"type":"function","private":"__PRIVATE__","function":{"name":"lookup","arguments":"{\"cost\":7}","private":"__PRIVATE__"}}],
 "function_call":{"name":"legacy","arguments":"{}","private":"__PRIVATE__"},
 "reasoning":"reason","reasoning_content":"thought",
 "reasoning_details":[{"type":"reasoning.text","text":"think","signature":"signed","id":null,"format":"anthropic-claude-v1","index":0,"private":"__PRIVATE__"},{"type":"reasoning.encrypted","data":"opaque","private":"__PRIVATE__"},{"type":"reasoning.summary","summary":"summary","private":"__PRIVATE__"}],
 "thinking_blocks":[{"type":"thinking","thinking":"think","signature":"signed","private":"__PRIVATE__"},{"type":"redacted_thinking","data":"opaque","private":"__PRIVATE__"}],
 "annotations":[{"type":"url_citation","private":"__PRIVATE__","url_citation":{"start_index":0,"end_index":2,"url":"https://example.com","title":"source","private":"__PRIVATE__"}}]
}`
const privateLogprobs = `{"private":"__PRIVATE__","content":[{"token":"ok","logprob":-0.1,"bytes":[111,107],"private":"__PRIVATE__","top_logprobs":[{"token":"OK","logprob":-2,"bytes":null,"private":"__PRIVATE__"}]}],"refusal":null}`
const privateUsage = `{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"cost":0.025,"cost_details":{"private":"__PRIVATE__"},"private":"__PRIVATE__","prompt_tokens_details":{"cached_tokens":0,"audio_tokens":null,"cost":"__PRIVATE__"},"completion_tokens_details":{"reasoning_tokens":1,"audio_tokens":0,"accepted_prediction_tokens":0,"rejected_prediction_tokens":0,"cost":"__PRIVATE__"}}`

// Force network reads to split at arbitrary positions, including inside secret
// keys and values. Upstream HTTP writes alone cannot guarantee these boundaries.
type fragmentedBody struct {
	io.ReadCloser
	width int
}

func (b fragmentedBody) Read(p []byte) (int, error) {
	if len(p) > b.width {
		p = p[:b.width]
	}
	return b.ReadCloser.Read(p)
}

type responseTransport func(*http.Request) (*http.Response, error)

func (f responseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestResponseProjectionAndAccounting(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, width := range []int{1, 7, 32768} {
			t.Run(fmt.Sprintf("stream=%t/width=%d", stream, width), func(t *testing.T) {
				g, key, _ := fixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected real upstream") })
				member := "message"
				if stream {
					member = "delta"
				}
				original := fmt.Sprintf(`{"id":"__PRIVATE__generation","model":"__PRIVATE__model","system_fingerprint":"__PRIVATE__","service_tier":"__PRIVATE__","private":"__PRIVATE__","choices":[{"index":0,%q:%s,"finish_reason":"tool_calls","logprobs":%s,"private":"__PRIVATE__"}],"usage":%s}`, member, privateMessage, privateLogprobs, privateUsage)
				original = strings.ReplaceAll(original, "\n", "")
				body, contentType := original, "application/json"
				if stream {
					// All upstream SSE metadata and comments must be discarded.
					body = ": __PRIVATE__comment\r\nid: __PRIVATE__id\r\nevent: __PRIVATE__event\r\nretry: 99999\r\ndata: " + original + "\r\n\r\ndata: [DONE]\r\n\r\n"
					contentType = "text/event-stream"
				}
				g.Client.Transport = responseTransport(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}, "X-Generation-Id": {"__PRIVATE__generation"}, "X-Cost": {"__PRIVATE__"}}, Body: fragmentedBody{io.NopCloser(strings.NewReader(body)), width}}, nil
				})
				w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, fmt.Sprintf(`{"model":"alias","messages":[{}],"stream":%t}`, stream))
				if w.Code != 200 || strings.Contains(w.Body.String(), "__PRIVATE__") || strings.Contains(fmt.Sprint(w.Header()), "__PRIVATE__") {
					t.Fatalf("unsafe response: %d %s %s", w.Code, w.Header(), w.Body)
				}
				var events [][]byte
				if stream {
					parser := SSEParser{OnEvent: func(b []byte) error { events = append(events, append([]byte(nil), b...)); return nil }}
					if err := parser.Feed(w.Body.Bytes()); err != nil {
						t.Fatal(err)
					}
					if len(events) != 2 || string(events[1]) != "[DONE]" {
						t.Fatalf("events: %q", events)
					}
				} else {
					events = [][]byte{w.Body.Bytes()}
				}
				req := onlyRequest(t, g)
				if req.GenerationID != "__PRIVATE__generation" || req.Accounting != "settled" || req.Lifecycle != "completed" || req.CostUSD == nil || *req.CostUSD != "0.025000000" {
					t.Fatalf("accounting: %+v", req)
				}
				equalResponseJSON(t, req.Usage, privateUsage)
				// Compare against the independently defined public contract, not a round
				// trip through the implementation's types.
				wantMessage := `{"role":"assistant","content":"{\"cost\":123}","refusal":null,"tool_calls":[{"id":"call-1","index":0,"type":"function","function":{"name":"lookup","arguments":"{\"cost\":7}"}}],"function_call":{"name":"legacy","arguments":"{}"},"reasoning":"reason","reasoning_content":"thought","reasoning_details":[{"type":"reasoning.text","text":"think","signature":"signed","id":null,"format":"anthropic-claude-v1","index":0},{"type":"reasoning.encrypted","data":"opaque"},{"type":"reasoning.summary","summary":"summary"}],"thinking_blocks":[{"type":"thinking","thinking":"think","signature":"signed"},{"type":"redacted_thinking","data":"opaque"}],"annotations":[{"type":"url_citation","url_citation":{"start_index":0,"end_index":2,"url":"https://example.com","title":"source"}}]}`
				wantLogprobs := `{"content":[{"token":"ok","logprob":-0.1,"bytes":[111,107],"top_logprobs":[{"token":"OK","logprob":-2,"bytes":null}]}],"refusal":null}`
				wantUsage := `{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"prompt_tokens_details":{"cached_tokens":0,"audio_tokens":null},"completion_tokens_details":{"reasoning_tokens":1,"audio_tokens":0,"accepted_prediction_tokens":0,"rejected_prediction_tokens":0}}`
				object := "chat.completion"
				if stream {
					object += ".chunk"
				}
				equalResponseJSON(t, events[0], fmt.Sprintf(`{"id":%q,"object":%q,"created":%d,"model":"alias","choices":[{"index":0,%q:%s,"finish_reason":"tool_calls","logprobs":%s}],"usage":%s}`, "chatcmpl-"+req.ID, object, req.Created, member, wantMessage, wantLogprobs, wantUsage))
			})
		}
	}
}

func TestResponseNullsAndDefaults(t *testing.T) {
	req := Request{ID: "req_test", Model: "alias", Created: 42}
	for _, tc := range []struct{ input, expected string }{
		{`{"choices":[{"message":{"content":null}}]}`, `{"id":"chatcmpl-req_test","object":"chat.completion","created":42,"model":"alias","choices":[{"index":0,"message":{"role":"assistant","content":null}}]}`},
		{`{"choices":[{"message":{"role":null,"content":"","tool_calls":[],"reasoning":null},"finish_reason":null,"logprobs":null}]}`, `{"id":"chatcmpl-req_test","object":"chat.completion","created":42,"model":"alias","choices":[{"index":0,"message":{"role":null,"content":"","tool_calls":[],"reasoning":null},"finish_reason":null,"logprobs":null}]}`},
		{`{"choices":[],"usage":null}`, `{"id":"chatcmpl-req_test","object":"chat.completion","created":42,"model":"alias","choices":[],"usage":null}`},
		{`{"choices":[],"usage":{"cost":0.01}}`, `{"id":"chatcmpl-req_test","object":"chat.completion","created":42,"model":"alias","choices":[],"usage":{}}`},
	} {
		b, err := rebuildCompletion([]byte(tc.input), req)
		if err != nil {
			t.Fatal(err)
		}
		equalResponseJSON(t, b, tc.expected)
	}
}

func TestResponseRejectsMalformedSupportedData(t *testing.T) {
	for _, bad := range []string{
		`null`, `[]`, `{}`, `{"choices":null}`, `{"choices":[null]}`, `{"choices":[{}]}`,
		`{"choices":[{"message":{"content":{"cost":1}}}]}`,
		`{"choices":[{"message":{"content":"ok","content":"__PRIVATE__"}}]}`,
		`{"choices":[{"message":{"role":"user"}}]}`,
		`{"choices":[{"index":-1,"message":{}}]}`,
		`{"choices":[{"message":{"tool_calls":[{"function":{"arguments":{}}}]}}]}`,
		`{"choices":[{"message":{"tool_calls":[null]}}]}`,
		`{"choices":[{"message":{"reasoning_details":[null]}}]}`,
		`{"choices":[{"message":{},"logprobs":{"content":[null]}}]}`,
		`{"choices":[],"usage":{"prompt_tokens":-1}}`,
		`{"choices":[],"usage":{"completion_tokens_details":{"reasoning_tokens":"1"}}}`,
		`{"choices":[],"usage":{"prompt_tokens_details":[]}}`,
		`{"choices":[],"usage":12}`,
		`{"choices":[{"message":{"annotations":[{"url_citation":{"url":42}}]}}]}`,
		`{"choices":[{"message":{"reasoning_details":[{"signature":{}}]}}]}`,
		"{\"choices\":[],\"private\":\"\xff\"}",
		`{"choices":[],"private":{"x":1,"x":2}}`,
		`{"choices":[]} {}`,
	} {
		t.Run(bad, func(t *testing.T) {
			if b, err := rebuildCompletion([]byte(bad), Request{}); err == nil {
				t.Fatalf("accepted malformed response: %s", b)
			}
			stream := strings.ReplaceAll(bad, `"message":`, `"delta":`)
			if b, _, err := rebuildChunk([]byte(stream), Request{}); err == nil {
				t.Fatalf("accepted malformed chunk: %s", b)
			}
		})
	}
}

func TestStreamSanitizesErrorsAndRetainsAccounting(t *testing.T) {
	for name, tail := range map[string]string{
		"upstream error":  `{"error":{"message":"__PRIVATE__","metadata":{"cost":1}},"usage":{"cost":0.025}}` + "\n\n",
		"malformed field": `{"choices":[{"delta":{"content":{"private":"__PRIVATE__"}}}],"usage":{"cost":0.025}}` + "\n\n",
		"truncated event": `{"choices":[{"delta":{"content":"__PRIVATE__`,
		"invalid JSON":    "__PRIVATE__\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			g, key, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"id\":\"private-generation\",\"choices\":[{\"delta\":{\"content\":\"visible\"}}]}\n\ndata: "+tail)
			})
			w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, `{"model":"alias","messages":[{}],"stream":true}`)
			body := w.Body.String()
			if strings.Contains(body, "__PRIVATE__") || strings.Contains(body, "private-generation") || !strings.Contains(body, "visible") || !strings.Contains(body, "gateway_error") || strings.Count(body, "[DONE]") != 1 {
				t.Fatalf("unsafe stream: %s", body)
			}
			req := onlyRequest(t, g)
			if req.Lifecycle != "failed" || req.GenerationID != "private-generation" {
				t.Fatalf("request: %+v", req)
			}
			if strings.Contains(tail, `"usage"`) && (req.Accounting != "settled" || req.CostUSD == nil || *req.CostUSD != "0.025000000") {
				t.Fatalf("lost accounting: %+v", req)
			}
		})
	}
}

func TestStreamUsageAndIdentity(t *testing.T) {
	g, key, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"private-generation\",\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"cost\":0.025,\"prompt_tokens\":10}}\n\ndata: [DONE]\n\n")
	})
	w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, `{"model":"alias","messages":[{}],"stream":true}`)
	req := onlyRequest(t, g)
	var ids []string
	parser := SSEParser{OnEvent: func(b []byte) error {
		if string(b) == "[DONE]" {
			return nil
		}
		var v struct {
			ID, Model string
			Created   int64
			Usage     map[string]json.RawMessage
		}
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		ids = append(ids, v.ID)
		if v.ID != "chatcmpl-"+req.ID || v.Model != "alias" || v.Created != req.Created {
			t.Errorf("unstable identity: %s", b)
		}
		if _, ok := v.Usage["cost"]; ok {
			t.Errorf("leaked cost: %s", b)
		}
		return nil
	}}
	if err := parser.Feed(w.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || req.Accounting != "settled" {
		t.Fatalf("events=%d request=%+v", len(ids), req)
	}
}

func TestCompletionFailureKeepsPrivateUsage(t *testing.T) {
	for _, body := range []string{
		`{"id":"private-generation","error":{"message":"__PRIVATE__"},"usage":{"cost":0.025}}`,
		`{"id":"private-generation","choices":[{"message":{"content":{"secret":"__PRIVATE__"}}}],"usage":{"cost":0.025}}`,
	} {
		g, key, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
		w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", key, `{"model":"alias","messages":[{}]}`)
		if w.Code != 502 || strings.Contains(w.Body.String(), "__PRIVATE__") || strings.Contains(w.Body.String(), "private-generation") {
			t.Fatalf("unsafe error: %d %s", w.Code, w.Body)
		}
		req := onlyRequest(t, g)
		if req.Lifecycle != "failed" || req.GenerationID != "private-generation" || req.Accounting != "settled" || req.CostUSD == nil || *req.CostUSD != "0.025000000" {
			t.Fatalf("lost accounting: %+v", req)
		}
	}
}

func BenchmarkResponseNormalization(b *testing.B) {
	request := Request{ID: "req_benchmark", Model: "alias", Created: 42}
	for _, stream := range []bool{false, true} {
		member := "message"
		if stream {
			member = "delta"
		}
		data := []byte(fmt.Sprintf(`{"id":"gen_benchmark","choices":[{%q:{"role":"assistant","content":"hello"}}],"usage":{"prompt_tokens":10,"completion_tokens":1,"cost":0.001}}`, member))
		for _, rebuild := range []bool{false, true} {
			b.Run(fmt.Sprintf("stream=%t/rebuild=%t", stream, rebuild), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					var obs observation
					if err := obs.inspect(data); err != nil {
						b.Fatal(err)
					}
					if rebuild {
						if stream {
							if _, _, err := rebuildChunk(data, request); err != nil {
								b.Fatal(err)
							}
						} else {
							if _, err := rebuildCompletion(data, request); err != nil {
								b.Fatal(err)
							}
						}
					}
				}
			})
		}
	}
}
