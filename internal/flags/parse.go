package flags

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	optionNames   = regexp.MustCompile(`--?[A-Za-z0-9][A-Za-z0-9-]*`)
	placeholderRE = regexp.MustCompile(`(?:<([^>]+)>|\[([^\]]+)\]|\b([A-Z][A-Z0-9_.-]*)\b)`)
	defaultRE     = regexp.MustCompile(`(?i)\(default:\s*([^)]*)\)`)
	envRE         = regexp.MustCompile(`\b(LLAMA_ARG_[A-Z0-9_]+)\b`)
	envPhraseRE   = regexp.MustCompile(`(?i)[,;\s]*(?:env(?:ironment variable)?:\s*|environment variable\s+)LLAMA_ARG_[A-Z0-9_]+`)
	spacesRE      = regexp.MustCompile(`\s+`)
	versionRE     = regexp.MustCompile(`\b(b\d{3,})\b`)
)

// ParseHelp reads the option list from llama-server --help output. It is
// deliberately conservative: unrecognised lines are ignored rather than
// guessed, and every parsed entry is filled with a value shape and category
// that curated overrides can correct.
func ParseHelp(text string, source CatalogSource) (*Catalog, error) {
	if source.Version == "" {
		source.Version = findVersion(text)
	}
	c := &Catalog{Source: source}
	seen := make(map[string]bool)
	for _, line := range strings.Split(text, "\n") {
		flag, ok := parseHelpLine(line)
		if !ok || seen[flag.Name] {
			continue
		}
		if source.Version != "" {
			flag.Source.Version = source.Version
		}
		if source.Commit != "" {
			flag.Source.Commit = source.Commit
		}
		seen[flag.Name] = true
		c.Flags = append(c.Flags, flag)
	}
	if len(c.Flags) == 0 {
		return nil, fmt.Errorf("no llama-server options found in help text")
	}
	Sort(c.Flags)
	return c, nil
}

// DetectVersion extracts a llama.cpp build tag such as b6000 from help or
// version output. It returns an empty string when none is present.
func DetectVersion(text string) string {
	return findVersion(text)
}

func findVersion(text string) string {
	head := text
	if lines := strings.Split(text, "\n"); len(lines) > 8 {
		head = strings.Join(lines[:8], "\n")
	}
	if m := versionRE.FindString(head); m != "" {
		return m
	}
	return ""
}

func parseHelpLine(line string) (Flag, bool) {
	trimmed := strings.TrimRight(line, " \t\r")
	indent := len(trimmed) - len(strings.TrimLeft(trimmed, " \t"))
	if indent < 2 {
		return Flag{}, false
	}
	body := strings.TrimLeft(trimmed, " \t")
	parts := spacesRE.Split(body, 2)
	if len(parts) != 2 {
		return Flag{}, false
	}
	optionPart, rawDescription := parts[0], parts[1]
	if !strings.Contains(body, "  ") {
		return Flag{}, false
	}
	// The split above collapses all whitespace, so recover the two-column
	// boundary from the original body to keep multi-word placeholders intact.
	boundary := twoColumnSplit(body)
	if boundary < 0 {
		return Flag{}, false
	}
	optionPart = strings.TrimSpace(body[:boundary])
	rawDescription = strings.TrimSpace(body[boundary:])
	spellings := optionNames.FindAllString(optionPart, -1)
	if len(spellings) == 0 {
		return Flag{}, false
	}

	name, aliases := "", []string{}
	for _, spelling := range spellings {
		n := Normalize(spelling)
		if n == "" {
			continue
		}
		if name == "" && strings.HasPrefix(spelling, "--") {
			name = n
			continue
		}
		aliases = append(aliases, n)
	}
	if name == "" {
		name = Normalize(spellings[0])
		aliases = aliases[1:]
	}
	if name == "" {
		return Flag{}, false
	}

	placeholder := ""
	if m := placeholderRE.FindAllStringSubmatch(optionPart, -1); len(m) > 0 {
		for _, group := range m {
			for _, candidate := range group[1:] {
				if candidate != "" {
					placeholder = candidate
					break
				}
			}
			if placeholder != "" {
				break
			}
		}
	}

	defaultValue := ""
	if m := defaultRE.FindStringSubmatch(rawDescription); len(m) == 2 {
		defaultValue = strings.TrimSpace(m[1])
	}
	env := ""
	if m := envRE.FindString(rawDescription); m != "" {
		env = m
	}
	description := cleanDescription(rawDescription)
	description = stripDescriptionMetadata(description)

	return Flag{
		Name:        name,
		Aliases:     normalizeAliases(name, aliases),
		Value:       inferValue(placeholder, description),
		Description: description,
		Default:     defaultValue,
		Env:         env,
		Category:    inferCategory(name),
	}, true
}

