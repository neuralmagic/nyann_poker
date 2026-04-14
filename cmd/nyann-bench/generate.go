package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/neuralmagic/nyann-bench/pkg/analysis"
	"github.com/neuralmagic/nyann-bench/pkg/client"
	"github.com/neuralmagic/nyann-bench/pkg/config"
	"github.com/neuralmagic/nyann-bench/pkg/loadgen"
	"github.com/neuralmagic/nyann-bench/pkg/metrics"
	"github.com/neuralmagic/nyann-bench/pkg/recorder"
	"github.com/spf13/cobra"
)

func generateCmd() *cobra.Command {
	var (
		target      string
		model       string
		cfgInput    string
		outputDir   string
		workerID    int
		metricsAddr string
	)

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate load against an LLM inference endpoint",
		Long: `Generate load against an LLM inference endpoint.

Configure the workload via --config (JSON file, inline JSON, or Starlark .star file):

  nyann-bench generate --target http://localhost:8000/v1 --model my-model \
    --config '{"load":{"mode":"concurrent","concurrency":10,"duration":"60s"},"workload":{"type":"faker","isl":128,"osl":256}}'

  nyann-bench generate --target http://localhost:8000/v1 --config benchmark.json

  nyann-bench generate --config scenario.star

Starlark (.star) files provide full programmability — loops, functions,
conditionals, and per-stage workload/target overrides:

  scenario(
      stages = [stage("2m", concurrency=c) for c in range(10, 101, 10)],
      workload = workload("faker", isl=512, osl=1024),
  )

Load modes:
  concurrent  Fixed number of streams, each fires next request on completion (default)
  constant    Requests arrive at a fixed rate (evenly spaced)
  poisson     Requests arrive at a target rate with exponential inter-arrival times

Workload types:
  synthetic   Random word padding
  faker       Diverse generated prose (gofakeit)
  corpus      Sliding window over real text files
  gsm8k       GSM8K math problems with streaming eval`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			// Auto-detect worker ID from K8s indexed Job
			if workerID == 0 {
				if idx, ok := os.LookupEnv("JOB_COMPLETION_INDEX"); ok {
					if v, err := strconv.Atoi(idx); err == nil {
						workerID = v
					}
				}
			}

			// Parse config
			sc, err := config.Parse(cfgInput)
			if err != nil {
				return fmt.Errorf("config: %w", err)
			}

			// CLI flags override config-level target/model
			if sc.Target != "" && target == "http://localhost:8000/v1" {
				target = sc.Target
			}
			if sc.Model != "" && model == "" {
				model = sc.Model
			}

			// Wait for endpoint to be ready
			c := client.New(target)
			slog.Info("Waiting for endpoint to be ready", "target", target)
			if err := c.WaitForReady(ctx); err != nil {
				return err
			}
			slog.Info("Endpoint ready")

			// Auto-detect model if not specified
			if model == "" {
				detected, err := c.DetectModel(ctx)
				if err != nil {
					return fmt.Errorf("auto-detecting model (use --model to specify): %w", err)
				}
				model = detected
				slog.Info("Detected model", "model", model)
			}

			w := sc.Workload
			if w.CacheSalt != nil {
				slog.Info("Cache salt enabled", "mode", w.CacheSalt.Mode)
			}
			if w.SubsequentISL != nil {
				slog.Info("Subsequent ISL configured", "isl", w.ISL, "subsequent_isl", *w.SubsequentISL)
			}

			charsPerToken := calibrateTokenRatio(ctx, c, model, w.CharsPerToken)

			ds, err := buildDataset(&w, charsPerToken)
			if err != nil {
				return err
			}

			// Build recorder
			var rec *recorder.Recorder
			if outputDir != "" {
				rec, err = recorder.New(outputDir, workerID)
				if err != nil {
					return fmt.Errorf("creating recorder: %w", err)
				}
			} else {
				rec = recorder.NewMemory()
			}

			// Start Prometheus metrics server
			var m *metrics.Metrics
			if metricsAddr != "" {
				reg := prometheus.NewRegistry()
				workloadName := w.Name
				if workloadName == "" {
					workloadName = w.Type
				}
				m = metrics.New(reg, workloadName, w.Type == "gsm8k")
				mux := http.NewServeMux()
				mux.Handle("/metrics", metrics.Handler(reg))
				srv := &http.Server{Addr: metricsAddr, Handler: mux}
				go func() {
					slog.Info("Metrics server listening", "addr", metricsAddr)
					if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
						slog.Error("Metrics server error", "error", err)
					}
				}()
			}

			// Build loadgen stages, resolving per-stage overrides.
			// Track the effective target/workload for each stage so we can
			// detect when we need to rebuild the dataset or client.
			type resolvedStage struct {
				loadgen  loadgen.Stage
				target   string
				model    string
				workload *config.Workload
				warmup   bool
				name     string
			}

			var resolved []resolvedStage
			for _, ss := range sc.Stages {
				effectiveTarget := target
				if ss.Target != "" {
					effectiveTarget = ss.Target
				}
				effectiveModel := model
				if ss.Model != "" {
					effectiveModel = ss.Model
				}
				var effectiveWorkload *config.Workload
				if ss.Workload != nil {
					effectiveWorkload = ss.Workload
				}

				mode := ss.Mode
				if mode == "" {
					mode = "concurrent"
				}

				resolved = append(resolved, resolvedStage{
					loadgen: loadgen.Stage{
						Concurrency: ss.Concurrency,
						Duration:    ss.Duration,
						Rampup:      ss.Rampup,
					},
					target:   effectiveTarget,
					model:    effectiveModel,
					workload: effectiveWorkload,
					warmup:   ss.Warmup,
					name:     ss.Name,
				})
			}

			// Group consecutive stages that share the same target/workload
			// into runs that can share a single generator and stream pool.
			type stageRun struct {
				stages   []loadgen.Stage
				target   string
				model    string
				workload *config.Workload
				warmups  []bool
				names    []string
			}

			var runs []stageRun
			for _, rs := range resolved {
				// Check if we can extend the current run
				canExtend := len(runs) > 0 &&
					runs[len(runs)-1].target == rs.target &&
					runs[len(runs)-1].model == rs.model &&
					workloadEqual(runs[len(runs)-1].workload, rs.workload)

				if canExtend {
					runs[len(runs)-1].stages = append(runs[len(runs)-1].stages, rs.loadgen)
					runs[len(runs)-1].warmups = append(runs[len(runs)-1].warmups, rs.warmup)
					runs[len(runs)-1].names = append(runs[len(runs)-1].names, rs.name)
				} else {
					runs = append(runs, stageRun{
						stages:   []loadgen.Stage{rs.loadgen},
						target:   rs.target,
						model:    rs.model,
						workload: rs.workload,
						warmups:  []bool{rs.warmup},
						names:    []string{rs.name},
					})
				}
			}

			// Count total measured (non-warmup) stages for logging
			totalMeasuredStages := 0
			for _, rs := range resolved {
				if !rs.warmup {
					totalMeasuredStages++
				}
			}

			warmupRec := recorder.NewMemory()
			var startTime time.Time
			var stageTimestamps []recorder.StageTimestamp
			globalStageIdx := 0
			measuredStageIdx := 0
			var lastStageStart time.Time
			var lastConcurrency int

			for _, run := range runs {
				if ctx.Err() != nil {
					break
				}

				// Determine the dataset and target for this run
				runTarget := run.target
				runModel := run.model
				runWorkload := &w
				if run.workload != nil {
					runWorkload = run.workload
				}

				// Build dataset for this run's workload if it differs
				runDS := ds
				if run.workload != nil {
					runCharsPerToken := charsPerToken
					if runWorkload.CharsPerToken > 0 {
						runCharsPerToken = runWorkload.CharsPerToken
					} else if runTarget != target {
						// Re-calibrate if hitting a different endpoint
						runC := client.New(runTarget)
						runCharsPerToken = calibrateTokenRatio(ctx, runC, runModel, runWorkload.CharsPerToken)
					}
					var err error
					runDS, err = buildDataset(runWorkload, runCharsPerToken)
					if err != nil {
						return err
					}
				}

				gen := &loadgen.Generator{
					Target:      runTarget,
					Model:       runModel,
					Mode:        loadgen.Mode(sc.Stages[globalStageIdx].Mode),
					Rate:        sc.Stages[globalStageIdx].Rate,
					MaxInFlight: sc.Stages[globalStageIdx].MaxInFlight,
					CacheSalt:   runWorkload.CacheSalt,
					Dataset:     runDS,
					Recorder:    rec,
					Metrics:     m,
				}

				gen.RunStages(ctx, run.stages, func(i, concurrency int) {
					isWarmup := run.warmups[i]
					stageName := run.names[i]

					if isWarmup {
						gen.SetRecorder(warmupRec)
						if stageName != "" {
							slog.Info("Warmup running", "name", stageName, "concurrency", concurrency)
						} else {
							slog.Info("Warmup running", "concurrency", concurrency)
						}
						return
					}

					gen.SetRecorder(rec)
					now := time.Now()

					if startTime.IsZero() {
						startTime = now
					} else if !lastStageStart.IsZero() {
						// Close out the previous measured stage
						stageTimestamps = append(stageTimestamps, recorder.StageTimestamp{
							Stage:       measuredStageIdx - 1,
							Concurrency: lastConcurrency,
							StartTime:   recorder.TimeToFloat(lastStageStart),
							EndTime:     recorder.TimeToFloat(now),
						})
					}

					lastStageStart = now
					lastConcurrency = concurrency
					measuredStageIdx++

					if stageName != "" {
						slog.Info("Stage started",
							"name", stageName,
							"stage", fmt.Sprintf("%d/%d", measuredStageIdx, totalMeasuredStages),
							"concurrency", concurrency,
							"duration", run.stages[i].Duration)
					} else {
						slog.Info("Stage started",
							"stage", fmt.Sprintf("%d/%d", measuredStageIdx, totalMeasuredStages),
							"concurrency", concurrency,
							"duration", run.stages[i].Duration)
					}
					if m != nil {
						m.Stage.Set(float64(measuredStageIdx - 1))
					}
				})

				globalStageIdx += len(run.stages)
			}

			endTime := time.Now()

			// Close out the final measured stage
			if !lastStageStart.IsZero() {
				stageTimestamps = append(stageTimestamps, recorder.StageTimestamp{
					Stage:       measuredStageIdx - 1,
					Concurrency: lastConcurrency,
					StartTime:   recorder.TimeToFloat(lastStageStart),
					EndTime:     recorder.TimeToFloat(endTime),
				})
			}

			if startTime.IsZero() {
				startTime = endTime
			}
			timestamps := &recorder.Timestamps{
				StartTime:     recorder.TimeToFloat(startTime),
				RampupEndTime: recorder.TimeToFloat(startTime),
				EndTime:       recorder.TimeToFloat(endTime),
				RampupSeconds: 0,
				TotalSeconds:  endTime.Sub(startTime).Seconds(),
				Stages:        stageTimestamps,
			}

			// Write files to disk if output-dir is set
			if outputDir != "" {
				tsPath := fmt.Sprintf("%s/timestamps_%d.json", outputDir, workerID)
				if err := timestamps.Write(tsPath); err != nil {
					return fmt.Errorf("writing timestamps: %w", err)
				}
			}

			// Compute and print summary
			rec.Close()
			records := rec.Records()
			if len(records) > 0 {
				summary := analysis.Compute(records, 0, 0)
				summary.Timestamps = timestamps

				// Human-readable to stderr
				fmt.Fprint(os.Stderr, "\n")
				fmt.Fprint(os.Stderr, analysis.FormatSummary(summary))

				// Machine-readable JSON to stdout
				jsonOut, err := json.MarshalIndent(summary, "", "  ")
				if err != nil {
					return fmt.Errorf("marshalling summary: %w", err)
				}
				fmt.Println(string(jsonOut))
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&target, "target", "http://localhost:8000/v1", "Target endpoint base URL")
	cmd.Flags().StringVar(&model, "model", "", "Model name for requests")
	cmd.Flags().StringVar(&cfgInput, "config", "{}", "Workload config (JSON file, inline JSON, or .star file)")
	cmd.Flags().StringVar(&outputDir, "output-dir", "", "Directory for JSONL + timestamp files (omit for stdout-only)")
	cmd.Flags().IntVar(&workerID, "worker-id", 0, "Worker identifier (for multi-container runs)")
	cmd.Flags().StringVar(&metricsAddr, "metrics", "", "Prometheus metrics listen address (e.g. :9090)")

	return cmd
}

// workloadEqual checks if two workload pointers refer to the same workload config.
// Both nil means "inherit from scenario" — they match. Both non-nil must match on type and name.
func workloadEqual(a, b *config.Workload) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Type == b.Type && a.Name == b.Name &&
		a.ISL == b.ISL && a.OSL == b.OSL && a.Turns == b.Turns &&
		a.CorpusPath == b.CorpusPath && a.GSM8KPath == b.GSM8KPath
}
