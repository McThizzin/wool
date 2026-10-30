package theme

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Theme is a small palette. Everything in the UI is derived from it.
type Theme struct {
	Name                                                      string
	Border, Accent, Text, Dim, BadgeFg                        lipgloss.Color
	Red, Yellow, Green, Cyan, Blue, Mauve, Pink, Orange, Teal lipgloss.Color
}

// Definition is a custom theme from config.toml. Base names a built-in or
// another custom theme; Colors overrides individual palette entries.
type Definition struct {
	Base   string            `toml:"base"`
	Colors map[string]string `toml:"colors"`
}

var builtinOrder = []string{"mocha", "tokyo", "gruvbox", "nord"}

var builtinThemes = map[string]Theme{
	"mocha": { // Catppuccin Mocha
		Name: "mocha", Border: "#6c7086", Accent: "#89b4fa", Text: "#cdd6f4", Dim: "#7f849c", BadgeFg: "#1e1e2e",
		Red: "#f38ba8", Yellow: "#f9e2af", Green: "#a6e3a1", Cyan: "#89dceb", Blue: "#89b4fa",
		Mauve: "#cba6f7", Pink: "#f5c2e7", Orange: "#fab387", Teal: "#94e2d5",
	},
	"tokyo": { // Tokyo Night
		Name: "tokyo", Border: "#3b4261", Accent: "#7aa2f7", Text: "#c0caf5", Dim: "#565f89", BadgeFg: "#1a1b26",
		Red: "#f7768e", Yellow: "#e0af68", Green: "#9ece6a", Cyan: "#7dcfff", Blue: "#7aa2f7",
		Mauve: "#bb9af7", Pink: "#ff9ac1", Orange: "#ff9e64", Teal: "#73daca",
	},
	"gruvbox": {
		Name: "gruvbox", Border: "#665c54", Accent: "#fabd2f", Text: "#ebdbb2", Dim: "#928374", BadgeFg: "#282828",
		Red: "#fb4934", Yellow: "#fabd2f", Green: "#b8bb26", Cyan: "#8ec07c", Blue: "#83a598",
		Mauve: "#d3869b", Pink: "#d3869b", Orange: "#fe8019", Teal: "#8ec07c",
	},
	"nord": {
		Name: "nord", Border: "#4c566a", Accent: "#88c0d0", Text: "#eceff4", Dim: "#616e88", BadgeFg: "#2e3440",
		Red: "#bf616a", Yellow: "#ebcb8b", Green: "#a3be8c", Cyan: "#8fbcbb", Blue: "#81a1c1",
		Mauve: "#b48ead", Pink: "#b48ead", Orange: "#d08770", Teal: "#8fbcbb",
	},
}

var order = append([]string(nil), builtinOrder...)

var themes = cloneThemes(builtinThemes)

func cloneThemes(src map[string]Theme) map[string]Theme {
	dst := make(map[string]Theme, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// Order returns built-in themes followed by custom themes in name order.
func Order() []string { return append([]string(nil), order...) }

func Get(name string) (Theme, bool) {
	t, ok := themes[strings.ToLower(strings.TrimSpace(name))]
	return t, ok
}

type colorSetter func(*Theme, lipgloss.Color)

var colorSetters = map[string]colorSetter{
	"border":   func(t *Theme, c lipgloss.Color) { t.Border = c },
	"accent":   func(t *Theme, c lipgloss.Color) { t.Accent = c },
	"text":     func(t *Theme, c lipgloss.Color) { t.Text = c },
	"dim":      func(t *Theme, c lipgloss.Color) { t.Dim = c },
	"badge_fg": func(t *Theme, c lipgloss.Color) { t.BadgeFg = c },
	"red":      func(t *Theme, c lipgloss.Color) { t.Red = c },
	"yellow":   func(t *Theme, c lipgloss.Color) { t.Yellow = c },
	"green":    func(t *Theme, c lipgloss.Color) { t.Green = c },
	"cyan":     func(t *Theme, c lipgloss.Color) { t.Cyan = c },
	"blue":     func(t *Theme, c lipgloss.Color) { t.Blue = c },
	"mauve":    func(t *Theme, c lipgloss.Color) { t.Mauve = c },
	"pink":     func(t *Theme, c lipgloss.Color) { t.Pink = c },
	"orange":   func(t *Theme, c lipgloss.Color) { t.Orange = c },
	"teal":     func(t *Theme, c lipgloss.Color) { t.Teal = c },
}

func normalizeColorKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "-", "_")
	if key == "badge" {
		return "badge_fg"
	}
	return key
}

// ValidateAll checks custom theme definitions without changing the registry.
func ValidateAll(defs map[string]Definition) error {
	_, _, err := resolveAll(defs)
	return err
}

// RegisterAll replaces the custom themes with defs. Built-in themes are always
// available. It is safe to call repeatedly, for example after a config reload.
func RegisterAll(defs map[string]Definition) error {
	resolved, custom, err := resolveAll(defs)
	if err != nil {
		return err
	}
	themes = resolved
	order = append(append([]string(nil), builtinOrder...), custom...)
	return nil
}

