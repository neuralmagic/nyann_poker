package dataset_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/neuralmagic/nyann_poker/pkg/dataset"
)

func TestGSM8KFromJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gsm8k.jsonl")

	data := `{"question":"If there are 3 cars and each has 4 wheels, how many wheels total?","answer":"3 cars * 4 wheels = 12 wheels\n#### 12"}
{"question":"A baker made 24 cookies and ate 3. How many are left?","answer":"24 - 3 = 21\n#### 21"}
`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	ds, err := dataset.NewGSM8K(path, 512)
	if err != nil {
		t.Fatal(err)
	}

	conv := ds.NextConversation()
	if len(conv.Turns) != 1 {
		t.Fatalf("expected 1 turn, got %d", len(conv.Turns))
	}
	if conv.MaxTokens != 512 {
		t.Errorf("expected MaxTokens=512, got %d", conv.MaxTokens)
	}
	if conv.ExpectedAnswer != "12" {
		t.Errorf("expected answer '12', got %q", conv.ExpectedAnswer)
	}
	// Should have system + user messages
	if len(conv.Turns[0]) != 2 {
		t.Errorf("expected 2 messages (system+user), got %d", len(conv.Turns[0]))
	}
	if conv.Turns[0][0].Role != "system" {
		t.Errorf("expected system message first, got %s", conv.Turns[0][0].Role)
	}
	if conv.Turns[0][1].Content != "If there are 3 cars and each has 4 wheels, how many wheels total?" {
		t.Errorf("unexpected question content: %s", conv.Turns[0][1].Content)
	}
}

func TestGSM8KFromJSONArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gsm8k.json")

	data := `[{"question":"What is 2+2?","answer":"#### 4"},{"question":"What is 3*5?","answer":"#### 15"}]`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	ds, err := dataset.NewGSM8K(path, 256)
	if err != nil {
		t.Fatal(err)
	}

	c1 := ds.NextConversation()
	c2 := ds.NextConversation()

	if c1.ExpectedAnswer != "4" {
		t.Errorf("expected '4', got %q", c1.ExpectedAnswer)
	}
	if c2.ExpectedAnswer != "15" {
		t.Errorf("expected '15', got %q", c2.ExpectedAnswer)
	}
}

func TestGSM8KWrapsAround(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gsm8k.jsonl")

	data := `{"question":"Q1","answer":"#### 1"}
{"question":"Q2","answer":"#### 2"}
`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	ds, err := dataset.NewGSM8K(path, 256)
	if err != nil {
		t.Fatal(err)
	}

	// Consume both items, then should wrap around
	ds.NextConversation()
	ds.NextConversation()
	c3 := ds.NextConversation()

	if c3.ExpectedAnswer != "1" {
		t.Errorf("expected wrap-around to '1', got %q", c3.ExpectedAnswer)
	}
}

func TestGSM8KEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := dataset.NewGSM8K(path, 256)
	if err == nil {
		t.Error("expected error for empty file")
	}
}
