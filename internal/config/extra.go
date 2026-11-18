package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// extraFlagName is the accepted syntax for a new extra flag. llama-server
// options use dashes, but underscores are accepted because config keys commonly
// use them. Dots are rejected because they would create nested TOML keys.
var extraFlagName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// NormalizeExtraFlag strips leading dashes and validates a user-supplied flag
// name. The stored form is used for TOML keys, provenance fields and CLI flags.
func NormalizeExtraFlag(name string) (string, error) {
	flag := normalizeExtraKey(name)
	if flag == "" {
		return "", fmt.Errorf("flag name cannot be empty")
	}
	if !extraFlagName.MatchString(flag) {
		return "", fmt.Errorf("invalid flag name %q (use letters, digits, '_' and '-')", flag)
	}
	return flag, nil
}

// normalizeExtraKey is the lossy form used for existing config keys. It never
// fails: an unusual imported key stays visible instead of breaking a launch.
func normalizeExtraKey(name string) string {
	return strings.TrimLeft(strings.TrimSpace(name), "-")
}

// NormalizeExtraMap returns a copy of raw extras keyed by normalized flag name.
// Raw keys are sorted so layers containing both "--flag" and "flag" merge
// deterministically.
func NormalizeExtraMap(raw map[string]string) map[string]string {
	if len(raw) == 0 {
		return map[string]string{}
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		flag := normalizeExtraKey(k)
		if flag != "" {
			out[flag] = raw[k]
		}
	}
	return out
}

// ExtraFieldName is the provenance field name for one extra flag.
func ExtraFieldName(flag string) string {
	return "extra." + normalizeExtraKey(flag)
}

// ExtraFlagFromField reverses ExtraFieldName. It returns false for other
// launch-option fields.
func ExtraFlagFromField(field string) (string, bool) {
	flag, ok := strings.CutPrefix(field, "extra.")
	if !ok || flag == "" {
		return "", false
	}
	return normalizeExtraKey(flag), true
}

// ExtraFlags returns normalized extra flag names in stable display order.
func ExtraFlags(extra map[string]string) []string {
	normalized := NormalizeExtraMap(extra)
	flags := make([]string, 0, len(normalized))
	for flag := range normalized {
		flags = append(flags, flag)
	}
	sort.Strings(flags)
	return flags
}

// ExtraFieldNames returns provenance field names for extras in display order.
func ExtraFieldNames(extra map[string]string) []string {
	flags := ExtraFlags(extra)
	fields := make([]string, 0, len(flags))
	for _, flag := range flags {
		fields = append(fields, ExtraFieldName(flag))
	}
	return fields
}

// FieldNames returns canonical option fields followed by resolved extras.
func FieldNames(r Resolved) []string {
	fields := OptionFields()
	return append(fields, ExtraFieldNames(r.Extra)...)
}

// firstClassFlags maps an extra flag spelling to the launch option it
// duplicates. Extras are appended last, so an explicit flag wins.
var firstClassFlags = map[string]string{
	"model":             "model",
	"host":              "host",
	"port":              "port",
	"alias":             "alias",
	"c":                 "ctx",
	"ctx":               "ctx",
	"ctx-size":          "ctx",
	"gpu_layers":        "gpu_layers",
	"gpu-layers":        "gpu_layers",
	"n_cpu_moe":         "n_cpu_moe",
	"n-cpu-moe":         "n_cpu_moe",
	"threads":           "threads",
	"threads_batch":     "threads_batch",
	"threads-batch":     "threads_batch",
	"batch_size":        "batch_size",
	"batch-size":        "batch_size",
	"ubatch_size":       "ubatch_size",
	"ubatch-size":       "ubatch_size",
	"parallel":          "parallel",
	"flash_attn":        "flash_attn",
	"flash-attn":        "flash_attn",
	"cache_type_k":      "cache_type_k",
	"cache-type-k":      "cache_type_k",
	"cache_type_v":      "cache_type_v",
	"cache-type-v":      "cache_type_v",
	"jinja":             "jinja",
	"no-jinja":          "jinja",
	"metrics":           "metrics",
	"temp":              "sampling.temp",
	"top_p":             "sampling.top_p",
	"top-p":             "sampling.top_p",
	"top_k":             "sampling.top_k",
	"top-k":             "sampling.top_k",
	"min_p":             "sampling.min_p",
	"min-p":             "sampling.min_p",
	"repeat_penalty":    "sampling.repeat_penalty",
	"repeat-penalty":    "sampling.repeat_penalty",
	"presence_penalty":  "sampling.presence_penalty",
	"presence-penalty":  "sampling.presence_penalty",
	"frequency_penalty": "sampling.frequency_penalty",
	"frequency-penalty": "sampling.frequency_penalty",
	"typical_p":         "sampling.typical_p",
	"typical-p":         "sampling.typical_p",
	"mirostat":          "sampling.mirostat",
	"seed":              "sampling.seed",
}

// ExtraConflict names a first-class launch option duplicated by an extra flag.
type ExtraConflict struct {
	Flag  string `json:"flag"`
	Field string `json:"field"`
}

// Warning is a short explanation suitable for CLI and TUI output.
func (c ExtraConflict) Warning() string {
	return fmt.Sprintf("extra %q duplicates first-class option %q; extras are appended last and win", c.Flag, c.Field)
}

// ConflictingField returns the first-class option duplicated by flag, if any.
func ConflictingField(flag string) string {
	return firstClassFlags[normalizeExtraKey(flag)]
}

// ExtraConflicts returns duplicates in flag order.
func ExtraConflicts(extra map[string]string) []ExtraConflict {
	var conflicts []ExtraConflict
	for _, flag := range ExtraFlags(extra) {
		if field := ConflictingField(flag); field != "" {
			conflicts = append(conflicts, ExtraConflict{Flag: flag, Field: field})
		}
	}
	return conflicts
}

// ExtraConflictWarnings renders ExtraConflicts as warnings.
func ExtraConflictWarnings(extra map[string]string) []string {
	conflicts := ExtraConflicts(extra)
	warnings := make([]string, 0, len(conflicts))
	for _, c := range conflicts {
		warnings = append(warnings, c.Warning())
	}
	return warnings
}

// DisplayFieldValue renders a resolved value for human-readable output. Bare
// extras are shown explicitly while JSON keeps the empty string.
func DisplayFieldValue(field string, value any) string {
	if _, ok := ExtraFlagFromField(field); ok {
		if s, ok := value.(string); ok && s == "" {
			return `"" (bare)`
		}
	}
	return fmt.Sprint(value)
}
