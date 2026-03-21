package client

import (
	"bytes"
	"encoding/json"
)

// chunkFields holds the fields extracted from a single SSE chunk
// without allocating the full parsed JSON structure.
type chunkFields struct {
	Content      string
	HasContent   bool
	FinishReason string
	Usage        *Usage
}

// parseSSEChunk extracts only the fields we need from an SSE data line
// using byte scanning instead of json.Unmarshal.
// contentKey is "content" for chat completions or "text" for completions.
func parseSSEChunk(data []byte, contentKey string) chunkFields {
	var f chunkFields

	if s, ok := extractString(data, contentKey); ok && s != "" {
		f.Content = s
		f.HasContent = true
	}

	f.FinishReason = extractFinishReason(data)

	if idx := bytes.Index(data, []byte(`"usage":{`)); idx >= 0 {
		f.Usage = extractUsage(data[idx+len(`"usage":`):]	)
	}

	return f
}

// extractString finds "key":"value" in JSON bytes and returns the value.
// Handles escaped characters in the value.
func extractString(data []byte, key string) (string, bool) {
	pattern := []byte(`"` + key + `":"`)
	idx := bytes.Index(data, pattern)
	if idx < 0 {
		return "", false
	}
	start := idx + len(pattern)

	// Find closing quote, handling backslash escapes
	hasEscape := false
	for i := start; i < len(data); i++ {
		if data[i] == '\\' {
			hasEscape = true
			i++ // skip next char
			continue
		}
		if data[i] == '"' {
			if !hasEscape {
				return string(data[start:i]), true
			}
			// Slow path: let json.Unmarshal handle the escapes
			var s string
			raw := make([]byte, 0, i-start+2)
			raw = append(raw, '"')
			raw = append(raw, data[start:i]...)
			raw = append(raw, '"')
			json.Unmarshal(raw, &s)
			return s, true
		}
	}
	return "", false
}

// extractFinishReason extracts finish_reason from JSON bytes.
// Returns "" for null or missing.
func extractFinishReason(data []byte) string {
	pattern := []byte(`"finish_reason":"`)
	idx := bytes.Index(data, pattern)
	if idx < 0 {
		return ""
	}
	start := idx + len(pattern)
	end := bytes.IndexByte(data[start:], '"')
	if end < 0 {
		return ""
	}
	return string(data[start : start+end])
}

// extractUsage parses a usage JSON object starting at the opening brace.
func extractUsage(data []byte) *Usage {
	if len(data) == 0 || data[0] != '{' {
		return nil
	}
	// Find matching closing brace
	depth := 0
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				var u Usage
				if json.Unmarshal(data[:i+1], &u) == nil {
					return &u
				}
				return nil
			}
		}
	}
	return nil
}