// twoColumnSplit finds the first run of two or more spaces or a tab in a help
// line. Option names and their placeholders use a single space.
func twoColumnSplit(s string) int {
	runes := []rune(s)
	for i := 0; i < len(runes)-1; i++ {
		if runes[i] == '\t' || (runes[i] == ' ' && runes[i+1] == ' ') {
			return i
		}
	}
	return -1
}

func cleanDescription(s string) string {
	return strings.TrimSpace(spacesRE.ReplaceAllString(s, " "))
}

func stripDescriptionMetadata(s string) string {
	s = defaultRE.ReplaceAllString(s, "")
	s = envPhraseRE.ReplaceAllString(s, "")
	s = strings.TrimSpace(spacesRE.ReplaceAllString(s, " "))
	s = strings.TrimRight(s, " ,;")
	if s == "" {
		return "llama-server option"
	}
	return s
}

func inferValue(placeholder, description string) string {
	if placeholder == "" {
		if strings.Contains(strings.ToLower(description), "deprecated") {
			return ValueNone
		}
		return ValueNone
	}
	p := strings.ToUpper(placeholder)
	switch {
	case strings.Contains(p, "FILE"), strings.Contains(p, "FNAME"), strings.Contains(p, "PATH"):
		return ValuePath
	case strings.Contains(p, "FLOAT"), strings.Contains(p, "REAL"):
		return ValueFloat
	case p == "BOOL", p == "TRUEFALSE":
		return ValueBool
	case p == "N", p == "INT", p == "NUM", p == "SEED", strings.Contains(p, "SIZE"),
		strings.Contains(p, "LAYERS"), strings.Contains(p, "COUNT"), strings.Contains(p, "MAX"):
		return ValueInt
	default:
		return ValueString
	}
}

func inferCategory(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "models-"), strings.Contains(lower, "router"), strings.Contains(lower, "fit"),
		strings.Contains(lower, "sleep"), strings.Contains(lower, "warm"), lower == "models-preset":
		return "router"
	case strings.Contains(lower, "model"), lower == "alias", strings.Contains(lower, "path"):
		return "model"
	case strings.Contains(lower, "ctx"), strings.Contains(lower, "context"):
		return "context"
	case strings.Contains(lower, "gpu"), strings.Contains(lower, "cpu-moe"), strings.Contains(lower, "offload"):
		return "offload"
	case strings.Contains(lower, "thread"), strings.Contains(lower, "batch"), strings.Contains(lower, "parallel"):
		return "performance"
	case strings.Contains(lower, "cache"), strings.Contains(lower, "flash"), strings.Contains(lower, "kv"):
		return "cache"
	case strings.Contains(lower, "temp"), strings.Contains(lower, "top-"), strings.Contains(lower, "min-p"),
		strings.Contains(lower, "penalty"), strings.Contains(lower, "mirostat"), strings.Contains(lower, "seed"),
		strings.Contains(lower, "typical"):
		return "sampling"
	case strings.Contains(lower, "jinja"), strings.Contains(lower, "prompt"), strings.Contains(lower, "template"):
		return "templating"
	case strings.Contains(lower, "host"), strings.Contains(lower, "port"), strings.Contains(lower, "api"),
		strings.Contains(lower, "endpoint"), strings.Contains(lower, "metrics"), strings.Contains(lower, "timeout"):
		return "server"
	case strings.Contains(lower, "log"), strings.Contains(lower, "verbose"):
		return "logging"
	default:
		return "general"
	}
}
