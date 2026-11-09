package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"wool/internal/measure"
)

// LlamaBenchOptions describes an external llama-bench invocation.
type LlamaBenchOptions struct {
	Bin              string
	ModelPath        string
	Runs             int
	PromptTokens     int
	GenerationTokens int
	Threads          int
	GPULayers        int
	NCPUMoE          int
	BatchSize        int
	UBatchSize       int
	CacheTypeK       string
	CacheTypeV       string
	FlashAttn        string
	NoWarmup         bool
}

// LlamaBenchResult is the parsed output of llama-bench.
type LlamaBenchResult struct {
	Runs    []measure.BenchRun
	Median  measure.BenchStats
	Min     measure.BenchStats
	Max     measure.BenchStats
	Version string
	Args    []string
}

// CommandArgs returns the argv passed to llama-bench, without the binary.
func (o LlamaBenchOptions) CommandArgs() []string {
	runs := o.Runs
	if runs <= 0 {
		runs = DefaultRuns
	}
	pp := o.PromptTokens
	if pp < 0 {
		pp = DefaultPromptTokens
	}
	tg := o.GenerationTokens
	if tg <= 0 {
		tg = DefaultGenerationTokens
	}
	args := []string{
		"-m", o.ModelPath,
		"-p", strconv.Itoa(pp),
		"-n", strconv.Itoa(tg),
		"-r", strconv.Itoa(runs),
		"-o", "json",
	}
	if o.Threads > 0 {
		args = append(args, "-t", strconv.Itoa(o.Threads))
	}
	// 999 is wool's "all layers" sentinel; llama-bench already defaults to -1
	// (all layers), so omit it rather than passing an unusual value.
	if o.GPULayers >= 0 && o.GPULayers < 999 {
		args = append(args, "-ngl", strconv.Itoa(o.GPULayers))
	}
	if o.NCPUMoE > 0 {
		args = append(args, "-ncmoe", strconv.Itoa(o.NCPUMoE))
	}
	if o.BatchSize > 0 {
		args = append(args, "-b", strconv.Itoa(o.BatchSize))
	}
	if o.UBatchSize > 0 {
		args = append(args, "-ub", strconv.Itoa(o.UBatchSize))
	}
	if o.CacheTypeK != "" {
		args = append(args, "-ctk", o.CacheTypeK)
	}
	if o.CacheTypeV != "" {
		args = append(args, "-ctv", o.CacheTypeV)
	}
	if o.FlashAttn != "" {
		args = append(args, "-fa", o.FlashAttn)
	}
	if o.NoWarmup {
		args = append(args, "--no-warmup")
	}
	return args
}

// RunLlamaBench executes llama-bench and parses its JSON output.
func RunLlamaBench(ctx context.Context, opts LlamaBenchOptions) (LlamaBenchResult, error) {
	var res LlamaBenchResult
	if strings.TrimSpace(opts.Bin) == "" {
		return res, errors.New("no llama-bench binary")
	}
	if strings.TrimSpace(opts.ModelPath) == "" {
		return res, errors.New("no model path")
	}
	args := opts.CommandArgs()
	res.Args = args

	cmd := exec.CommandContext(ctx, opts.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := tailText(stderr.String(), 2000)
		if msg == "" {
			return res, fmt.Errorf("llama-bench: %w", err)
		}
		return res, fmt.Errorf("llama-bench: %w: %s", err, msg)
	}

	entries, err := parseLlamaBenchJSON(stdout.Bytes())
	if err != nil {
		return res, err
	}
	res.Runs, res.Version, err = llamaBenchRuns(entries, opts)
	if err != nil {
		return res, err
	}
	res.Median, res.Min, res.Max = Summarize(res.Runs)
	return res, nil
}

