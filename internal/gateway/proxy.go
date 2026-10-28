package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type observation struct {
	generation    string
	usage         json.RawMessage
	upstreamError bool
}

func (o *observation) inspect(data []byte) error {
	var v struct {
		ID    string          `json:"id"`
		Usage json.RawMessage `json:"usage"`
		Error json.RawMessage `json:"error"`
	}
	if err := jsonv2.Unmarshal(data, &v); err != nil {
		return err
	}
	if len(v.ID) > 512 {
		return errors.New("generation ID too long")
	}
	if o.generation == "" {
		o.generation = v.ID
	}
	if len(v.Usage) > 0 && string(v.Usage) != "null" {
		o.usage = append(o.usage[:0], v.Usage...)
	}
	if len(v.Error) > 0 && string(v.Error) != "null" {
		o.upstreamError = true
	}
	return nil
}

func costFromUsage(raw json.RawMessage, mode string, route Route) (int64, string, error) {
	var usage map[string]json.RawMessage
	if err := json.Unmarshal(raw, &usage); err != nil || usage == nil {
		return 0, "", errors.New("missing usage")
	}
	if mode == "openrouter" || mode == "reported" {
		b := usage["cost"]
		if len(b) == 0 || bytes.Equal(b, []byte("null")) {
			return 0, "", errors.New("missing cost")
		}
		var num json.Number
		if err := json.Unmarshal(b, &num); err != nil {
			return 0, "", errors.New("invalid cost")
		}
		n, err := ParseUSD(num.String())
		return n, num.String(), err
	}
	var input, output int64
	if err := json.Unmarshal(usage["prompt_tokens"], &input); err != nil || input < 0 {
		return 0, "", errors.New("invalid prompt token count")
	}
	if err := json.Unmarshal(usage["completion_tokens"], &output); err != nil || output < 0 {
		return 0, "", errors.New("invalid completion token count")
	}
	// Flat token rates are intentionally limited to uncached text inference. Do
	// not silently price cached, audio, image, or separately charged operations.
	var details map[string]json.RawMessage
	for _, field := range []string{"prompt_tokens_details", "completion_tokens_details"} {
		details = nil
		if b := usage[field]; len(b) > 0 && string(b) != "null" {
			if err := json.Unmarshal(b, &details); err != nil {
				return 0, "", err
			}
		}
		for key, b := range details {
			if key == "reasoning_tokens" {
				continue
			}
			var n int64
			if string(b) == "null" {
				continue
			}
			if err := json.Unmarshal(b, &n); err != nil || n != 0 {
				return 0, "", fmt.Errorf("unsupported pricing dimension %s", key)
			}
		}
	}
	n, err := tokenCost(input, output, route)
	return n, FormatUSD(n), err
}

