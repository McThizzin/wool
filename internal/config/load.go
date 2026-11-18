package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"wool/internal/theme"
)

// ParseOptions controls how a config document is decoded. Strict mode rejects
// keys that do not match the schema; map fields such as [*.extra] and
// [themes.*.colors] accept arbitrary keys in both modes.
type ParseOptions struct {
	Strict bool
}

func Load(path string) (*Config, error) {
	return LoadWithOptions(path, ParseOptions{})
}

// LoadWithOptions reads and decodes a config file with the given options.
func LoadWithOptions(path string, opts ParseOptions) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseWithOptions(b, path, opts)
}

func Parse(b []byte, source string) (*Config, error) {
	return ParseWithOptions(b, source, ParseOptions{})
}

// ParseWithOptions decodes and validates a config document. Decode errors are
// returned as *Error or Errors with file, line, column and key information.
func ParseWithOptions(b []byte, source string, opts ParseOptions) (*Config, error) {
	var cfg Config
	dec := toml.NewDecoder(bytes.NewReader(b))
	if opts.Strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(&cfg); err != nil {
		return nil, structuredDecodeError(source, err)
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, &Error{File: source, Message: err.Error(), Err: err}
	}
	return &cfg, nil
}

// ApplyDefaults fills unset application-level values. Launch defaults are a
// separate layer applied during resolution, so an explicit [defaults] key
// stays distinguishable from a built-in value.
func (c *Config) Validate() error {
	var errs []error
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		errs = append(errs, fmt.Errorf("server.port must be between 1 and 65535"))
	}
	if d, err := time.ParseDuration(c.Server.SmokeTimeout); err != nil || d <= 0 {
		errs = append(errs, fmt.Errorf("server.smoke_timeout must be a positive duration"))
	}
	switch c.Server.Restart {
	case "never", "on-failure", "always":
	default:
		errs = append(errs, fmt.Errorf("server.restart must be never, on-failure or always"))
	}
	if c.Server.RestartMax != nil && *c.Server.RestartMax < 0 {
		errs = append(errs, fmt.Errorf("server.restart_max cannot be negative"))
	}
	if d, err := time.ParseDuration(c.Server.RestartWindow); err != nil || d <= 0 {
		errs = append(errs, fmt.Errorf("server.restart_window must be a positive duration"))
	}
	if d, err := time.ParseDuration(c.Server.RestartBackoff); err != nil || d <= 0 {
		errs = append(errs, fmt.Errorf("server.restart_backoff must be a positive duration"))
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
	for id, g := range c.Groups {
		if err := validateID("groups."+id, id); err != nil {
			errs = append(errs, err)
		}
		if len(g.Models) == 0 {
			errs = append(errs, fmt.Errorf("groups.%s.models is empty", id))
			continue
		}
		seen := make(map[string]bool, len(g.Models))
		for _, model := range g.Models {
			if strings.TrimSpace(model) == "" {
				errs = append(errs, fmt.Errorf("groups.%s.models contains an empty model ID", id))
				continue
			}
			if _, ok := c.Models[model]; !ok {
				errs = append(errs, fmt.Errorf("groups.%s references unknown model %q", id, model))
				continue
			}
			if seen[model] {
				errs = append(errs, fmt.Errorf("groups.%s lists model %q more than once", id, model))
			}
			seen[model] = true
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
	if c.Alerts.TempC < 0 || c.Alerts.TempC > 150 {
		errs = append(errs, fmt.Errorf("alerts.temp_c must be between 0 and 150"))
	}
	if c.Alerts.VRAMPercent < 0 || c.Alerts.VRAMPercent > 100 {
		errs = append(errs, fmt.Errorf("alerts.vram_percent must be between 0 and 100"))
	}
	if d, err := time.ParseDuration(c.Alerts.Cooldown); err != nil || d <= 0 {
		errs = append(errs, fmt.Errorf("alerts.cooldown must be a positive duration"))
	}
	if c.Alerts.Webhook != "" {
		u, err := url.Parse(c.Alerts.Webhook)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("alerts.webhook must be an http or https URL"))
		}
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
