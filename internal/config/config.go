package config

import (
	"sort"
	"time"

	"wool/internal/theme"
)

// Config is the root of config.toml.
type Config struct {
	App      AppConfig                   `toml:"app"`
	Server   ServerConfig                `toml:"server"`
	Defaults Options                     `toml:"defaults"`
	Models   map[string]ModelConfig      `toml:"models"`
	Presets  map[string]PresetConfig     `toml:"presets"`
	Groups   map[string]GroupConfig      `toml:"groups"`
	Router   RouterConfig                `toml:"router"`
	Alerts   AlertsConfig                `toml:"alerts"`
	Themes   map[string]theme.Definition `toml:"themes"`
}

type AppConfig struct {
	Theme       string   `toml:"theme"`
	NerdFonts   bool     `toml:"nerd_fonts"`
	ModelDirs   []string `toml:"model_dirs"`
	LlamaServer string   `toml:"llama_server"`
	LogLines    int      `toml:"log_lines"`
}

type ServerConfig struct {
	Host           string `toml:"host"`
	Port           int    `toml:"port"`
	AutoPort       *bool  `toml:"auto_port,omitempty"`
	StartupTimeout string `toml:"startup_timeout"`
	PollInterval   string `toml:"poll_interval"`
	SmokeTest      bool   `toml:"smoke_test"`
	SmokeTimeout   string `toml:"smoke_timeout"`
	Restart        string `toml:"restart"`
	RestartMax     *int   `toml:"restart_max,omitempty"`
	RestartWindow  string `toml:"restart_window"`
	RestartBackoff string `toml:"restart_backoff"`
}

// AutoPortEnabled defaults to true when auto_port is omitted.
func (s ServerConfig) AutoPortEnabled() bool {
	if s.AutoPort == nil {
		return true
	}
	return *s.AutoPort
}

// RestartEnabled reports whether in-session crash restarts are allowed.
func (s ServerConfig) RestartEnabled() bool {
	return s.Restart == "on-failure" || s.Restart == "always"
}

// RestartMaxAttempts defaults to three when restart_max is omitted. An
// explicit zero disables automatic restarts without changing the policy.
func (s ServerConfig) RestartMaxAttempts() int {
	if s.RestartMax == nil {
		return 3
	}
	return *s.RestartMax
}

// RestartWindowDuration is the sliding window used to count attempts.
func (s ServerConfig) RestartWindowDuration() time.Duration {
	if d, err := time.ParseDuration(s.RestartWindow); err == nil && d > 0 {
		return d
	}
	return 10 * time.Minute
}

// RestartBackoffDuration is the delay before the first automatic restart.
func (s ServerConfig) RestartBackoffDuration() time.Duration {
	if d, err := time.ParseDuration(s.RestartBackoff); err == nil && d > 0 {
		return d
	}
	return 2 * time.Second
}

// Options is a layer of launch options. Pointer fields mean "not set at this
// layer", so defaults, model options and preset options can be merged.
type Options struct {
	Ctx          *int              `toml:"ctx,omitempty"`
	GPULayers    *int              `toml:"gpu_layers,omitempty"`
	NCPUMoE      *int              `toml:"n_cpu_moe,omitempty"`
	Threads      *int              `toml:"threads,omitempty"`
	ThreadsBatch *int              `toml:"threads_batch,omitempty"`
	BatchSize    *int              `toml:"batch_size,omitempty"`
	UBatchSize   *int              `toml:"ubatch_size,omitempty"`
	Parallel     *int              `toml:"parallel,omitempty"`
	FlashAttn    *string           `toml:"flash_attn,omitempty"`
	CacheTypeK   *string           `toml:"cache_type_k,omitempty"`
	CacheTypeV   *string           `toml:"cache_type_v,omitempty"`
	Jinja        *bool             `toml:"jinja,omitempty"`
	Metrics      *bool             `toml:"metrics,omitempty"`
	Sampling     *SamplingOverride `toml:"sampling,omitempty"`
	Extra        map[string]string `toml:"extra,omitempty"`
}

// Sampling is the fully resolved sampler configuration.
type Sampling struct {
	Temp             float64 `toml:"temp"`
	TopP             float64 `toml:"top_p"`
	TopK             int     `toml:"top_k"`
	MinP             float64 `toml:"min_p"`
	RepeatPenalty    float64 `toml:"repeat_penalty"`
	PresencePenalty  float64 `toml:"presence_penalty"`
	FrequencyPenalty float64 `toml:"frequency_penalty"`
	TypicalP         float64 `toml:"typical_p"`
	Mirostat         int     `toml:"mirostat"`
	Seed             int     `toml:"seed"`
}

// SamplingOverride carries only the fields set at one config layer.
type SamplingOverride struct {
	Temp             *float64 `toml:"temp,omitempty"`
	TopP             *float64 `toml:"top_p,omitempty"`
	TopK             *int     `toml:"top_k,omitempty"`
	MinP             *float64 `toml:"min_p,omitempty"`
	RepeatPenalty    *float64 `toml:"repeat_penalty,omitempty"`
	PresencePenalty  *float64 `toml:"presence_penalty,omitempty"`
	FrequencyPenalty *float64 `toml:"frequency_penalty,omitempty"`
	TypicalP         *float64 `toml:"typical_p,omitempty"`
	Mirostat         *int     `toml:"mirostat,omitempty"`
	Seed             *int     `toml:"seed,omitempty"`
}

type ModelConfig struct {
	Path  string `toml:"path"`
	Alias string `toml:"alias"`
	Options
}

type PresetConfig struct {
	Model string `toml:"model"`
	Options
}

