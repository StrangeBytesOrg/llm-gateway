package gateway

import (
	json "encoding/json/v2"
	"errors"
)

// responseField preserves the distinction between omitted, null, and zero values.
// Every public object below is a closed projection: no raw objects or extension maps.
type responseField[T any] struct {
	Value   *T
	Present bool
}

func (f responseField[T]) IsZero() bool { return !f.Present }
func (f *responseField[T]) UnmarshalJSON(b []byte) error {
	f.Present = true
	return json.Unmarshal(b, &f.Value)
}
func (f responseField[T]) MarshalJSON() ([]byte, error) { return json.Marshal(f.Value) }
func responseValue[T any](v T) responseField[T]         { return responseField[T]{Value: &v, Present: true} }

// Counts and indices must be nonnegative integers, never arbitrary JSON numbers.
type responseCount int64

func (n *responseCount) UnmarshalJSON(b []byte) error {
	var value int64
	if err := json.Unmarshal(b, &value); err != nil {
		return err
	}
	if value < 0 {
		return errors.New("negative response count")
	}
	*n = responseCount(value)
	return nil
}

// Structured array members cannot be null. Optional arrays themselves may be.
type responseList[T any] []T

func (list *responseList[T]) UnmarshalJSON(b []byte) error {
	var items []*T
	if err := json.Unmarshal(b, &items); err != nil {
		return err
	}
	*list = make(responseList[T], 0, len(items))
	for _, item := range items {
		if item == nil {
			return errors.New("null response array member")
		}
		*list = append(*list, *item)
	}
	return nil
}

type completionResponse[C any] struct {
	ID      string                       `json:"id"`
	Object  string                       `json:"object"`
	Created int64                        `json:"created"`
	Model   string                       `json:"model"`
	Choices []C                          `json:"choices"`
	Usage   responseField[responseUsage] `json:"usage,omitzero"`
}
type completionChoice struct {
	Index        int                             `json:"index"`
	Message      responseField[responseMessage]  `json:"message,omitzero"`
	FinishReason responseField[string]           `json:"finish_reason,omitzero"`
	Logprobs     responseField[responseLogprobs] `json:"logprobs,omitzero"`
}
type streamChoice struct {
	Index        int                             `json:"index"`
	Delta        responseField[responseMessage]  `json:"delta,omitzero"`
	FinishReason responseField[string]           `json:"finish_reason,omitzero"`
	Logprobs     responseField[responseLogprobs] `json:"logprobs,omitzero"`
}
type responseMessage struct {
	Role             responseField[string]                                `json:"role,omitzero"`
	Content          responseField[string]                                `json:"content,omitzero"`
	Refusal          responseField[string]                                `json:"refusal,omitzero"`
	ToolCalls        responseField[responseList[responseToolCall]]        `json:"tool_calls,omitzero"`
	FunctionCall     responseField[responseFunction]                      `json:"function_call,omitzero"`
	Reasoning        responseField[string]                                `json:"reasoning,omitzero"`
	ReasoningContent responseField[string]                                `json:"reasoning_content,omitzero"`
	ReasoningDetails responseField[responseList[responseReasoningDetail]] `json:"reasoning_details,omitzero"`
	ThinkingBlocks   responseField[responseList[responseThinkingBlock]]   `json:"thinking_blocks,omitzero"`
	Annotations      responseField[responseList[responseAnnotation]]      `json:"annotations,omitzero"`
}
type responseToolCall struct {
	Index    responseField[responseCount]    `json:"index,omitzero"`
	ID       responseField[string]           `json:"id,omitzero"`
	Type     responseField[string]           `json:"type,omitzero"`
	Function responseField[responseFunction] `json:"function,omitzero"`
}
type responseFunction struct {
	Name      responseField[string] `json:"name,omitzero"`
	Arguments responseField[string] `json:"arguments,omitzero"`
}