type llamaBenchEntry struct {
	BuildCommit string    `json:"build_commit"`
	BuildNumber int       `json:"build_number"`
	NPrompt     int       `json:"n_prompt"`
	NGen        int       `json:"n_gen"`
	AvgNS       float64   `json:"avg_ns"`
	AvgTS       float64   `json:"avg_ts"`
	SamplesNS   []float64 `json:"samples_ns"`
	SamplesTS   []float64 `json:"samples_ts"`
}

func parseLlamaBenchJSON(data []byte) ([]llamaBenchEntry, error) {
	var entries []llamaBenchEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("decode llama-bench output: %w", err)
	}
	if len(entries) == 0 {
		return nil, errors.New("llama-bench returned no results")
	}
	return entries, nil
}

func llamaBenchRuns(entries []llamaBenchEntry, opts LlamaBenchOptions) ([]measure.BenchRun, string, error) {
	var prompt, gen *llamaBenchEntry
	for i := range entries {
		e := &entries[i]
		switch {
		case e.NPrompt > 0 && e.NGen == 0:
			prompt = e
		case e.NGen > 0 && e.NPrompt == 0:
			gen = e
		}
	}
	pp := opts.PromptTokens
	if pp < 0 {
		pp = DefaultPromptTokens
	}
	tg := opts.GenerationTokens
	if tg <= 0 {
		tg = DefaultGenerationTokens
	}
	if prompt == nil && pp > 0 {
		return nil, "", errors.New("llama-bench output has no prompt test")
	}
	if gen == nil && tg > 0 {
		return nil, "", errors.New("llama-bench output has no generation test")
	}

	promptSamples := []float64{}
	if prompt != nil {
		promptSamples = prompt.SamplesTS
		if len(promptSamples) == 0 {
			promptSamples = []float64{prompt.AvgTS}
		}
	}
	genSamples := []float64{}
	if gen != nil {
		genSamples = gen.SamplesTS
		if len(genSamples) == 0 {
			genSamples = []float64{gen.AvgTS}
		}
	}
	n := len(promptSamples)
	if len(genSamples) > n {
		n = len(genSamples)
	}
	if n == 0 {
		n = 1
	}

	runs := make([]measure.BenchRun, 0, n)
	for i := 0; i < n; i++ {
		run := measure.BenchRun{Index: i + 1, Source: "llama-bench"}
		if prompt != nil {
			run.PromptTokens = prompt.NPrompt
			run.PromptTPS = sampleAt(promptSamples, prompt.AvgTS, i)
			run.PromptMS = sampleNS(prompt, i) / 1e6
		}
		if gen != nil {
			run.GenerationTokens = gen.NGen
			run.GenerationTPS = sampleAt(genSamples, gen.AvgTS, i)
			run.GenerationMS = sampleNS(gen, i) / 1e6
		}
		run.TotalMS = run.PromptMS + run.GenerationMS
		runs = append(runs, run)
	}

	version := ""
	if prompt != nil {
		version = llamaBenchVersion(*prompt)
	} else if gen != nil {
		version = llamaBenchVersion(*gen)
	}
	return runs, version, nil
}

func sampleAt(samples []float64, avg float64, i int) float64 {
	if len(samples) == 0 {
		return avg
	}
	if i < len(samples) {
		return samples[i]
	}
	return avg
}

func sampleNS(e *llamaBenchEntry, i int) float64 {
	if e == nil {
		return 0
	}
	if i < len(e.SamplesNS) {
		return e.SamplesNS[i]
	}
	return e.AvgNS
}

func llamaBenchVersion(e llamaBenchEntry) string {
	switch {
	case e.BuildCommit != "" && e.BuildNumber > 0:
		return fmt.Sprintf("llama-bench %s (build %d)", e.BuildCommit, e.BuildNumber)
	case e.BuildCommit != "":
		return "llama-bench " + e.BuildCommit
	case e.BuildNumber > 0:
		return fmt.Sprintf("llama-bench build %d", e.BuildNumber)
	}
	return ""
}

func tailText(s string, max int) string {
	s = strings.TrimSpace(s)
	if max > 0 && len(s) > max {
		s = s[len(s)-max:]
	}
	return s
}
