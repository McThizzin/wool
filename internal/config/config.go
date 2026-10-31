package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"wool/internal/theme"
)

// Config is the root of config.toml.
type Config struct {
	App      AppConfig                   `toml:"app"`
	Server   ServerConfig                `toml:"server"`
	Defaults Options                     `toml:"defaults"`
	Models   map[string]ModelConfig      `toml:"models"`
	Presets  map[string]PresetConfig     `toml:"presets"`
	Router   RouterConfig                `toml:"router"`
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
}

// AutoPortEnabled defaults to true when auto_port is omitted.
func (s ServerConfig) AutoPortEnabled() bool {
	if s.AutoPort == nil {
		return true
	}
	return *s.AutoPort
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
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b, path)
}

func Parse(b []byte, source string) (*Config, error) {
	var cfg Config
	if err := toml.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", source, err)
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	return &cfg, nil
}

// ApplyDefaults fills unset application-level values. Launch defaults are a
// separate layer applied during resolution, so an explicit [defaults] key
// stays distinguishable from a built-in value.
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
	if c.Models == nil {
		c.Models = map[string]ModelConfig{}
	}
	if c.Presets == nil {
		c.Presets = map[string]PresetConfig{}
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
}

// Validate checks structural and value errors. Missing model files are
// reported by Warnings instead, because a config can be prepared offline.
func (c *Config) Validate() error {
	var errs []error
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		errs = append(errs, fmt.Errorf("server.port must be between 1 and 65535"))
	}
	if err := validateSampling("defaults.sampling", applySampling(DefaultSampling(), c.Defaults.Sampling)); err != nil {
		errs = append(errs, err)
	}
	for id, m := range c.Models {
		if err := validateID("models."+id, id); err != nil {
			errs = append(errs, err)
		}
		if strings.TrimSpace(m.Path) == "" {
			errs = append(errs, fmt.Errorf("models.%s.path is empty", id))
		}
		if m.Options.Sampling != nil {
			if err := validateSampling("models."+id+".sampling", applySampling(DefaultSampling(), m.Options.Sampling)); err != nil {
				errs = append(errs, err)
			}
		}
	}
	for id, p := range c.Presets {
		if err := validateID("presets."+id, id); err != nil {
			errs = append(errs, err)
		}
		if strings.TrimSpace(p.Model) == "" {
			errs = append(errs, fmt.Errorf("presets.%s.model is empty", id))
			continue
		}
		if _, ok := c.Models[p.Model]; !ok {
			errs = append(errs, fmt.Errorf("presets.%s references unknown model %q", id, p.Model))
		}
		if p.Options.Sampling != nil {
			if err := validateSampling("presets."+id+".sampling", applySampling(DefaultSampling(), p.Options.Sampling)); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if c.Router.ModelsMax < 0 {
		errs = append(errs, fmt.Errorf("router.models_max cannot be negative"))
	}
	if c.Router.MemoryMarginMiB < 0 {
		errs = append(errs, fmt.Errorf("router.memory_margin_mib cannot be negative"))
	}
	if c.Router.SleepIdleSeconds < -1 {
		errs = append(errs, fmt.Errorf("router.sleep_idle_seconds must be -1 or greater"))
	}
	for name := range c.Themes {
		if err := validateID("themes."+name, name); err != nil {
			errs = append(errs, err)
		}
	}
	if err := theme.ValidateAll(c.Themes); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func validateSampling(where string, s Sampling) error {
	var errs []error
	if s.Temp < 0 {
		errs = append(errs, fmt.Errorf("%s.temp cannot be negative", where))
	}
	if s.TopP < 0 || s.TopP > 1 {
		errs = append(errs, fmt.Errorf("%s.top_p must be between 0 and 1", where))
	}
	if s.MinP < 0 || s.MinP > 1 {
		errs = append(errs, fmt.Errorf("%s.min_p must be between 0 and 1", where))
	}
	if s.TypicalP < 0 || s.TypicalP > 1 {
		errs = append(errs, fmt.Errorf("%s.typical_p must be between 0 and 1", where))
	}
	if s.Mirostat < 0 || s.Mirostat > 2 {
		errs = append(errs, fmt.Errorf("%s.mirostat must be 0, 1 or 2", where))
	}
	return errors.Join(errs...)
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validateID(where, id string) error {
	if len(id) > 64 {
		return fmt.Errorf("%s is longer than 64 characters", where)
	}
	if !idPattern.MatchString(id) {
		return fmt.Errorf("%s must match [A-Za-z0-9][A-Za-z0-9._-]* (it is used as a router section and command name)", where)
	}
	return nil
}

// Warnings reports non-fatal problems such as missing model files.
func (c *Config) Warnings() []string {
	var out []string
	for _, id := range c.ModelIDs() {
		path := AbsPath(c.Models[id].Path)
		if _, err := os.Stat(path); err != nil {
			out = append(out, fmt.Sprintf("model %s: %v", id, err))
		}
	}
	for _, dir := range c.App.ModelDirs {
		path := AbsPath(dir)
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			out = append(out, fmt.Sprintf("model dir %s is not accessible", path))
		}
	}
	return out
}

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

// Resolve merges defaults -> model options -> preset options.
func (c *Config) Resolve(modelID string, preset *PresetConfig) (Resolved, error) {
	if preset != nil {
		if preset.Model == "" {
			return Resolved{}, fmt.Errorf("preset has no model")
		}
		modelID = preset.Model
	}
	model, ok := c.Models[modelID]
	if !ok {
		return Resolved{}, fmt.Errorf("unknown model %q", modelID)
	}

	opts := builtinDefaults()
	mergeOptions(&opts, c.Defaults)
	mergeOptions(&opts, model.Options)
	if preset != nil {
		mergeOptions(&opts, preset.Options)
	}

	alias := model.Alias
	if alias == "" {
		alias = modelID
	}
	threads := derefInt(opts.Threads)
	if threads == 0 {
		threads = runtime.NumCPU()
	}
	threadsBatch := derefInt(opts.ThreadsBatch)
	if threadsBatch == 0 {
		threadsBatch = threads
	}
	sampling := applySampling(DefaultSampling(), opts.Sampling)
	extra := map[string]string{}
	for k, v := range opts.Extra {
		extra[k] = v
	}

	return Resolved{
		ModelID:      modelID,
		ModelPath:    AbsPath(model.Path),
		Alias:        alias,
		Ctx:          derefInt(opts.Ctx),
		GPULayers:    derefInt(opts.GPULayers),
		NCPUMoE:      derefInt(opts.NCPUMoE),
		Threads:      threads,
		ThreadsBatch: threadsBatch,
		BatchSize:    derefInt(opts.BatchSize),
		UBatchSize:   derefInt(opts.UBatchSize),
		Parallel:     derefInt(opts.Parallel),
		FlashAttn:    derefString(opts.FlashAttn),
		CacheTypeK:   derefString(opts.CacheTypeK),
		CacheTypeV:   derefString(opts.CacheTypeV),
		Jinja:        derefBool(opts.Jinja),
		Metrics:      derefBool(opts.Metrics),
		Sampling:     sampling,
		Extra:        extra,
	}, nil
}

// Provenance says which config layer supplied a resolved field.
type Provenance struct {
	Source string `json:"source"` // built-in, defaults, model or preset
	Layer  string `json:"layer"`  // the exact TOML layer
}

// Source labels reported by ResolveDetailed.
const (
	SourceBuiltin  = "built-in"
	SourceDefaults = "defaults"
	SourceModel    = "model"
	SourcePreset   = "preset"
)

// optionFields is the canonical order of launch options exposed by the editor
// and `wool config explain`.
var optionFields = []string{
	"ctx",
	"gpu_layers",
	"n_cpu_moe",
	"threads",
	"threads_batch",
	"batch_size",
	"ubatch_size",
	"parallel",
	"flash_attn",
	"cache_type_k",
	"cache_type_v",
	"jinja",
	"metrics",
	"sampling.temp",
	"sampling.top_p",
	"sampling.top_k",
	"sampling.min_p",
	"sampling.repeat_penalty",
	"sampling.presence_penalty",
	"sampling.frequency_penalty",
	"sampling.typical_p",
	"sampling.mirostat",
	"sampling.seed",
}

// OptionFields returns the canonical launch-option names in display order.
func OptionFields() []string {
	return append([]string(nil), optionFields...)
}

// ResolveDetailed merges the same layers as Resolve and reports where each
// launch option came from. Pass presetName to resolve a preset; pass an empty
// presetName and a model ID to resolve a model.
func (c *Config) ResolveDetailed(modelID, presetName string) (Resolved, map[string]Provenance, error) {
	var preset *PresetConfig
	if presetName != "" {
		p, ok := c.Presets[presetName]
		if !ok {
			return Resolved{}, nil, fmt.Errorf("unknown preset %q", presetName)
		}
		preset = &p
	}
	r, err := c.Resolve(modelID, preset)
	if err != nil {
		return Resolved{}, nil, err
	}
	sources := make(map[string]Provenance, len(optionFields))
	for _, field := range optionFields {
		sources[field] = c.provenance(r.ModelID, presetName, field)
	}
	return r, sources, nil
}

// provenance checks layers from highest to lowest precedence.
func (c *Config) provenance(modelID, presetName, field string) Provenance {
	if presetName != "" {
		if p, ok := c.Presets[presetName]; ok && optionFieldSet(p.Options, field) {
			return Provenance{Source: SourcePreset, Layer: "presets." + presetName}
		}
	}
	if m, ok := c.Models[modelID]; ok && optionFieldSet(m.Options, field) {
		return Provenance{Source: SourceModel, Layer: "models." + modelID}
	}
	if optionFieldSet(c.Defaults, field) {
		return Provenance{Source: SourceDefaults, Layer: "defaults"}
	}
	return Provenance{Source: SourceBuiltin, Layer: "built-in"}
}

func optionFieldSet(o Options, field string) bool {
	if key, ok := strings.CutPrefix(field, "sampling."); ok {
		return samplingFieldSet(o.Sampling, key)
	}
	switch field {
	case "ctx":
		return o.Ctx != nil
	case "gpu_layers":
		return o.GPULayers != nil
	case "n_cpu_moe":
		return o.NCPUMoE != nil
	case "threads":
		return o.Threads != nil
	case "threads_batch":
		return o.ThreadsBatch != nil
	case "batch_size":
		return o.BatchSize != nil
	case "ubatch_size":
		return o.UBatchSize != nil
	case "parallel":
		return o.Parallel != nil
	case "flash_attn":
		return o.FlashAttn != nil
	case "cache_type_k":
		return o.CacheTypeK != nil
	case "cache_type_v":
		return o.CacheTypeV != nil
	case "jinja":
		return o.Jinja != nil
	case "metrics":
		return o.Metrics != nil
	}
	return false
}

func samplingFieldSet(o *SamplingOverride, key string) bool {
	if o == nil {
		return false
	}
	switch key {
	case "temp":
		return o.Temp != nil
	case "top_p":
		return o.TopP != nil
	case "top_k":
		return o.TopK != nil
	case "min_p":
		return o.MinP != nil
	case "repeat_penalty":
		return o.RepeatPenalty != nil
	case "presence_penalty":
		return o.PresencePenalty != nil
	case "frequency_penalty":
		return o.FrequencyPenalty != nil
	case "typical_p":
		return o.TypicalP != nil
	case "mirostat":
		return o.Mirostat != nil
	case "seed":
		return o.Seed != nil
	}
	return false
}

// ResolveDefaults returns the defaults layer with no model-specific options.
func (c *Config) ResolveDefaults() Resolved {
	opts := builtinDefaults()
	mergeOptions(&opts, c.Defaults)
	threads := derefInt(opts.Threads)
	if threads == 0 {
		threads = runtime.NumCPU()
	}
	threadsBatch := derefInt(opts.ThreadsBatch)
	if threadsBatch == 0 {
		threadsBatch = threads
	}
	sampling := applySampling(DefaultSampling(), opts.Sampling)
	return Resolved{
		Ctx:          derefInt(opts.Ctx),
		GPULayers:    derefInt(opts.GPULayers),
		NCPUMoE:      derefInt(opts.NCPUMoE),
		Threads:      threads,
		ThreadsBatch: threadsBatch,
		BatchSize:    derefInt(opts.BatchSize),
		UBatchSize:   derefInt(opts.UBatchSize),
		Parallel:     derefInt(opts.Parallel),
		FlashAttn:    derefString(opts.FlashAttn),
		CacheTypeK:   derefString(opts.CacheTypeK),
		CacheTypeV:   derefString(opts.CacheTypeV),
		Jinja:        derefBool(opts.Jinja),
		Metrics:      derefBool(opts.Metrics),
		Sampling:     sampling,
		Extra:        map[string]string{},
	}
}

func mergeOptions(dst *Options, src Options) {
	if src.Ctx != nil {
		dst.Ctx = src.Ctx
	}
	if src.GPULayers != nil {
		dst.GPULayers = src.GPULayers
	}
	if src.NCPUMoE != nil {
		dst.NCPUMoE = src.NCPUMoE
	}
	if src.Threads != nil {
		dst.Threads = src.Threads
	}
	if src.ThreadsBatch != nil {
		dst.ThreadsBatch = src.ThreadsBatch
	}
	if src.BatchSize != nil {
		dst.BatchSize = src.BatchSize
	}
	if src.UBatchSize != nil {
		dst.UBatchSize = src.UBatchSize
	}
	if src.Parallel != nil {
		dst.Parallel = src.Parallel
	}
	if src.FlashAttn != nil {
		dst.FlashAttn = src.FlashAttn
	}
	if src.CacheTypeK != nil {
		dst.CacheTypeK = src.CacheTypeK
	}
	if src.CacheTypeV != nil {
		dst.CacheTypeV = src.CacheTypeV
	}
	if src.Jinja != nil {
		dst.Jinja = src.Jinja
	}
	if src.Metrics != nil {
		dst.Metrics = src.Metrics
	}
	if src.Sampling != nil {
		if dst.Sampling == nil {
			dst.Sampling = &SamplingOverride{}
		}
		mergeSamplingOverride(dst.Sampling, src.Sampling)
	}
	if len(src.Extra) > 0 {
		if dst.Extra == nil {
			dst.Extra = map[string]string{}
		}
		for k, v := range src.Extra {
			dst.Extra[k] = v
		}
	}
}

func applySampling(base Sampling, o *SamplingOverride) Sampling {
	if o == nil {
		return base
	}
	if o.Temp != nil {
		base.Temp = *o.Temp
	}
	if o.TopP != nil {
		base.TopP = *o.TopP
	}
	if o.TopK != nil {
		base.TopK = *o.TopK
	}
	if o.MinP != nil {
		base.MinP = *o.MinP
	}
	if o.RepeatPenalty != nil {
		base.RepeatPenalty = *o.RepeatPenalty
	}
	if o.PresencePenalty != nil {
		base.PresencePenalty = *o.PresencePenalty
	}
	if o.FrequencyPenalty != nil {
		base.FrequencyPenalty = *o.FrequencyPenalty
	}
	if o.TypicalP != nil {
		base.TypicalP = *o.TypicalP
	}
	if o.Mirostat != nil {
		base.Mirostat = *o.Mirostat
	}
	if o.Seed != nil {
		base.Seed = *o.Seed
	}
	return base
}

func mergeSamplingOverride(dst, src *SamplingOverride) {
	if src.Temp != nil {
		dst.Temp = src.Temp
	}
	if src.TopP != nil {
		dst.TopP = src.TopP
	}
	if src.TopK != nil {
		dst.TopK = src.TopK
	}
	if src.MinP != nil {
		dst.MinP = src.MinP
	}
	if src.RepeatPenalty != nil {
		dst.RepeatPenalty = src.RepeatPenalty
	}
	if src.PresencePenalty != nil {
		dst.PresencePenalty = src.PresencePenalty
	}
	if src.FrequencyPenalty != nil {
		dst.FrequencyPenalty = src.FrequencyPenalty
	}
	if src.TypicalP != nil {
		dst.TypicalP = src.TypicalP
	}
	if src.Mirostat != nil {
		dst.Mirostat = src.Mirostat
	}
	if src.Seed != nil {
		dst.Seed = src.Seed
	}
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefBool(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
}

// FormatKB is a small helper for config/doctor output.
func FormatKB(kb uint64) string {
	return strconv.FormatUint(kb, 10) + " kB"
}
