package config

import "time"

// ScenarioConfig is the universal intermediate representation for benchmark
// configurations. Both JSON configs and Starlark scripts produce this type.
type ScenarioConfig struct {
	Target   string          // default target URL (empty = use CLI flag)
	Model    string          // default model (empty = use CLI flag)
	Workload Workload        // default workload for stages that don't override
	Stages   []ScenarioStage // ordered stages to execute
}

// ScenarioStage is a single phase of a benchmark with optional per-stage overrides.
type ScenarioStage struct {
	Name        string        // human-readable label (for logging/analysis)
	Duration    time.Duration // how long this stage runs
	Mode        string        // "concurrent", "constant", "poisson" (empty = inherit)
	Concurrency int           // concurrent streams (0 = inherit)
	Rate        float64       // req/s for constant/poisson (0 = inherit)
	MaxInFlight int           // cap for rate-based modes (0 = unlimited)
	Rampup      time.Duration // stagger stream starts / ramp rate
	Workload    *Workload     // nil = inherit from scenario
	Target      string        // empty = inherit from scenario
	Model       string        // empty = inherit from scenario
	Warmup      bool          // true = don't record results
}

// ToScenarioConfig converts a JSON Config into the universal ScenarioConfig IR.
func (c *Config) ToScenarioConfig() *ScenarioConfig {
	sc := &ScenarioConfig{
		Workload: c.Workload,
	}

	// Convert warmup to a warmup stage if present
	effectiveStages := c.EffectiveStages()
	if c.Warmup != nil && c.Warmup.Duration.Duration() > 0 {
		var rampup time.Duration
		if c.Warmup.Stagger {
			rampup = c.Warmup.Duration.Duration()
		}
		warmupConcurrency := 0
		if len(effectiveStages) > 0 {
			warmupConcurrency = effectiveStages[0].Concurrency
		}
		sc.Stages = append(sc.Stages, ScenarioStage{
			Name:        "warmup",
			Duration:    c.Warmup.Duration.Duration(),
			Mode:        c.Load.Mode,
			Concurrency: warmupConcurrency,
			Rampup:      rampup,
			Warmup:      true,
		})
	}

	for _, s := range effectiveStages {
		sc.Stages = append(sc.Stages, ScenarioStage{
			Duration:    s.Duration.Duration(),
			Mode:        c.Load.Mode,
			Concurrency: s.Concurrency,
			Rate:        c.Load.Rate,
			MaxInFlight: c.Load.MaxInFlight,
			Rampup:      c.Load.Rampup.Duration(),
		})
	}

	return sc
}