// resolveAll merges definitions in dependency order. A custom theme may use
// another custom theme as its base as long as there is no cycle.
func resolveAll(defs map[string]Definition) (map[string]Theme, []string, error) {
	resolved := cloneThemes(builtinThemes)
	pending := make(map[string]Definition, len(defs))
	for rawName, def := range defs {
		name := strings.ToLower(strings.TrimSpace(rawName))
		if name == "" {
			return nil, nil, fmt.Errorf("themes: empty theme name")
		}
		if _, ok := builtinThemes[name]; ok {
			return nil, nil, fmt.Errorf("themes.%s: cannot override a built-in theme", rawName)
		}
		if _, ok := pending[name]; ok {
			return nil, nil, fmt.Errorf("themes.%s: duplicate theme name", rawName)
		}
		pending[name] = def
	}

	var custom []string
	for len(pending) > 0 {
		progress := false
		for _, name := range sortedNames(pending) {
			def := pending[name]
			baseName := strings.ToLower(strings.TrimSpace(def.Base))
			if baseName == "" {
				baseName = "mocha"
			}
			base, ok := resolved[baseName]
			if !ok {
				continue
			}
			next := base
			next.Name = name
			if err := applyColors(&next, name, def.Colors); err != nil {
				return nil, nil, err
			}
			resolved[name] = next
			custom = append(custom, name)
			delete(pending, name)
			progress = true
		}
		if progress {
			continue
		}

		name := sortedNames(pending)[0]
		base := strings.ToLower(strings.TrimSpace(pending[name].Base))
		if base == "" {
			base = "mocha"
		}
		if _, ok := pending[base]; ok {
			return nil, nil, fmt.Errorf("themes.%s.base: cycle involving %q", name, base)
		}
		return nil, nil, fmt.Errorf("themes.%s.base: unknown theme %q", name, base)
	}
	sort.Strings(custom)
	return resolved, custom, nil
}

func sortedNames(defs map[string]Definition) []string {
	names := make([]string, 0, len(defs))
	for name := range defs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func applyColors(t *Theme, name string, colors map[string]string) error {
	keys := make([]string, 0, len(colors))
	for key := range colors {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		set, ok := colorSetters[normalizeColorKey(key)]
		if !ok {
			return fmt.Errorf("themes.%s.colors.%s: unknown color key", name, key)
		}
		value := strings.TrimSpace(colors[key])
		if !validColor(value) {
			return fmt.Errorf("themes.%s.colors.%s: invalid color %q (use #rrggbb or 0-255)", name, key, value)
		}
		set(t, lipgloss.Color(value))
	}
	return nil
}

func validColor(value string) bool {
	if value == "" {
		return false
	}
	if strings.HasPrefix(value, "#") {
		hex := value[1:]
		if len(hex) != 3 && len(hex) != 6 && len(hex) != 8 {
			return false
		}
		for _, r := range hex {
			switch {
			case r >= '0' && r <= '9':
			case r >= 'a' && r <= 'f':
			case r >= 'A' && r <= 'F':
			default:
				return false
			}
		}
		return true
	}
	n, err := strconv.Atoi(value)
	return err == nil && n >= 0 && n <= 255
}

// Color maps a user-facing color name to a theme color.
func (t Theme) Color(name string) (lipgloss.Color, bool) {
	switch strings.ToLower(name) {
	case "red":
		return t.Red, true
	case "yellow":
		return t.Yellow, true
	case "green":
		return t.Green, true
	case "cyan":
		return t.Cyan, true
	case "blue":
		return t.Blue, true
	case "mauve", "purple":
		return t.Mauve, true
	case "pink":
		return t.Pink, true
	case "orange":
		return t.Orange, true
	case "teal":
		return t.Teal, true
	}
	return "", false
}

// Styles are the reusable text styles for the current theme.
type Styles struct {
	Dim, App, Tx, Alert, Plain, Accent, Text lipgloss.Style
	Title, Header, Muted, Good, Warn, Bad    lipgloss.Style
	Selected, ChipText, Border               lipgloss.Style
}

func NewStyles(t Theme) Styles {
	fg := func(c lipgloss.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }
	return Styles{
		Dim:      fg(t.Dim),
		App:      fg(t.Mauve),
		Tx:       fg(t.Blue),
		Alert:    fg(t.Red).Bold(true),
		Plain:    fg(t.Text),
		Accent:   fg(t.Accent).Bold(true),
		Text:     fg(t.Text),
		Title:    fg(t.Text).Bold(true),
		Header:   fg(t.Mauve).Bold(true),
		Muted:    fg(t.Dim),
		Good:     fg(t.Green),
		Warn:     fg(t.Yellow),
		Bad:      fg(t.Red),
		Selected: lipgloss.NewStyle().Foreground(t.BadgeFg).Background(t.Accent).Bold(true),
		ChipText: lipgloss.NewStyle().Foreground(t.BadgeFg).Bold(true),
		Border:   fg(t.Border),
	}
}

// Icons: plain by default, Nerd Font glyphs with nerd_fonts = true.
type Icons struct {
	Server, Model, Preset, Router, Logs, Settings, Hardware, Activity, Help string
	Input, Output, Command                                                  string
}

var ASCII = Icons{}

var Nerd = Icons{
	Server:   "\uf233",
	Model:    "\uf085",
	Preset:   "\uf0c7",
	Router:   "\uf0e8",
	Logs:     "\uf120",
	Settings: "\uf013",
	Hardware: "\uf2db",
	Activity: "\uf0f1",
	Help:     "\uf059",
	Input:    "\uf11c",
	Output:   "\uf120",
	Command:  "\uf120",
}

func Titled(icon, name string) string {
	if icon == "" {
		return name
	}
	return icon + " " + name
}