func (g *Gateway) chat(w http.ResponseWriter, r *http.Request) {
	admissionStarted := time.Now()
	g.Metrics.Requests.Add(1)
	if !g.beginInference() {
		g.reject(w, 503, "shutting_down")
		return
	}
	defer g.handlers.Done()
	handlerContext, stopHandler := context.WithTimeout(r.Context(), seconds(g.Config.RequestTimeoutSeconds))
	defer stopHandler()
	r = r.WithContext(handlerContext)
	secret := bearer(r)
	p, err := g.Store.Authenticate(r.Context(), secret)
	if err != nil {
		g.Metrics.Rejected.Add(1)
		g.fail(w, err)
		return
	}
	release, code := g.limits.acquire(p.UserID, g.Config, time.Now())
	if release == nil {
		w.Header().Set("Retry-After", "1")
		g.reject(w, 429, code)
		return
	}
	defer release()
	r.Body = http.MaxBytesReader(w, r.Body, g.Config.MaxBodyBytes)
	var payload map[string]json.RawMessage
	// Strictly validate the entire request, including provider extension fields.
	if err = jsonv2.UnmarshalRead(r.Body, &payload); err != nil || payload == nil {
		g.reject(w, 400, "invalid_request")
		return
	}
	var model string
	if json.Unmarshal(payload["model"], &model) != nil {
		g.reject(w, 400, "invalid_model")
		return
	}
	route, ok := g.Config.Models[model]
	if !ok {
		g.reject(w, 403, "model_not_allowed")
		return
	}
	for _, field := range []string{"provider", "models", "route", "transforms"} {
		if _, ok = payload[field]; ok {
			g.reject(w, 400, "routing_override_forbidden")
			return
		}
	}
	var messages []json.RawMessage
	if json.Unmarshal(payload["messages"], &messages) != nil || len(messages) == 0 {
		g.reject(w, 400, "messages_required")
		return
	}
	streaming := false
	if b, exists := payload["stream"]; exists {
		if string(b) == "null" || json.Unmarshal(b, &streaming) != nil {
			g.reject(w, 400, "invalid_stream")
			return
		}
	}
	// Validate client limits and enforce any configured cap for both spellings.
	hasLimit := false
	for _, field := range []string{"max_tokens", "max_completion_tokens"} {
		if b, exists := payload[field]; exists {
			var n int
			if json.Unmarshal(b, &n) != nil || n < 1 || (g.Config.MaxOutputTokens > 0 && n > g.Config.MaxOutputTokens) {
				g.reject(w, 400, "invalid_output_limit")
				return
			}
			hasLimit = true
		}
	}
	if !hasLimit && g.Config.MaxOutputTokens > 0 {
		payload["max_tokens"] = json.RawMessage(strconv.Itoa(g.Config.MaxOutputTokens))
	}
	if n, exists := payload["n"]; exists && string(n) != "1" {
		g.reject(w, 400, "multiple_completions_unsupported")
		return
	}
	payload["model"], _ = json.Marshal(route.Model)
	payload["user"], _ = json.Marshal(p.UserID)
	provider := g.Config.Providers[route.Provider]
	if streaming && provider.Accounting != "openrouter" {
		var options map[string]json.RawMessage
		if b := payload["stream_options"]; len(b) > 0 && string(b) != "null" {
			if json.Unmarshal(b, &options) != nil {
				g.reject(w, 400, "invalid_stream_options")
				return
			}
		}
		if options == nil {
			options = map[string]json.RawMessage{}
		}
		options["include_usage"] = json.RawMessage("true")
		payload["stream_options"], _ = json.Marshal(options)
	}
	for field, value := range route.RequestParams {
		payload[field] = value
	}
	body, err := json.Marshal(payload)
	if err != nil {
		g.reject(w, 400, "invalid_request")
		return
	}
	admittedAt := time.Now()
	request, err := g.Store.Admit(r.Context(), secret, model, route, provider, admittedAt, g.Config)
	if err != nil {
		g.Metrics.Rejected.Add(1)
		g.fail(w, err)
		return
	}
	admissionDuration := time.Since(admissionStarted)
	g.Metrics.AdmissionNS.Add(admissionDuration.Nanoseconds())
	g.Metrics.AdmissionCount.Add(1)
	w.Header().Set("Server-Timing", fmt.Sprintf("gateway;dur=%.3f", float64(admissionDuration.Microseconds())/1000))
	g.Metrics.Active.Add(1)
	defer g.Metrics.Active.Add(-1)
	started := time.Now()
	defer func() { g.Metrics.DurationNS.Add(time.Since(started).Nanoseconds()); g.Metrics.Completed.Add(1) }()
	w.Header().Set("X-Request-ID", request.ID)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	ctx, cancel := context.WithTimeout(r.Context(), seconds(g.Config.RequestTimeoutSeconds))
	defer cancel()
	upstream, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(provider.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		g.finalize(request, "failed", "upstream_configuration", observation{}, false)
		apiError(w, 502, "upstream_configuration")
		return
	}
	upstream.Header.Set("Authorization", "Bearer "+os.Getenv(provider.APIKeyEnv))
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("Accept", "application/json")
	if streaming {
		upstream.Header.Set("Accept", "text/event-stream")
	}
	response, err := g.Client.Do(upstream)
	if err != nil {
		code := "upstream_unavailable"
		state := "failed"
		if errors.Is(r.Context().Err(), context.Canceled) {
			code = "client_disconnected"
			state = "cancelled"
		} else if ctx.Err() != nil {
			code = "upstream_timeout"
		} else {
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				code = "upstream_timeout"
			}
		}
		g.Metrics.UpstreamErrors.Add(1)
		g.finalize(request, state, code, observation{}, false)
		apiError(w, 502, code)
		return
	}
	defer response.Body.Close()
	obs := observation{generation: response.Header.Get("X-Generation-Id")}
	if len(obs.generation) > 512 {
		obs.generation = ""
	}
	if obs.generation != "" {
		g.saveGeneration(request.ID, obs.generation)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		g.Metrics.UpstreamErrors.Add(1)
		// A rejected request without a generation ID has not started inference.
		knownZero := obs.generation == "" && (response.StatusCode == 400 || response.StatusCode == 401 || response.StatusCode == 402 || response.StatusCode == 403 || response.StatusCode == 404 || response.StatusCode == 422 || response.StatusCode == 429)
		status := 502
		if response.StatusCode == 429 {
			status = 429
			w.Header().Set("Retry-After", "5")
		}
		g.finalize(request, "failed", "upstream_rejected", obs, knownZero)
		apiError(w, status, "upstream_rejected")
		return
	}
	// An idle timer cancels blocked upstream reads without buffering whole streams.
	idle := time.AfterFunc(seconds(g.Config.StreamIdleSeconds), cancel)
	defer idle.Stop()
	if streaming {
		if !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
			g.Metrics.UpstreamErrors.Add(1)
			g.finalize(request, "failed", "invalid_upstream_stream", obs, false)
			apiError(w, 502, "invalid_upstream_stream")
			return
		}
		g.relayStream(w, r, response, request, &obs, idle, started)
		return
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, g.Config.MaxResponseBytes+1))
	if err != nil || int64(len(data)) > g.Config.MaxResponseBytes {
		g.Metrics.UpstreamErrors.Add(1)
		g.finalize(request, "failed", "upstream_response_incomplete", obs, false)
		apiError(w, 502, "upstream_response_incomplete")
		return
	}
	if err = obs.inspect(data); err != nil {
		g.Metrics.UpstreamErrors.Add(1)
		g.finalize(request, "failed", "invalid_upstream_response", obs, false)
		apiError(w, 502, "invalid_upstream_response")
		return
	}
	if obs.upstreamError {
		g.Metrics.UpstreamErrors.Add(1)
		g.finalize(request, "failed", "upstream_error", obs, false)
		apiError(w, 502, "upstream_error")
		return
	}
	data, err = rebuildCompletion(data, request)
	if err != nil {
		g.Metrics.UpstreamErrors.Add(1)
		g.finalize(request, "failed", "invalid_upstream_response", obs, false)
		apiError(w, 502, "invalid_upstream_response")
		return
	}
	g.finalize(request, "completed", "", obs, false)
	if deadline, ok := r.Context().Deadline(); ok {
		_ = http.NewResponseController(w).SetWriteDeadline(deadline)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(data)
}

