package loadgen

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/neuralmagic/nyann_poker/pkg/client"
	"github.com/neuralmagic/nyann_poker/pkg/dataset"
	"github.com/neuralmagic/nyann_poker/pkg/recorder"
)

type Generator struct {
	Target      string
	Model       string
	Concurrency int
	Rampup      time.Duration
	Duration    time.Duration
	Dataset     dataset.Dataset
	Recorder    *recorder.Recorder
	ThinkTime   time.Duration
}

func (g *Generator) Run(ctx context.Context) (*recorder.Timestamps, error) {
	c := client.New(g.Target)

	// Create a context that cancels after the total duration
	ctx, cancel := context.WithTimeout(ctx, g.Duration)
	defer cancel()

	startTime := time.Now()
	rampupEnd := startTime.Add(g.Rampup)

	var wg sync.WaitGroup
	for i := 0; i < g.Concurrency; i++ {
		wg.Add(1)
		delay := time.Duration(0)
		if g.Rampup > 0 && g.Concurrency > 1 {
			delay = g.Rampup * time.Duration(i) / time.Duration(g.Concurrency-1)
		}
		go func(streamID int, delay time.Duration) {
			defer wg.Done()
			g.runStream(ctx, c, streamID, delay)
		}(i, delay)
	}

	wg.Wait()
	endTime := time.Now()

	ts := &recorder.Timestamps{
		StartTime:     recorder.TimeToFloat(startTime),
		RampupEndTime: recorder.TimeToFloat(rampupEnd),
		EndTime:       recorder.TimeToFloat(endTime),
		RampupSeconds: g.Rampup.Seconds(),
		TotalSeconds:  endTime.Sub(startTime).Seconds(),
	}
	return ts, nil
}

func (g *Generator) runStream(ctx context.Context, c *client.Client, streamID int, delay time.Duration) {
	// Wait for rampup delay
	if delay > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}

	convID := 0
	for {
		if ctx.Err() != nil {
			return
		}

		conversation := g.Dataset.NextConversation()
		conversationID := fmt.Sprintf("w%d-c%d", streamID, convID)
		convID++

		g.runConversation(ctx, c, streamID, conversationID, conversation)
	}
}

func (g *Generator) runConversation(ctx context.Context, c *client.Client, streamID int, convID string, turns [][]client.Message) {
	for turnIdx, messages := range turns {
		if ctx.Err() != nil {
			return
		}

		// Think time between turns (not before the first turn)
		if turnIdx > 0 && g.ThinkTime > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(g.ThinkTime):
			}
		}

		req := &client.Request{
			Model:    g.Model,
			Messages: messages,
			Stream:   true,
		}

		result := c.ChatStream(ctx, req)

		rec := &recorder.Record{
			RequestID:      fmt.Sprintf("%s-t%d", convID, turnIdx),
			StreamID:       streamID,
			ConversationID: convID,
			Turn:           turnIdx,
			StartTime:      recorder.TimeToFloat(result.RequestStart),
			EndTime:        recorder.TimeToFloat(result.EndTime),
			TotalLatencyMs: result.TotalLatency().Seconds() * 1000,
			OutputTokens:   result.OutputTokens(),
		}

		if result.Err != nil {
			rec.Status = "error"
			rec.Error = result.Err.Error()
			// Don't continue the conversation on error
			if err := g.Recorder.Write(rec); err != nil {
				fmt.Fprintf(os.Stderr, "recorder write error: %v\n", err)
			}
			return
		}

		rec.Status = "ok"
		rec.TTFT = result.TTFT().Seconds() * 1000

		itls := result.ITLs()
		rec.ITLs = make([]float64, len(itls))
		for i, d := range itls {
			rec.ITLs[i] = d.Seconds() * 1000
		}

		if result.Usage != nil {
			rec.PromptTokens = result.Usage.PromptTokens
			rec.OutputTokens = result.Usage.CompletionTokens
		}

		if err := g.Recorder.Write(rec); err != nil {
			fmt.Fprintf(os.Stderr, "recorder write error: %v\n", err)
		}
	}
}
