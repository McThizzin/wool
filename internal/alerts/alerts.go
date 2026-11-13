// Package alerts evaluates best-effort operating conditions and deduplicates
// them into a small stream of user-facing warnings.
//
// The engine is deliberately pure: callers pass one observation (hardware,
// instance health, router model state and a log tail) and the engine returns
// only alerts that passed the per-rule cooldown. Notifications live in
// notify.go so the rules can be tested with a fake clock and notifier.
package alerts

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"wool/internal/config"
)

// Rule names are stable identifiers used by the status bar, JSON output and
// deduplication keys.
const (
	RuleTemperature = "temperature"
	RuleVRAM        = "vram"
	RuleHealth      = "health"
	RuleOOM         = "oom"
	RuleModelLoad   = "model_load"
	RuleRestartLoop = "restart_loop"
)

// Severity is presentation metadata; every current rule is actionable.
const (
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

const (
	defaultCooldown = 5 * time.Minute
	healthGrace     = 15 * time.Second
	restartWindow   = 10 * time.Minute
	restartCount    = 3
	maxMessage      = 180
)

// Config is the validated runtime form of [alerts].
type Config struct {
	Enabled     bool
	TempC       int
	VRAMPercent int
	Cooldown    time.Duration
	Notify      bool
	Webhook     string
}

// FromConfig converts the TOML shape. The config validator has already
// checked the values; invalid durations fall back to the default so a hand
// constructed Config cannot disable deduplication by accident.
func FromConfig(c config.AlertsConfig) Config {
	cooldown := defaultCooldown
	if d, err := time.ParseDuration(c.Cooldown); err == nil && d > 0 {
		cooldown = d
	}
	return Config{
		Enabled:     c.Enabled,
		TempC:       c.TempC,
		VRAMPercent: c.VRAMPercent,
		Cooldown:    cooldown,
		Notify:      c.Notify,
		Webhook:     c.Webhook,
	}
}

// Alert is one deduplicated condition.
type Alert struct {
	At       time.Time `json:"at"`
	Rule     string    `json:"rule"`
	Severity string    `json:"severity"`
	Subject  string    `json:"subject"`
	Message  string    `json:"message"`
}

// Instance is one managed server or router.
type Instance struct {
	Name      string
	Alive     bool
	Health    string
	StartedAt time.Time
}

// Model is one router model. Failed comes from the router's status block.
type Model struct {
	ID     string
	Status string
	Failed bool
}

// Input is one observation. Logs maps an instance name to its tail, so an
// OOM in the router log is attributed to the router rather than the server.
type Input struct {
	At          time.Time
	TempC       int
	HasTemp     bool
	VRAMPercent float64
	HasVRAM     bool
	Instances   []Instance
	Models      []Model
	Logs        map[string][]string
}

// Engine keeps the deduplication and restart history between observations.
type Engine struct {
	cfg Config

	last        map[string]time.Time
	starts      map[string]time.Time
	restarts    map[string][]time.Time
	healthSince map[string]time.Time
	eventSeen   map[string]string
}

// New builds an engine. It is safe to use when alerts are disabled.
func New(cfg Config) *Engine {
	return &Engine{
		cfg:         cfg,
		last:        map[string]time.Time{},
		starts:      map[string]time.Time{},
		restarts:    map[string][]time.Time{},
		healthSince: map[string]time.Time{},
		eventSeen:   map[string]string{},
	}
}

// Enabled reports whether evaluation is turned on.
func (e *Engine) Enabled() bool { return e != nil && e.cfg.Enabled }

// Config returns the runtime configuration.
func (e *Engine) Config() Config { return e.cfg }

// Evaluate applies every rule and returns only alerts that are past their
// cooldown. Repeated conditions are still evaluated so the caller can display
// them; they are simply not emitted again until the window expires.
func (e *Engine) Evaluate(in Input) []Alert {
	if !e.Enabled() {
		return nil
	}
	now := in.At
	if now.IsZero() {
		now = time.Now()
	}
	var out []Alert
	fire := func(rule, severity, subject, message string) {
		key := rule + "|" + subject
		if last, ok := e.last[key]; ok && now.Sub(last) < e.cfg.Cooldown {
			return
		}
		e.last[key] = now
		out = append(out, Alert{
			At:       now,
			Rule:     rule,
			Severity: severity,
			Subject:  subject,
			Message:  oneLine(message, maxMessage),
		})
	}

	if in.HasTemp && e.cfg.TempC > 0 && in.TempC >= e.cfg.TempC {
		fire(RuleTemperature, SeverityWarning, "gpu",
			fmt.Sprintf("GPU temperature %d°C reached the %d°C threshold", in.TempC, e.cfg.TempC))
	}
	if in.HasVRAM && e.cfg.VRAMPercent > 0 && in.VRAMPercent >= float64(e.cfg.VRAMPercent) {
		fire(RuleVRAM, SeverityWarning, "gpu",
			fmt.Sprintf("VRAM usage %.0f%% reached the %d%% threshold", in.VRAMPercent, e.cfg.VRAMPercent))
	}

	seen := map[string]bool{}
	for _, inst := range in.Instances {
		if inst.Name == "" {
			continue
		}
		seen[inst.Name] = true
		e.trackRestart(inst, now, fire)
		if inst.Alive && healthFailed(inst.Health) {
			since, ok := e.healthSince[inst.Name]
			if !ok {
				since = now
				e.healthSince[inst.Name] = now
			}
			// Do not alert during the first seconds of a launch: /health is
			// expected to fail while the model is still loading. When the
			// start time is known, the grace window is anchored there;
			// otherwise it starts at the first failed observation.
			graceStart := since
			if !inst.StartedAt.IsZero() {
				graceStart = inst.StartedAt
			}
			if now.Sub(graceStart) >= healthGrace {
				fire(RuleHealth, SeverityCritical, inst.Name,
					fmt.Sprintf("%s health is %s", inst.Name, inst.Health))
			}
		} else {
			delete(e.healthSince, inst.Name)
		}
	}
	for name := range e.healthSince {
		if !seen[name] {
			delete(e.healthSince, name)
		}
	}

	for _, m := range in.Models {
		if m.Failed {
			fire(RuleModelLoad, SeverityCritical, m.ID,
				fmt.Sprintf("model %s failed to load", m.ID))
		}
	}

	for name, lines := range in.Logs {
		e.scanLogs(name, lines, fire)
	}
	return out
}

// trackRestart counts observed StartedAt changes. A restart is only counted
// after the instance has been seen once, so the first observation is not one.
func (e *Engine) trackRestart(inst Instance, now time.Time, fire func(rule, severity, subject, message string)) {
	if !inst.Alive || inst.StartedAt.IsZero() {
		return
	}
	prev, ok := e.starts[inst.Name]
	if !ok {
		e.starts[inst.Name] = inst.StartedAt
	} else if !inst.StartedAt.Equal(prev) {
		e.starts[inst.Name] = inst.StartedAt
		e.restarts[inst.Name] = append(e.restarts[inst.Name], now)
	}
	cutoff := now.Add(-restartWindow)
	times := e.restarts[inst.Name]
	kept := times[:0]
	for _, t := range times {
		if !t.Before(cutoff) {
			kept = append(kept, t)
		}
	}
	e.restarts[inst.Name] = kept
	if len(kept) >= restartCount {
		fire(RuleRestartLoop, SeverityCritical, inst.Name,
			fmt.Sprintf("%s restarted %d times in %s", inst.Name, len(kept), restartWindow))
	}
}

var (
	oomPattern  = regexp.MustCompile(`(?i)(out of memory|outofmemory|\boom\b|failed to allocate)`)
	loadPattern = regexp.MustCompile(`(?i)(failed to load model|error loading model|model load failed|failed to load)`)
)

// scanLogs turns the newest matching line into one event alert. The line
// fingerprint prevents an old OOM that stays in the tail from firing again
// after every cooldown.
func (e *Engine) scanLogs(name string, lines []string, fire func(rule, severity, subject, message string)) {
	if name == "" {
		return
	}
	e.scanPattern(name, lines, RuleOOM, oomPattern, fire)
	e.scanPattern(name, lines, RuleModelLoad, loadPattern, fire)
}

func (e *Engine) scanPattern(name string, lines []string, rule string, re *regexp.Regexp, fire func(rule, severity, subject, message string)) {
	key := rule + "|" + name
	line := lastMatch(lines, re)
	if line == "" {
		delete(e.eventSeen, key)
		return
	}
	if e.eventSeen[key] == line {
		return
	}
	e.eventSeen[key] = line
	fire(rule, SeverityCritical, name, name+": "+line)
}

func lastMatch(lines []string, re *regexp.Regexp) string {
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" && re.MatchString(line) {
			return line
		}
	}
	return ""
}

func healthFailed(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "ok", "loading", "loading model", "starting":
		return false
	}
	return true
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max && max > 3 {
		s = s[:max-3] + "..."
	}
	return s
}