// GroupConfig is a warm set: model IDs loaded together in one action.
type GroupConfig struct {
	Models []string `toml:"models"`
}

type RouterConfig struct {
	ModelsMax        int      `toml:"models_max"`
	MemoryMarginMiB  int      `toml:"memory_margin_mib"`
	Autoload         bool     `toml:"autoload"`
	SleepIdleSeconds int      `toml:"sleep_idle_seconds"`
	Metrics          *bool    `toml:"metrics,omitempty"`
	ExtraArgs        []string `toml:"extra_args,omitempty"`
}

func (r RouterConfig) MetricsEnabled() bool {
	if r.Metrics == nil {
		return true
	}
	return *r.Metrics
}

// AlertsConfig controls best-effort operating alerts. Zero thresholds fall
// back to the defaults; validation rejects nonsensical values.
type AlertsConfig struct {
	Enabled     bool   `toml:"enabled"`
	TempC       int    `toml:"temp_c"`
	VRAMPercent int    `toml:"vram_percent"`
	Cooldown    string `toml:"cooldown"`
	Notify      bool   `toml:"notify"`
	Webhook     string `toml:"webhook"`
}

// Resolved is a fully merged launch configuration.
type Resolved struct {
	ModelID      string
	ModelPath    string
	Alias        string
	Ctx          int
	GPULayers    int
	NCPUMoE      int
	Threads      int
	ThreadsBatch int
	BatchSize    int
	UBatchSize   int
	Parallel     int
	FlashAttn    string
	CacheTypeK   string
	CacheTypeV   string
	Jinja        bool
	Metrics      bool
	Sampling     Sampling
	Extra        map[string]string
}

func intp(v int) *int       { return &v }
func strp(v string) *string { return &v }
func boolp(v bool) *bool    { return &v }

// builtinDefaults is the lowest-precedence launch layer. Keeping it separate
// from Config.Defaults is what makes "built-in" distinguishable from an
// explicit [defaults] key.
func builtinDefaults() Options {
	return Options{
		Ctx:          intp(32768),
		GPULayers:    intp(999),
		NCPUMoE:      intp(0),
		Threads:      intp(0), // 0 = resolve to nproc
		ThreadsBatch: intp(0),
		BatchSize:    intp(4096),
		UBatchSize:   intp(256),
		Parallel:     intp(1),
		FlashAttn:    strp("on"),
		CacheTypeK:   strp("q4_0"),
		CacheTypeV:   strp("q4_0"),
		Jinja:        boolp(true),
		Metrics:      boolp(true),
	}
}

// DefaultSampling is the baseline kept from the original launcher scripts.
func DefaultSampling() Sampling {
	return Sampling{
		Temp:             1.0,
		TopP:             0.95,
		TopK:             20,
		MinP:             0.05,
		RepeatPenalty:    1.1,
		PresencePenalty:  0.0,
		FrequencyPenalty: 0.0,
		TypicalP:         1.0,
		Mirostat:         0,
		Seed:             -1,
	}
}

// Load reads and validates a TOML config.
func (c *Config) ApplyDefaults() {
	if c.App.Theme == "" {
		c.App.Theme = "mocha"
	}
	if c.App.LlamaServer == "" {
		c.App.LlamaServer = "llama-server"
	}
	if len(c.App.ModelDirs) == 0 {
		c.App.ModelDirs = []string{"~/models"}
	}
	if c.App.LogLines <= 0 {
		c.App.LogLines = 10000
	}
	if c.Server.Host == "" {
		c.Server.Host = "127.0.0.1"
	}
	if c.Server.Port == 0 {
		c.Server.Port = 8080
	}
	if c.Server.StartupTimeout == "" {
		c.Server.StartupTimeout = "120s"
	}
	if c.Server.PollInterval == "" {
		c.Server.PollInterval = "1s"
	}
	if c.Server.SmokeTimeout == "" {
		c.Server.SmokeTimeout = "30s"
	}
	if c.Server.Restart == "" {
		c.Server.Restart = "never"
	}
	if c.Server.RestartWindow == "" {
		c.Server.RestartWindow = "10m"
	}
	if c.Server.RestartBackoff == "" {
		c.Server.RestartBackoff = "2s"
	}
	if c.Models == nil {
		c.Models = map[string]ModelConfig{}
	}
	if c.Presets == nil {
		c.Presets = map[string]PresetConfig{}
	}
	if c.Groups == nil {
		c.Groups = map[string]GroupConfig{}
	}

	if c.Router.ModelsMax == 0 {
		c.Router.ModelsMax = 1
	}
	if c.Router.MemoryMarginMiB == 0 {
		c.Router.MemoryMarginMiB = 512
	}
	if c.Router.SleepIdleSeconds == 0 {
		c.Router.SleepIdleSeconds = -1
	}

	if c.Alerts.TempC == 0 {
		c.Alerts.TempC = 90
	}
	if c.Alerts.VRAMPercent == 0 {
		c.Alerts.VRAMPercent = 95
	}
	if c.Alerts.Cooldown == "" {
		c.Alerts.Cooldown = "5m"
	}
}

// Validate checks structural and value errors. Missing model files are
// reported by Warnings instead, because a config can be prepared offline.
func (c *Config) ModelIDs() []string {
	ids := make([]string, 0, len(c.Models))
	for id := range c.Models {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (c *Config) PresetIDs() []string {
	ids := make([]string, 0, len(c.Presets))
	for id := range c.Presets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// GroupIDs returns warm-set names in stable order.
func (c *Config) GroupIDs() []string {
	ids := make([]string, 0, len(c.Groups))
	for id := range c.Groups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Resolve merges defaults -> model options -> preset options.
