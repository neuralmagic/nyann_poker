package dataset

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/neuralmagic/nyann_poker/pkg/client"
	"github.com/neuralmagic/nyann_poker/pkg/eval"
)

// GSM8K generates single-turn conversations from GSM8K math problems.
// Each conversation sends a question and expects a numerical answer.
type GSM8K struct {
	MaxTokens int
	items     []gsm8kItem
	idx       atomic.Uint64
}

type gsm8kItem struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

const gsm8kSystemPrompt = "Solve the following math problem step by step. End your response with the answer on a new line in the format: #### <number>"

func NewGSM8K(path string, maxTokens int) (*GSM8K, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading gsm8k file %s: %w", path, err)
	}

	items, err := parseGSM8K(data)
	if err != nil {
		return nil, fmt.Errorf("parsing gsm8k file %s: %w", path, err)
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("no questions found in %s", path)
	}

	return &GSM8K{MaxTokens: maxTokens, items: items}, nil
}

func (g *GSM8K) NextConversation() Conversation {
	idx := g.idx.Add(1) - 1
	item := g.items[idx%uint64(len(g.items))]

	messages := []client.Message{
		{Role: "system", Content: gsm8kSystemPrompt},
		{Role: "user", Content: item.Question},
	}

	return Conversation{
		Turns:          [][]client.Message{messages},
		MaxTokens:      g.MaxTokens,
		ExpectedAnswer: eval.ExtractExpected(item.Answer),
	}
}

// parseGSM8K parses GSM8K data as JSONL (one object per line) or JSON array.
func parseGSM8K(data []byte) ([]gsm8kItem, error) {
	// Try JSONL first
	var items []gsm8kItem
	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var item gsm8kItem
		if err := dec.Decode(&item); err != nil {
			// Not valid JSONL, try array
			items = nil
			break
		}
		if item.Question != "" {
			items = append(items, item)
		}
	}
	if len(items) > 0 {
		return items, nil
	}

	// Try JSON array
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	return items, nil
}
