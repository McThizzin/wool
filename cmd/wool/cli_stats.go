package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"wool/internal/daemon"
	"wool/internal/llamaserver"
)

const statsUsage = `usage: wool stats [MODEL] [options]

Shows live inference rates by scraping /metrics and /slots of the running
server twice, one window apart. Without MODEL the standalone server is used
when alive, else the router's first loaded model. With MODEL the router must
be running that model.

options:
  --window D       time between the two scrapes (default 1s)
  --json           print the stable machine-readable snapshot`

type statsOptions struct {
	model  string
	window time.Duration
	asJSON bool
}

func parseStatsArgs(args []string) (statsOptions, error) {
	o := statsOptions{window: time.Second}
	setWindow := func(raw string) error {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return fmt.Errorf("--window must be a positive duration like 2s")
		}
		o.window = d
		return nil
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			o.asJSON = true
		case a == "-h" || a == "--help":
			return o, flag.ErrHelp
		case a == "--window":
			if i+1 >= len(args) {
				return o, fmt.Errorf("--window needs a value")
			}
			i++
			if err := setWindow(args[i]); err != nil {
				return o, err
			}
		case strings.HasPrefix(a, "--window="):
			if err := setWindow(strings.TrimPrefix(a, "--window=")); err != nil {
				return o, err
			}
		case strings.HasPrefix(a, "-"):
			return o, fmt.Errorf("unknown option %q", a)
		default:
			if o.model != "" {
				return o, fmt.Errorf("only one MODEL is allowed")
			}
			o.model = a
		}
	}
	return o, nil
}

// statsTarget is the server a stats snapshot is scraped from.
type statsTarget struct {
	source string // "server" or "router"
	model  string // router model ID, or the server's preset/model name
	url    string
	client *llamaserver.Client
}

// scrapeModel is the model query parameter /metrics and /slots need. Only a
// router requires one.
func (t statsTarget) scrapeModel() string {
	if t.source == "router" {
		return t.model
	}
	return ""
}

// resolveStatsTarget picks what to scrape. An explicit model requires the
// router to be running it; otherwise the standalone server wins, then the
// router's first loaded model.
func resolveStatsTarget(model string) (statsTarget, error) {
	mgr := daemon.NewManager()
	if model != "" {
		inst, err := mgr.Status("router")
		if err != nil || !inst.Alive {
			return statsTarget{}, errors.New("router is not running (start it with `wool router start`)")
		}
		target, err := routerStatsTarget(inst, model)
		if err != nil {
			return statsTarget{}, err
		}
		return target, nil
	}
	if inst, err := mgr.Status("server"); err == nil && inst.Alive {
		what := inst.Model
		if inst.Preset != "" {
			what = inst.Preset
		}
		return statsTarget{source: "server", model: what, url: inst.URL(), client: llamaserver.NewClient(inst.Host, inst.Port)}, nil
	}
	if inst, err := mgr.Status("router"); err == nil && inst.Alive {
		target, err := routerStatsTarget(inst, "")
		if err != nil {
			return statsTarget{}, err
		}
		return target, nil
	}
	return statsTarget{}, errors.New("no server is running (start one with `wool run TARGET` or `wool router start`)")
}

// routerStatsTarget builds a router target. With want empty it picks the
// first loaded or sleeping model, sorted for determinism.
func routerStatsTarget(inst daemon.Instance, want string) (statsTarget, error) {
	client := llamaserver.NewClient(inst.Host, inst.Port)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	models, err := client.Models(ctx, false)
	if err != nil {
		return statsTarget{}, fmt.Errorf("router models: %w", err)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	loaded := func(status string) bool {
		return status == "loaded" || status == "sleeping"
	}
	for _, rm := range models {
		if want != "" {
			if rm.ID != want {
				continue
			}
			if !loaded(rm.Status.Value) {
				return statsTarget{}, fmt.Errorf("%s is %s on the router; load it with `wool router load %s`", want, rm.Status.Value, want)
			}
			return statsTarget{source: "router", model: want, url: inst.URL(), client: client}, nil
		}
		if loaded(rm.Status.Value) {
			return statsTarget{source: "router", model: rm.ID, url: inst.URL(), client: client}, nil
		}
	}
	if want != "" {
		return statsTarget{}, fmt.Errorf("router does not list %s", want)
	}
	return statsTarget{}, errors.New("router is running but no model is loaded")
}

// statsSnapshot is the stable `wool stats --json` output. Version 1.
type statsSnapshot struct {
	Version            int        `json:"version"`
	Source             string     `json:"source"`
	Model              string     `json:"model,omitempty"`
	URL                string     `json:"url"`
	WindowSeconds      float64    `json:"window_seconds"`
	PromptTPS          float64    `json:"prompt_tps"`
	GenTPS             float64    `json:"gen_tps"`
	PromptTokens       float64    `json:"prompt_tokens"`
	GenTokens          float64    `json:"gen_tokens"`
	LifetimePromptTPS  float64    `json:"lifetime_prompt_tps"`
	LifetimeGenTPS     float64    `json:"lifetime_gen_tps"`
	RequestsProcessing float64    `json:"requests_processing"`
	RequestsDeferred   float64    `json:"requests_deferred"`
	Slots              statsSlots `json:"slots"`
	Speculative        bool       `json:"speculative"`
	SpecAcceptPercent  float64    `json:"spec_accept_percent"`
	CounterReset       bool       `json:"counter_reset"`
}

// statsSlots is the aggregate /slots occupancy.
type statsSlots struct {
	Total  int     `json:"total"`
	Busy   int     `json:"busy"`
	KVUsed int     `json:"kv_used"`
	KVCap  int     `json:"kv_cap"`
	KVFill float64 `json:"kv_fill_percent"`
}

const statsVersion = 1

func cmdStats(cfgPath string, args []string) int {
	_ = cfgPath
	opts, err := parseStatsArgs(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Println(statsUsage)
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n%s\n", err, statsUsage)
		return 2
	}
	target, err := resolveStatsTarget(opts.model)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: stats: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	snapshot, err := captureStats(ctx, target, opts.window)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: stats: %v\n", err)
		return 1
	}
	if opts.asJSON {
		return writeJSON(snapshot)
	}
	printStats(snapshot)
	return 0
}