// OpenRouter reasoning details retain opaque signatures/data for tool round trips.
// https://openrouter.ai/docs/guides/best-practices/reasoning-tokens
type responseReasoningDetail struct {
	Type      responseField[string]        `json:"type,omitzero"`
	ID        responseField[string]        `json:"id,omitzero"`
	Format    responseField[string]        `json:"format,omitzero"`
	Index     responseField[responseCount] `json:"index,omitzero"`
	Text      responseField[string]        `json:"text,omitzero"`
	Summary   responseField[string]        `json:"summary,omitzero"`
	Signature responseField[string]        `json:"signature,omitzero"`
	Data      responseField[string]        `json:"data,omitzero"`
}
type responseThinkingBlock struct {
	Type      responseField[string] `json:"type,omitzero"`
	Thinking  responseField[string] `json:"thinking,omitzero"`
	Signature responseField[string] `json:"signature,omitzero"`
	Data      responseField[string] `json:"data,omitzero"`
}
type responseAnnotation struct {
	Type        responseField[string]              `json:"type,omitzero"`
	URLCitation responseField[responseURLCitation] `json:"url_citation,omitzero"`
}
type responseURLCitation struct {
	StartIndex responseField[responseCount] `json:"start_index,omitzero"`
	EndIndex   responseField[responseCount] `json:"end_index,omitzero"`
	URL        responseField[string]        `json:"url,omitzero"`
	Title      responseField[string]        `json:"title,omitzero"`
}
type responseLogprobs struct {
	Content responseField[responseList[responseTokenLogprob]] `json:"content,omitzero"`
	Refusal responseField[responseList[responseTokenLogprob]] `json:"refusal,omitzero"`
}
type responseTokenLogprob struct {
	Token       responseField[string]                           `json:"token,omitzero"`
	Logprob     responseField[float64]                          `json:"logprob,omitzero"`
	Bytes       responseField[[]int]                            `json:"bytes,omitzero"`
	TopLogprobs responseField[responseList[responseTopLogprob]] `json:"top_logprobs,omitzero"`
}
type responseTopLogprob struct {
	Token   responseField[string]  `json:"token,omitzero"`
	Logprob responseField[float64] `json:"logprob,omitzero"`
	Bytes   responseField[[]int]   `json:"bytes,omitzero"`
}
type responseUsage struct {
	PromptTokens            responseField[responseCount]            `json:"prompt_tokens,omitzero"`
	CompletionTokens        responseField[responseCount]            `json:"completion_tokens,omitzero"`
	TotalTokens             responseField[responseCount]            `json:"total_tokens,omitzero"`
	PromptTokensDetails     responseField[responsePromptTokens]     `json:"prompt_tokens_details,omitzero"`
	CompletionTokensDetails responseField[responseCompletionTokens] `json:"completion_tokens_details,omitzero"`
}
type responsePromptTokens struct {
	CachedTokens responseField[responseCount] `json:"cached_tokens,omitzero"`
	AudioTokens  responseField[responseCount] `json:"audio_tokens,omitzero"`
}
type responseCompletionTokens struct {
	ReasoningTokens          responseField[responseCount] `json:"reasoning_tokens,omitzero"`
	AudioTokens              responseField[responseCount] `json:"audio_tokens,omitzero"`
	AcceptedPredictionTokens responseField[responseCount] `json:"accepted_prediction_tokens,omitzero"`
	RejectedPredictionTokens responseField[responseCount] `json:"rejected_prediction_tokens,omitzero"`
}

func projectResponse[C any](data []byte, request Request, object string) (completionResponse[C], error) {
	// Decode only provider content. Identity and timestamps belong to the gateway.
	var upstream struct {
		Choices responseField[[]C]           `json:"choices"`
		Usage   responseField[responseUsage] `json:"usage"`
	}
	var out completionResponse[C]
	if err := json.Unmarshal(data, &upstream); err != nil {
		return out, err
	}
	if upstream.Choices.Value == nil {
		return out, errors.New("missing choices")
	}
	out = completionResponse[C]{ID: "chatcmpl-" + request.ID, Object: object, Created: request.Created, Model: request.Model, Choices: *upstream.Choices.Value, Usage: upstream.Usage}
	return out, nil
}
func rebuildCompletion(data []byte, request Request) ([]byte, error) {
	out, err := projectResponse[completionChoice](data, request, "chat.completion")
	if err != nil {
		return nil, err
	}
	for i := range out.Choices {
		choice := &out.Choices[i]
		if choice.Index < 0 || choice.Message.Value == nil {
			return nil, errors.New("invalid completion choice")
		}
		message := choice.Message.Value
		if !message.Role.Present {
			message.Role = responseValue("assistant")
		}
		if message.Role.Value != nil && *message.Role.Value != "assistant" {
			return nil, errors.New("invalid response role")
		}
	}
	return json.Marshal(out)
}
func rebuildChunk(data []byte, request Request) ([]byte, bool, error) {
	out, err := projectResponse[streamChoice](data, request, "chat.completion.chunk")
	if err != nil {
		return nil, false, err
	}
	first := false
	for _, choice := range out.Choices {
		if choice.Index < 0 || choice.Delta.Value == nil {
			return nil, false, errors.New("invalid stream choice")
		}
		delta := choice.Delta.Value
		if delta.Role.Value != nil && *delta.Role.Value != "assistant" {
			return nil, false, errors.New("invalid response role")
		}
		first = first || delta.hasContent()
	}
	b, err := json.Marshal(out)
	return b, first, err
}

func (m responseMessage) hasContent() bool {
	for _, field := range []responseField[string]{m.Content, m.Refusal, m.Reasoning, m.ReasoningContent} {
		if field.Value != nil && *field.Value != "" {
			return true
		}
	}
	if m.ToolCalls.Value != nil && len(*m.ToolCalls.Value) > 0 {
		return true
	}
	if m.ReasoningDetails.Value != nil && len(*m.ReasoningDetails.Value) > 0 {
		return true
	}
	if m.ThinkingBlocks.Value != nil && len(*m.ThinkingBlocks.Value) > 0 {
		return true
	}
	return m.FunctionCall.Value != nil
}