func (g *Gateway) reject(w http.ResponseWriter, status int, code string) {
	g.Metrics.Rejected.Add(1)
	apiError(w, status, code)
}
func (g *Gateway) saveGeneration(id, generation string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := g.Store.Generation(ctx, id, generation); err != nil {
		g.Metrics.AccountingErrors.Add(1)
		g.Log.Error("generation persistence failed", "request_id", id)
	}
}

func (g *Gateway) finalize(r Request, state, code string, obs observation, zero bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := g.Store.ObservedUsage(ctx, r.ID, obs.usage); err != nil {
		g.Metrics.AccountingErrors.Add(1)
		g.Log.Error("usage persistence failed", "request_id", r.ID)
	}
	if obs.generation != "" {
		if err := g.Store.Generation(ctx, r.ID, obs.generation); err != nil {
			g.Metrics.AccountingErrors.Add(1)
			g.Log.Error("generation persistence failed", "request_id", r.ID)
		}
	}
	if err := g.Store.Finish(ctx, r.ID, state, code); err != nil {
		g.Metrics.AccountingErrors.Add(1)
		g.Log.Error("request finalization failed", "request_id", r.ID)
		return
	}
	var amount int64
	var raw string
	var err error
	source := "provider_usage"
	if zero {
		raw = "0"
		source = "upstream_rejection"
	} else {
		amount, raw, err = costFromUsage(obs.usage, r.ProviderConfig.Accounting, r.Route)
		if r.ProviderConfig.Accounting == "token_rates" {
			source = "token_rates:" + r.Route.PricingVersion
		}
	}
	if err == nil {
		if err = g.Store.Settle(ctx, r.ID, amount, raw, obs.usage, source, "", false); err != nil {
			g.Metrics.AccountingErrors.Add(1)
			g.Log.Error("charge persistence failed", "request_id", r.ID)
		}
	}
	g.Log.Info("inference finished", "request_id", r.ID, "model", r.Model, "provider", r.Provider, "lifecycle", state, "error_code", code, "accounting_pending", err != nil)
}

