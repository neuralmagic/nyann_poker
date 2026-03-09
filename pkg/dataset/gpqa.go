package dataset

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
)

// GPQA generates chain-of-thought completions from GPQA multiple-choice questions.
// Uses the completions API with CoT prompting to match lm_eval's gpqa_cot_zeroshot task.
type GPQA struct {
	items []gpqaItem
	idx   atomic.Uint64
}

type gpqaRawItem struct {
	Question         string `json:"Question"`
	CorrectAnswer    string `json:"Correct Answer"`
	IncorrectAnswer1 string `json:"Incorrect Answer 1"`
	IncorrectAnswer2 string `json:"Incorrect Answer 2"`
	IncorrectAnswer3 string `json:"Incorrect Answer 3"`
}

type gpqaItem struct {
	Question string
	Choices  [4]string // shuffled
	Answer   string    // "(A)", "(B)", "(C)", or "(D)"
}

// NewGPQA creates a GPQA dataset from a JSONL or JSON file.
func NewGPQA(path string) (*GPQA, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading gpqa file %s: %w", path, err)
	}

	rawItems, err := parseGPQA(data)
	if err != nil {
		return nil, fmt.Errorf("parsing gpqa file %s: %w", path, err)
	}

	if len(rawItems) == 0 {
		return nil, fmt.Errorf("no questions found in %s", path)
	}

	// Shuffle choices and record correct answer letter
	items := make([]gpqaItem, len(rawItems))
	for i, raw := range rawItems {
		choices := [4]string{
			preprocess(raw.CorrectAnswer),
			preprocess(raw.IncorrectAnswer1),
			preprocess(raw.IncorrectAnswer2),
			preprocess(raw.IncorrectAnswer3),
		}
		// Shuffle with a per-item seed for reproducibility
		r := rand.New(rand.NewSource(int64(i)))
		perm := r.Perm(4)
		var shuffled [4]string
		correctIdx := 0
		for j, p := range perm {
			shuffled[j] = choices[p]
			if p == 0 { // index 0 is the correct answer
				correctIdx = j
			}
		}
		items[i] = gpqaItem{
			Question: preprocess(raw.Question),
			Choices:  shuffled,
			Answer:   fmt.Sprintf("(%c)", 'A'+correctIdx),
		}
	}

	return &GPQA{items: items}, nil
}

func (g *GPQA) NextConversation() Conversation {
	idx := g.idx.Add(1) - 1
	item := g.items[idx%uint64(len(g.items))]

	var b strings.Builder
	fmt.Fprintf(&b, "What is the correct answer to this question: %s\n", item.Question)
	b.WriteString("Choices:\n")
	for i, choice := range item.Choices {
		fmt.Fprintf(&b, "(%c) %s\n", 'A'+i, choice)
	}
	b.WriteString("Let's think step by step: ")

	greedy := 0.0
	return Conversation{
		Prompt:         b.String(),
		MaxTokens:      1024,
		Stop:           []string{"</s>", "<|im_end|>"},
		Temperature:    &greedy,
		ExpectedAnswer: item.Answer,
	}
}

// preprocess cleans up GPQA text, matching lm_eval's preprocessing.
var bracketRe = regexp.MustCompile(`\[.*?\]`)

func preprocess(text string) string {
	text = strings.TrimSpace(text)
	text = strings.ReplaceAll(text, " [title]", ". ")
	text = bracketRe.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "  ", " ")
	return text
}

// parseGPQA parses GPQA data as JSONL or JSON array.
func parseGPQA(data []byte) ([]gpqaRawItem, error) {
	var items []gpqaRawItem
	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var item gpqaRawItem
		if err := dec.Decode(&item); err != nil {
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

	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	return items, nil
}
