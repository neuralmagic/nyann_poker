package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/neuralmagic/nyann_poker/pkg/config"
)

func TestParseInline(t *testing.T) {
	cfg, err := config.Parse(`{
		"load": {
			"mode": "concurrent",
			"concurrency": 100,
			"rampup": "30s",
			"duration": "5m"
		},
		"workload": {
			"type": "faker",
			"isl": 512,
			"osl": 1024,
			"turns": 3
		}
	}`)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Load.Mode != "concurrent" {
		t.Errorf("expected concurrent, got %s", cfg.Load.Mode)
	}
	if cfg.Load.Concurrency != 100 {
		t.Errorf("expected 100, got %d", cfg.Load.Concurrency)
	}
	if cfg.Load.Rampup.Duration() != 30*time.Second {
		t.Errorf("expected 30s rampup, got %v", cfg.Load.Rampup.Duration())
	}
	if cfg.Load.Duration.Duration() != 5*time.Minute {
		t.Errorf("expected 5m duration, got %v", cfg.Load.Duration.Duration())
	}
	if cfg.Workload.ISL != 512 {
		t.Errorf("expected ISL 512, got %d", cfg.Workload.ISL)
	}
	if cfg.Workload.Turns != 3 {
		t.Errorf("expected 3 turns, got %d", cfg.Workload.Turns)
	}
}

func TestParseFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	err := os.WriteFile(path, []byte(`{
		"load": {"mode": "poisson", "rate": 50, "duration": "2m"},
		"workload": {"type": "synthetic", "isl": 64, "osl": 128}
	}`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Parse(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Load.Mode != "poisson" {
		t.Errorf("expected poisson, got %s", cfg.Load.Mode)
	}
	if cfg.Load.Rate != 50 {
		t.Errorf("expected rate 50, got %f", cfg.Load.Rate)
	}
}

func TestParseDefaults(t *testing.T) {
	cfg, err := config.Parse(`{}`)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Load.Mode != "concurrent" {
		t.Errorf("expected default mode concurrent, got %s", cfg.Load.Mode)
	}
	if cfg.Load.Concurrency != 10 {
		t.Errorf("expected default concurrency 10, got %d", cfg.Load.Concurrency)
	}
	if cfg.Load.Duration.Duration() != 60*time.Second {
		t.Errorf("expected default duration 60s, got %v", cfg.Load.Duration.Duration())
	}
	if cfg.Workload.Type != "faker" {
		t.Errorf("expected default type faker, got %s", cfg.Workload.Type)
	}
	if cfg.Workload.ISL != 128 {
		t.Errorf("expected default ISL 128, got %d", cfg.Workload.ISL)
	}
}

func TestDurationNumeric(t *testing.T) {
	cfg, err := config.Parse(`{"load": {"duration": 120}}`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Load.Duration.Duration() != 120*time.Second {
		t.Errorf("expected 120s, got %v", cfg.Load.Duration.Duration())
	}
}

func TestBurstStages(t *testing.T) {
	cfg, err := config.Parse(`{
		"burst": {
			"concurrency": 64,
			"burst_duration": "5s",
			"pause_duration": "10s",
			"cycles": 3
		}
	}`)
	if err != nil {
		t.Fatal(err)
	}

	stages := cfg.EffectiveStages()
	if len(stages) != 6 {
		t.Fatalf("expected 6 stages (3 cycles × 2), got %d", len(stages))
	}

	for i, stage := range stages {
		if i%2 == 0 {
			// Burst stage
			if stage.Concurrency != 64 {
				t.Errorf("stage %d: expected concurrency 64, got %d", i, stage.Concurrency)
			}
			if stage.Duration.Duration() != 5*time.Second {
				t.Errorf("stage %d: expected 5s, got %v", i, stage.Duration.Duration())
			}
		} else {
			// Pause stage
			if stage.Concurrency != 0 {
				t.Errorf("stage %d: expected concurrency 0, got %d", i, stage.Concurrency)
			}
			if stage.Duration.Duration() != 10*time.Second {
				t.Errorf("stage %d: expected 10s, got %v", i, stage.Duration.Duration())
			}
		}
	}
}

func TestBurstDefaultCycles(t *testing.T) {
	stages := config.BurstStages(&config.Burst{
		Concurrency:   32,
		BurstDuration: config.Duration(3 * time.Second),
		PauseDuration: config.Duration(7 * time.Second),
		Cycles:        0, // should default to 1
	})
	if len(stages) != 2 {
		t.Fatalf("expected 2 stages (1 default cycle), got %d", len(stages))
	}
	if stages[0].Concurrency != 32 {
		t.Errorf("expected concurrency 32, got %d", stages[0].Concurrency)
	}
}

func TestBurstOverridesOtherModes(t *testing.T) {
	cfg, err := config.Parse(`{
		"burst": {"concurrency": 16, "burst_duration": "2s", "pause_duration": "3s", "cycles": 1},
		"sweep": {"min": 1, "max": 100, "steps": 10, "step_duration": "30s"},
		"stages": [{"concurrency": 50, "duration": "60s"}]
	}`)
	if err != nil {
		t.Fatal(err)
	}
	stages := cfg.EffectiveStages()
	if len(stages) != 2 {
		t.Fatalf("burst should take priority, got %d stages", len(stages))
	}
	if stages[0].Concurrency != 16 {
		t.Errorf("expected burst concurrency 16, got %d", stages[0].Concurrency)
	}
}
