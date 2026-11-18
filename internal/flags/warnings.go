package flags

import (
	"errors"
	"fmt"
	"sort"

	"wool/internal/config"
)

// Warning is an advisory problem with an extra flag. Unknown flags never
// prevent a launch; strict checks can promote them to errors.
type Warning struct {
	Flag    string `json:"flag"`
	Kind    string `json:"kind"` // unknown, deprecated or conflict
	Layer   string `json:"layer,omitempty"`
	Message string `json:"message"`
}

// CheckExtras reports catalog and first-class conflicts for resolved extras.
// Results are sorted by flag name and then kind, independent of map order.
func CheckExtras(extra map[string]string, c *Catalog) []Warning {
	return checkExtrasLayer(extra, "", c)
}

// ConfigWarnings checks every extra layer in a config, including defaults,
// models and presets. Duplicate warnings are collapsed by flag and kind.
func ConfigWarnings(cfg *config.Config, c *Catalog) []Warning {
	seen := make(map[string]bool)
	var out []Warning
	addLayer := func(extra map[string]string, layer string) {
		for _, w := range checkExtrasLayer(extra, layer, c) {
			key := w.Flag + "\x00" + w.Kind + "\x00" + w.Layer
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, w)
		}
	}
	addLayer(cfg.Defaults.Extra, "defaults")
	for _, id := range cfg.ModelIDs() {
		addLayer(cfg.Models[id].Options.Extra, "models."+id)
	}
	for _, id := range cfg.PresetIDs() {
		addLayer(cfg.Presets[id].Options.Extra, "presets."+id)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Flag != out[j].Flag {
			return out[i].Flag < out[j].Flag
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Layer < out[j].Layer
	})
	return out
}

func checkExtrasLayer(extra map[string]string, layer string, c *Catalog) []Warning {
	var out []Warning
	seen := make(map[string]bool)
	add := func(w Warning) {
		key := w.Flag + "\x00" + w.Kind
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, w)
	}
	for flag := range config.NormalizeExtraMap(extra) {
		f, ok := c.Lookup(flag)
		switch {
		case !ok:
			add(Warning{Flag: flag, Kind: "unknown", Layer: layer, Message: fmt.Sprintf("extra %q is not in the llama-server flag catalog", flag)})
		case f.Deprecated:
			msg := fmt.Sprintf("extra %q is deprecated", flag)
			if f.Replacement != "" {
				msg += "; use " + f.Replacement
			}
			add(Warning{Flag: flag, Kind: "deprecated", Layer: layer, Message: msg})
		}
	}
	for _, conflict := range config.ExtraConflicts(extra) {
		add(Warning{Flag: conflict.Flag, Kind: "conflict", Layer: layer, Message: conflict.Warning()})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Flag != out[j].Flag {
			return out[i].Flag < out[j].Flag
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// WarningMessages renders CheckExtras for CLI stderr and TUI toasts.
func WarningMessages(warnings []Warning) []string {
	out := make([]string, 0, len(warnings))
	for _, w := range warnings {
		msg := w.Message
		if w.Layer != "" {
			msg = w.Layer + ": " + msg
		}
		out = append(out, msg)
	}
	return out
}

// StrictError returns an error when strict checking is requested and an extra
// is unknown or deprecated. Conflicts stay advisory because replacing a
// generated option is an explicit user choice.
func StrictError(warnings []Warning) error {
	var errs []error
	for _, w := range warnings {
		switch w.Kind {
		case "unknown", "deprecated":
			errs = append(errs, fmt.Errorf("%s", w.Message))
		}
	}
	return errors.Join(errs...)
}