// captureStats scrapes the target twice, window apart, and computes rates
// from the counter deltas. Slots come from the second scrape.
func captureStats(ctx context.Context, target statsTarget, window time.Duration) (statsSnapshot, error) {
	client := target.client
	client.HTTP.Timeout = 5 * time.Second
	first, _, reason := client.ScrapeInference(ctx, target.scrapeModel())
	if reason != "" {
		return statsSnapshot{}, errors.New(reason)
	}
	at1 := time.Now()
	select {
	case <-ctx.Done():
		return statsSnapshot{}, ctx.Err()
	case <-time.After(window):
	}
	second, slots, reason := client.ScrapeInference(ctx, target.scrapeModel())
	if reason != "" {
		return statsSnapshot{}, errors.New(reason)
	}
	at2 := time.Now()

	var rates llamaserver.Rates
	rates.Update(first, at1)
	rates.Update(second, at2)
	sum := llamaserver.SummarizeSlots(slots)
	return statsSnapshot{
		Version:            statsVersion,
		Source:             target.source,
		Model:              target.model,
		URL:                target.url,
		WindowSeconds:      at2.Sub(at1).Seconds(),
		PromptTPS:          rates.PromptTPS,
		GenTPS:             rates.GenTPS,
		PromptTokens:       rates.PromptTokens,
		GenTokens:          rates.GenTokens,
		LifetimePromptTPS:  second.PromptTPS(),
		LifetimeGenTPS:     second.PredictedTPS(),
		RequestsProcessing: second.RequestsProcessing,
		RequestsDeferred:   second.RequestsDeferred,
		Slots: statsSlots{
			Total:  sum.Total,
			Busy:   sum.Busy,
			KVUsed: sum.KVUsed,
			KVCap:  sum.KVCap,
			KVFill: sum.KVPercent(),
		},
		Speculative:       rates.SpecSeen,
		SpecAcceptPercent: rates.SpecRate,
		CounterReset:      rates.Reset,
	}, nil
}

func printStats(s statsSnapshot) {
	fmt.Printf("%s %s · %s · window %.1fs\n", s.Source, fallbackDash(s.Model), s.URL, s.WindowSeconds)
	fmt.Printf("prompt:      %.1f t/s (%s tokens)\n", s.PromptTPS, trimTokens(s.PromptTokens))
	fmt.Printf("generation: %.1f t/s (%s tokens)\n", s.GenTPS, trimTokens(s.GenTokens))
	fmt.Printf("lifetime:    %.1f prompt · %.1f gen t/s\n", s.LifetimePromptTPS, s.LifetimeGenTPS)
	fmt.Printf("requests:   %.0f processing · %.0f deferred\n", s.RequestsProcessing, s.RequestsDeferred)
	fmt.Printf("slots:       %d/%d busy · KV %s/%s tok (%.0f%%)\n",
		s.Slots.Busy, s.Slots.Total, humanTokensCLI(s.Slots.KVUsed), humanTokensCLI(s.Slots.KVCap), s.Slots.KVFill)
	if s.Speculative {
		fmt.Printf("speculative: %.0f%% accepted\n", s.SpecAcceptPercent)
	}
	if s.CounterReset {
		fmt.Println("note: a counter reset was detected (the server restarted)")
	}
}

// humanTokensCLI renders a token count with k/M suffixes.