func (g *Gateway) relayStream(w http.ResponseWriter, r *http.Request, response *http.Response, request Request, obs *observation, idle *time.Timer, started time.Time) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	controller := http.NewResponseController(w)
	parser := &SSEParser{MaxEventBytes: 2 << 20}
	saved := obs.generation
	done := false
	first := false
	var writeErr error
	writeEvent := func(data []byte) error {
		_ = controller.SetWriteDeadline(time.Now().Add(seconds(g.Config.StreamIdleSeconds)))
		_, writeErr = w.Write(data)
		if writeErr == nil {
			writeErr = controller.Flush()
		}
		return writeErr
	}
	var lastKeepalive time.Time
	parser.OnComment = func() error {
		if time.Since(lastKeepalive) < time.Second {
			return nil
		}
		lastKeepalive = time.Now()
		return writeEvent([]byte(": keepalive\n\n"))
	}
	parser.OnEvent = func(data []byte) error {
		if done {
			return errors.New("event after DONE")
		}
		if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
			done = true
			return nil
		}
		if err := obs.inspect(data); err != nil {
			return err
		}
		if saved == "" && obs.generation != "" {
			g.saveGeneration(request.ID, obs.generation)
			saved = obs.generation
		}
		if obs.upstreamError {
			return errors.New("upstream error")
		}
		data, hasContent, err := rebuildChunk(data, request)
		if err != nil {
			return err
		}
		if !first && hasContent {
			first = true
			g.Metrics.FirstTokenNS.Add(time.Since(started).Nanoseconds())
			g.Metrics.FirstTokenCount.Add(1)
		}
		return writeEvent(append(append([]byte("data: "), data...), '\n', '\n'))
	}
	buf := make([]byte, 32<<10)
	state, code := "completed", ""
	var received int64
	for {
		remaining := g.Config.MaxResponseBytes - received
		readBuf := buf
		if remaining < int64(len(buf)) {
			readBuf = buf[:remaining+1]
		}
		n, readErr := response.Body.Read(readBuf)
		if n > 0 {
			if int64(n) > remaining {
				state = "failed"
				code = "upstream_response_too_large"
				break
			}
			received += int64(n)
			idle.Reset(seconds(g.Config.StreamIdleSeconds))
			if err := parser.Feed(buf[:n]); err != nil {
				state = "failed"
				code = "invalid_upstream_stream"
				break
			}
		}
		if done {
			break
		}
		if readErr != nil {
			state = "failed"
			code = "upstream_stream_incomplete"
			break
		}
	}
	if writeErr != nil || errors.Is(r.Context().Err(), context.Canceled) {
		state = "cancelled"
		code = "client_disconnected"
	}
	if obs.upstreamError {
		state = "failed"
		code = "upstream_error"
	}
	if state == "failed" {
		g.Metrics.UpstreamErrors.Add(1)
		data, _ := json.Marshal(map[string]any{"error": map[string]string{"type": "gateway_error", "code": code, "message": strings.ReplaceAll(code, "_", " ")}})
		_ = controller.SetWriteDeadline(time.Now().Add(time.Second))
		_, _ = fmt.Fprintf(w, "\n\ndata: %s\n\ndata: [DONE]\n\n", data)
		_ = controller.Flush()
	}
	if state == "completed" {
		if err := writeEvent([]byte("data: [DONE]\n\n")); err != nil {
			state, code = "cancelled", "client_disconnected"
		}
	}
	_ = response.Body.Close()
	g.finalize(request, state, code, *obs, false)
}

// SSEParser accepts fragmented CR/LF lines, comments and multiline data with a
// fixed bound on unfinished events. Callbacks receive complete events only.
type SSEParser struct {
	MaxEventBytes int
	OnEvent       func([]byte) error
	OnComment     func() error
	line          []byte
	data          []byte
	size          int
	cr            bool
}

func (p *SSEParser) Feed(chunk []byte) error {
	if p.MaxEventBytes <= 0 {
		p.MaxEventBytes = 2 << 20
	}
	for _, b := range chunk {
		if p.cr {
			p.cr = false
			if b == '\n' {
				continue
			}
		}
		if b == '\r' || b == '\n' {
			if err := p.endLine(); err != nil {
				return err
			}
			p.cr = b == '\r'
			continue
		}
		p.size++
		if p.size > p.MaxEventBytes {
			return errors.New("SSE event too large")
		}
		p.line = append(p.line, b)
	}
	return nil
}
func (p *SSEParser) endLine() error {
	line := p.line
	p.line = nil
	if len(line) == 0 {
		if len(p.data) > 0 {
			data := p.data[:len(p.data)-1]
			if p.OnEvent != nil {
				if err := p.OnEvent(data); err != nil {
					return err
				}
			}
		}
		p.data = nil
		p.size = 0
		return nil
	}
	if line[0] == ':' {
		p.size -= len(line)
		if p.OnComment != nil {
			return p.OnComment()
		}
		return nil
	}
	name, value, found := bytes.Cut(line, []byte(":"))
	if !found {
		value = nil
	}
	if len(value) > 0 && value[0] == ' ' {
		value = value[1:]
	}
	if bytes.Equal(name, []byte("data")) {
		p.data = append(p.data, value...)
		p.data = append(p.data, '\n')
	}
	return nil
}
