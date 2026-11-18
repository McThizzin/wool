// Package flags holds the embedded llama-server flag catalog, a conservative
// help-text parser, fuzzy search, and advisory checks for extra flags.
package flags

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed data/catalog.json
var catalogJSON []byte

// Value shapes describe how a flag is written on the command line. "none" is a
// bare flag; every other shape expects a value.
const (
	ValueNone   = "none"
	ValueBool   = "bool"
	ValueInt    = "int"
	ValueFloat  = "float"
	ValueString = "string"
	ValuePath   = "path"
)

// CatalogSource records the llama.cpp revision the catalog was generated from.
type CatalogSource struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// Flag is one llama-server command-line option. The source version and commit
// are repeated on each entry so JSON consumers do not need catalog metadata.
type Flag struct {
	Name        string        `json:"name"`
	Aliases     []string      `json:"aliases"`
	Value       string        `json:"value"`
	Description string        `json:"description"`
	Default     string        `json:"default,omitempty"`
	Env         string        `json:"env,omitempty"`
	Category    string        `json:"category"`
	Deprecated  bool          `json:"deprecated,omitempty"`
	Replacement string        `json:"replacement,omitempty"`
	Source      CatalogSource `json:"source"`
}

// Catalog is the generated flag reference.
type Catalog struct {
	Source CatalogSource `json:"source"`
	Flags  []Flag        `json:"flags"`
}

var (
	embeddedOnce sync.Once
	embedded     *Catalog
	embeddedErr  error
)

// Embedded returns the catalog compiled into wool. The returned value is a
// copy; callers may sort or filter it without affecting later callers.
func Embedded() (*Catalog, error) {
	embeddedOnce.Do(func() {
		c, err := Decode(catalogJSON)
		if err != nil {
			embeddedErr = err
			return
		}
		embedded = c
	})
	if embeddedErr != nil {
		return nil, embeddedErr
	}
	copy := *embedded
	copy.Flags = append([]Flag(nil), embedded.Flags...)
	for i := range copy.Flags {
		copy.Flags[i].Aliases = append(make([]string, 0, len(embedded.Flags[i].Aliases)), embedded.Flags[i].Aliases...)
	}
	return &copy, nil
}

// Decode parses and validates a catalog document.
func Decode(data []byte) (*Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("decode flag catalog: %w", err)
	}
	if c.Source.Version == "" || c.Source.Commit == "" {
		return nil, fmt.Errorf("flag catalog is missing source version or commit")
	}
	if len(c.Flags) == 0 {
		return nil, fmt.Errorf("flag catalog is empty")
	}
	seen := make(map[string]bool, len(c.Flags))
	for i := range c.Flags {
		f := &c.Flags[i]
		f.Name = Normalize(f.Name)
		if f.Name == "" {
			return nil, fmt.Errorf("flag catalog entry %d has no name", i)
		}
		if seen[f.Name] {
			return nil, fmt.Errorf("duplicate flag %q in catalog", f.Name)
		}
		seen[f.Name] = true
		if f.Value == "" {
			return nil, fmt.Errorf("flag %q has no value shape", f.Name)
		}
		if f.Description == "" {
			return nil, fmt.Errorf("flag %q has no description", f.Name)
		}
		if f.Category == "" {
			return nil, fmt.Errorf("flag %q has no category", f.Name)
		}
		if f.Source.Version == "" {
			f.Source.Version = c.Source.Version
		}
		if f.Source.Commit == "" {
			f.Source.Commit = c.Source.Commit
		}
		f.Aliases = normalizeAliases(f.Name, f.Aliases)
	}
	Sort(c.Flags)
	return &c, nil
}

// Encode renders a catalog as deterministic JSON.
func Encode(c *Catalog) ([]byte, error) {
	copy := *c
	copy.Flags = append([]Flag(nil), c.Flags...)
	Sort(copy.Flags)
	b, err := json.MarshalIndent(&copy, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Sort orders flags by canonical name.
func Sort(flags []Flag) {
	sort.Slice(flags, func(i, j int) bool { return flags[i].Name < flags[j].Name })
}

// Normalize removes leading dashes and surrounding whitespace from a flag
// spelling. It never rejects a name; validation happens in Decode.
func Normalize(name string) string {
	return strings.TrimLeft(strings.TrimSpace(name), "-")
}

func normalizeAliases(name string, aliases []string) []string {
	seen := map[string]bool{name: true}
	out := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		a := Normalize(alias)
		if a == "" || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// Lookup finds a flag by canonical name or alias.
func (c *Catalog) Lookup(name string) (Flag, bool) {
	needle := Normalize(name)
	if needle == "" {
		return Flag{}, false
	}
	for _, f := range c.Flags {
		if f.Name == needle {
			return f, true
		}
		for _, alias := range f.Aliases {
			if alias == needle {
				return f, true
			}
		}
	}
	return Flag{}, false
}

// Names returns canonical names in sorted order.
func (c *Catalog) Names() []string {
	out := make([]string, 0, len(c.Flags))
	for _, f := range c.Flags {
		out = append(out, f.Name)
	}
	return out
}

// NeedsValue reports whether selecting the flag should prompt for a value.
func (f Flag) NeedsValue() bool { return f.Value != ValueNone }

// Usage renders a compact command-line form for the flag reference.
func (f Flag) Usage() string {
	if !f.NeedsValue() {
		return "--" + f.Name
	}
	placeholder := map[string]string{
		ValueBool: "true|false", ValueInt: "N", ValueFloat: "FLOAT",
		ValueString: "VALUE", ValuePath: "FILE",
	}[f.Value]
	return "--" + f.Name + " " + placeholder
}
