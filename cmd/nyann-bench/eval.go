package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/neuralmagic/nyann-bench/pkg/analysis"
	"github.com/neuralmagic/nyann-bench/pkg/config"
	"github.com/neuralmagic/nyann-bench/pkg/dataset"
	"github.com/spf13/cobra"
)

func evalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Run evaluation benchmarks",
		Long:  "Run standardized evaluation benchmarks against an LLM endpoint.",
	}
	cmd.AddCommand(evalGSM8KCmd())
	return cmd
}

func evalGSM8KCmd() *cobra.Command {
	var (
		target         string
		model          string
		concurrency    int
		gsm8kPath      string
		gsm8kTrainPath string
		numFewShot     int
		timeout        string
		outputDir      string
		metricsAddr    string
	)

	cmd := &cobra.Command{
		Use:   "gsm8k",
		Short: "Evaluate GSM8K math accuracy under load",
		Long: `Run the GSM8K evaluation benchmark against an LLM endpoint.

Sends all GSM8K test problems with few-shot prompting, evaluates
correctness of model responses, and reports accuracy alongside latency metrics.

Example:
  nyann-bench eval gsm8k --target http://localhost:8000/v1 --model llama-70b \
    --gsm8k-path data/gsm8k_test.jsonl --gsm8k-train-path data/gsm8k_train.jsonl

  nyann-bench eval gsm8k --target http://localhost:8000/v1 --model llama-70b \
    --gsm8k-path data/gsm8k_test.jsonl --num-fewshot 0 --concurrency 128`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			timeoutDur, err := time.ParseDuration(timeout)
			if err != nil {
				return fmt.Errorf("invalid timeout %q: %w", timeout, err)
			}

			if gsm8kPath == "" {
				return fmt.Errorf("--gsm8k-path is required (path to GSM8K test JSONL)")
			}
			if numFewShot > 0 && gsm8kTrainPath == "" {
				return fmt.Errorf("--gsm8k-train-path is required when --num-fewshot > 0")
			}

			// Load dataset to get item count
			gsm8kDS, err := dataset.NewGSM8K(gsm8kPath, gsm8kTrainPath, numFewShot)
			if err != nil {
				return fmt.Errorf("loading GSM8K dataset: %w", err)
			}
			itemCount := gsm8kDS.Len()

			slog.Info("GSM8K eval configured",
				"items", itemCount,
				"concurrency", concurrency,
				"timeout", timeout,
				"num_fewshot", numFewShot)

			sc := &config.ScenarioConfig{
				Target: target,
				Model:  model,
				Workload: config.Workload{
					Type:           "gsm8k",
					GSM8KPath:      gsm8kPath,
					GSM8KTrainPath: gsm8kTrainPath,
					NumFewShot:     &numFewShot,
				},
				Stages: []config.ScenarioStage{{
					Name:        "gsm8k-eval",
					Duration:    timeoutDur,
					Mode:        "concurrent",
					Concurrency: concurrency,
					MaxRequests: itemCount,
				}},
			}

			summary, err := runScenario(ctx, cancel, scenarioOpts{
				Target:      target,
				Model:       model,
				Scenario:    sc,
				OutputDir:   outputDir,
				MetricsAddr: metricsAddr,
			})
			if err != nil {
				return err
			}

			if summary.TotalRequests > 0 {
				fmt.Fprint(os.Stderr, "\n")
				fmt.Fprint(os.Stderr, analysis.FormatSummary(summary))

				jsonOut, err := json.MarshalIndent(summary, "", "  ")
				if err != nil {
					return fmt.Errorf("marshalling summary: %w", err)
				}
				fmt.Println(string(jsonOut))
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&target, "target", "", "Target endpoint base URL (required)")
	cmd.Flags().StringVar(&model, "model", "", "Model name (auto-detected if omitted)")
	cmd.Flags().IntVar(&concurrency, "concurrency", 64, "Number of concurrent streams")
	cmd.Flags().StringVar(&gsm8kPath, "gsm8k-path", "", "Path to GSM8K test JSONL file (required)")
	cmd.Flags().StringVar(&gsm8kTrainPath, "gsm8k-train-path", "", "Path to GSM8K train JSONL (for few-shot examples)")
	cmd.Flags().IntVar(&numFewShot, "num-fewshot", 5, "Number of few-shot examples (0 for zero-shot)")
	cmd.Flags().StringVar(&timeout, "timeout", "30m", "Hard time cap for the evaluation")
	cmd.Flags().StringVar(&outputDir, "output-dir", "", "Directory for JSONL + timestamp output files")
	cmd.Flags().StringVar(&metricsAddr, "metrics", "", "Prometheus metrics listen address (e.g. :9090)")

	cmd.MarkFlagRequired("target")
	cmd.MarkFlagRequired("gsm8k-path")

	return cmd
}
