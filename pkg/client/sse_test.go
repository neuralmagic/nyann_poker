package client

import (
	"encoding/json"
	"testing"
)

func TestParseSSEChunkChatContent(t *testing.T) {
	data := []byte(`{"id":"chatcmpl-123","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hello world"}}]}`)
	f := parseSSEChunk(data, "content")
	if !f.HasContent || f.Content != "hello world" {
		t.Errorf("got content=%q has=%v, want 'hello world' true", f.Content, f.HasContent)
	}
	if f.FinishReason != "" {
		t.Errorf("got finish_reason=%q, want empty", f.FinishReason)
	}
	if f.Usage != nil {
		t.Error("expected nil usage")
	}
}

func TestParseSSEChunkCompletionText(t *testing.T) {
	data := []byte(`{"id":"cmpl-123","choices":[{"index":0,"text":"token here"}]}`)
	f := parseSSEChunk(data, "text")
	if !f.HasContent || f.Content != "token here" {
		t.Errorf("got content=%q has=%v, want 'token here' true", f.Content, f.HasContent)
	}
}

func TestParseSSEChunkEmptyContent(t *testing.T) {
	data := []byte(`{"id":"chatcmpl-123","choices":[{"index":0,"delta":{"content":""}}]}`)
	f := parseSSEChunk(data, "content")
	if f.HasContent {
		t.Error("expected HasContent=false for empty content")
	}
}

func TestParseSSEChunkNoContent(t *testing.T) {
	data := []byte(`{"id":"chatcmpl-123","choices":[{"index":0,"delta":{}}]}`)
	f := parseSSEChunk(data, "content")
	if f.HasContent {
		t.Error("expected HasContent=false when content field missing")
	}
}

func TestParseSSEChunkFinishReason(t *testing.T) {
	data := []byte(`{"id":"chatcmpl-123","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	f := parseSSEChunk(data, "content")
	if f.FinishReason != "stop" {
		t.Errorf("got finish_reason=%q, want 'stop'", f.FinishReason)
	}
}

func TestParseSSEChunkFinishReasonNull(t *testing.T) {
	data := []byte(`{"id":"chatcmpl-123","choices":[{"index":0,"delta":{"content":"tok "},"finish_reason":null}]}`)
	f := parseSSEChunk(data, "content")
	if f.FinishReason != "" {
		t.Errorf("got finish_reason=%q, want empty for null", f.FinishReason)
	}
}

func TestParseSSEChunkUsage(t *testing.T) {
	data := []byte(`{"id":"chatcmpl-123","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	f := parseSSEChunk(data, "content")
	if f.Usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if f.Usage.PromptTokens != 10 {
		t.Errorf("got prompt_tokens=%d, want 10", f.Usage.PromptTokens)
	}
	if f.Usage.CompletionTokens != 5 {
		t.Errorf("got completion_tokens=%d, want 5", f.Usage.CompletionTokens)
	}
	if f.Usage.TotalTokens != 15 {
		t.Errorf("got total_tokens=%d, want 15", f.Usage.TotalTokens)
	}
}

func TestParseSSEChunkEscapedContent(t *testing.T) {
	data := []byte(`{"choices":[{"delta":{"content":"hello \"world\"\n"}}]}`)
	f := parseSSEChunk(data, "content")
	if !f.HasContent {
		t.Fatal("expected HasContent=true")
	}
	if f.Content != "hello \"world\"\n" {
		t.Errorf("got content=%q, want %q", f.Content, "hello \"world\"\n")
	}
}

func TestParseSSEChunkFinishReasonLength(t *testing.T) {
	data := []byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`)
	f := parseSSEChunk(data, "content")
	if f.FinishReason != "length" {
		t.Errorf("got finish_reason=%q, want 'length'", f.FinishReason)
	}
}

func BenchmarkParseSSEChunkScanner(b *testing.B) {
	data := []byte(`{"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"tok "},"finish_reason":null}]}`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		parseSSEChunk(data, "content")
	}
}

func BenchmarkParseSSEChunkJSON(b *testing.B) {
	data := []byte(`{"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"tok "},"finish_reason":null}]}`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *Usage `json:"usage"`
		}
		_ = json.Unmarshal(data, &chunk)
	}
}
