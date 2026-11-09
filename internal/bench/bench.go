// Package bench measures prompt and generation throughput against a
// llama-server instance and summarizes repeated runs.
package bench

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"wool/internal/llamaserver"
	"wool/internal/measure"
)

const (
	EngineServer     = "server"
	EngineLlamaBench = "llama-bench"

	DefaultRuns             = 3
	DefaultPromptTokens     = 512
	DefaultGenerationTokens = 128
	DefaultSeed             = 42
)

// promptSentence tokenizes to roughly ten tokens on common BPE models, so
// repeating it is a simple way to request an approximate prompt length without
// depending on a tokenizer endpoint.
const promptSentence = "The quick brown fox jumps over the lazy dog. "

// Options controls one benchmark.
type Options struct {
	Runs             int
	PromptTokens     int
	GenerationTokens int
	Seed             int
	Warmup           bool
	Cooldown         time.Duration
}

// WithDefaults fills zero values without hiding an explicit zero prompt.
func (o Options) WithDefaults() Options {
	if o.Runs <= 0 {
		o.Runs = DefaultRuns
	}
	if o.PromptTokens < 0 {
		o.PromptTokens = DefaultPromptTokens
	}
	if o.GenerationTokens <= 0 {
		o.GenerationTokens = DefaultGenerationTokens
	}
	return o
}

// Runner sends requests to one server. Client is required.
type Runner struct {
	Client  *llamaserver.Client
	Options Options

	// Sleep and Now are injectable for tests.
	Sleep func(time.Duration)
	Now   func() time.Time
}

// Result is the raw runs plus their summary.
type Result struct {
	Runs   []measure.BenchRun
	Prompt string
	Median measure.BenchStats
	Min    measure.BenchStats
	Max    measure.BenchStats
}

// Run warms up, then performs the requested repetitions.
func (r *Runner) Run(ctx context.Context) (Result, error) {
	if r.Client == nil {
		return Result{}, errors.New("no HTTP client")
	}
	opts := r.Options.WithDefaults()
	prompt := BuildPrompt(opts.PromptTokens)
	res := Result{Prompt: prompt}

	if opts.Warmup {
		if _, err := r.complete(ctx, prompt, minInt(8, opts.GenerationTokens)); err != nil {
			return res, fmt.Errorf("warmup: %w", err)
		}
	}
	for i := 0; i < opts.Runs; i++ {
		if i > 0 && opts.Cooldown > 0 {
			r.sleep(opts.Cooldown)
		}
		run, err := r.complete(ctx, prompt, opts.GenerationTokens)
		if err != nil {
			return res, fmt.Errorf("run %d: %w", i+1, err)
		}
		run.Index = i + 1
		res.Runs = append(res.Runs, run)
	}
	res.Median, res.Min, res.Max = Summarize(res.Runs)
	return res, nil
}

// BuildPrompt returns a deterministic prompt of approximately tokens tokens.
func BuildPrompt(tokens int) string {
	if tokens <= 0 {
		return "Hello"
	}
	repeats := (tokens + 9) / 10
	if repeats < 1 {
		repeats = 1
	}
	return strings.Repeat(promptSentence, repeats)
}

func (r *Runner) complete(ctx context.Context, prompt string, nPredict int) (measure.BenchRun, error) {
	before, metricsOK := r.metrics(ctx)
	start := r.now()
	resp, err := r.Client.Complete(ctx, llamaserver.CompletionRequest{
		Prompt:      prompt,
		NPredict:    nPredict,
		Temperature: 0,
		Seed:        r.Options.WithDefaults().Seed,
		CachePrompt: false,
		Stream:      false,
	})
	elapsed := r.now().Sub(start)
	if err != nil {
		return measure.BenchRun{}, err
	}

	run := measure.BenchRun{
		PromptTokens:     resp.TokensEvaluated,
		GenerationTokens: resp.TokensPredicted,
		TotalMS:          millis(elapsed),
	}
	if t := resp.Timings; t != nil && (t.PromptN > 0 || t.PredictedN > 0) {
		run.PromptTokens = t.PromptN
		run.GenerationTokens = t.PredictedN
		run.PromptTPS = t.PromptPerSecond
		run.GenerationTPS = t.PredictedPerSecond
		run.PromptMS = t.PromptMS
		run.GenerationMS = t.PredictedMS
		run.TotalMS = t.PromptMS + t.PredictedMS
		run.Source = "timings"
	}

	if run.Source == "" && metricsOK {
		if after, err := r.Client.Metrics(ctx, ""); err == nil {
			promptN := after.PromptTokens - before.PromptTokens
			promptS := after.PromptSeconds - before.PromptSeconds
			predictedN := after.PredictedTokens - before.PredictedTokens
			predictedS := after.PredictedSeconds - before.PredictedSeconds
			if promptS > 0 {
				run.PromptTPS = promptN / promptS
				run.PromptMS = promptS * 1000
			}
			if predictedS > 0 {
				run.GenerationTPS = predictedN / predictedS
				run.GenerationMS = predictedS * 1000
			}
			if promptN > 0 {
				run.PromptTokens = int(promptN)
			}
			if predictedN > 0 {
				run.GenerationTokens = int(predictedN)
			}
			if promptS > 0 || predictedS > 0 {
				run.TotalMS = (promptS + predictedS) * 1000
				run.Source = "metrics"
			}
		}
	}

	if run.Source == "" {
		// Last resort: wall time cannot separate prompt from generation, so
		// both rates are conservative and the source says so.
		if secs := elapsed.Seconds(); secs > 0 {
			run.PromptTPS = float64(run.PromptTokens) / secs
			run.GenerationTPS = float64(run.GenerationTokens) / secs
		}
		run.Source = "wall"
	}
	return run, nil
}

func (r *Runner) metrics(ctx context.Context) (llamaserver.Metrics, bool) {
	if r.Client == nil {
		return llamaserver.Metrics{}, false
	}
	m, err := r.Client.Metrics(ctx, "")
	return m, err == nil
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) sleep(d time.Duration) {
	if r.Sleep != nil {
		r.Sleep(d)
		return
	}
	time.Sleep(d)
}

// Summarize returns the median, minimum and maximum of each series.
func Summarize(runs []measure.BenchRun) (median, min, max measure.BenchStats) {
	if len(runs) == 0 {
		return median, min, max
	}
	prompt := make([]float64, len(runs))
	generation := make([]float64, len(runs))
	total := make([]float64, len(runs))
	for i, run := range runs {
		prompt[i] = run.PromptTPS
		generation[i] = run.GenerationTPS
		total[i] = run.TotalMS
	}
	median = measure.BenchStats{PromptTPS: medianOf(prompt), GenerationTPS: medianOf(generation), TotalMS: medianOf(total)}
	min = measure.BenchStats{PromptTPS: minOf(prompt), GenerationTPS: minOf(generation), TotalMS: minOf(total)}
	max = measure.BenchStats{PromptTPS: maxOf(prompt), GenerationTPS: maxOf(generation), TotalMS: maxOf(total)}
	return median, min, max
}

func medianOf(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

func minOf(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	v := values[0]
	for _, x := range values[1:] {
		if x < v {
			v = x
		}
	}
	return v
}

func maxOf(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	v := values[0]
	for _, x := range values[1:] {
		if x > v {
			v = x
		}
	}
	return v
}

func millis(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
