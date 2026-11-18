package flags

import (
	"reflect"
	"sort"
	"strings"
)

// Change describes an existing flag whose metadata differs between the
// installed binary and the embedded catalog.
type Change struct {
	Name   string   `json:"name"`
	Fields []string `json:"fields"`
}

// CheckResult is the stable output of comparing an installed help snapshot
// with the embedded catalog.
type CheckResult struct {
	CatalogSource   CatalogSource `json:"catalog_source"`
	InstalledSource CatalogSource `json:"installed_source"`
	New             []string      `json:"new"`
	Missing         []string      `json:"missing"`
	Changed         []Change      `json:"changed"`
	OK              bool          `json:"ok"`
}

// Compare reports flags present only in the installed help (new), only in the
// embedded catalog (missing), and metadata differences for shared flags.
func Compare(installed, catalog *Catalog) CheckResult {
	installedFlags := make(map[string]Flag, len(installed.Flags))
	installedNames := make([]string, 0, len(installed.Flags))
	for _, f := range installed.Flags {
		name := Normalize(f.Name)
		installedFlags[name] = f
		installedNames = append(installedNames, name)
	}
	catalogFlags := make(map[string]Flag, len(catalog.Flags))
	catalogNames := make([]string, 0, len(catalog.Flags))
	for _, f := range catalog.Flags {
		name := Normalize(f.Name)
		catalogFlags[name] = f
		catalogNames = append(catalogNames, name)
	}

	result := CheckResult{
		CatalogSource:   catalog.Source,
		InstalledSource: installed.Source,
		New:             []string{},
		Missing:         []string{},
		Changed:         []Change{},
	}
	for _, name := range installedNames {
		if _, ok := catalogFlags[name]; !ok {
			result.New = append(result.New, name)
			continue
		}
		changed := changedFields(installedFlags[name], catalogFlags[name])
		if len(changed) > 0 {
			result.Changed = append(result.Changed, Change{Name: name, Fields: changed})
		}
	}
	for _, name := range catalogNames {
		if _, ok := installedFlags[name]; !ok {
			result.Missing = append(result.Missing, name)
		}
	}
	sort.Strings(result.New)
	sort.Strings(result.Missing)
	sort.Slice(result.Changed, func(i, j int) bool { return result.Changed[i].Name < result.Changed[j].Name })
	result.OK = len(result.New) == 0 && len(result.Missing) == 0 && len(result.Changed) == 0
	return result
}

func changedFields(installed, catalog Flag) []string {
	var out []string
	if !reflect.DeepEqual(installed.Aliases, catalog.Aliases) {
		out = append(out, "aliases")
	}
	if installed.Value != catalog.Value {
		out = append(out, "value")
	}
	if strings.TrimSpace(installed.Description) != strings.TrimSpace(catalog.Description) {
		out = append(out, "description")
	}
	if installed.Default != catalog.Default {
		out = append(out, "default")
	}
	if installed.Env != catalog.Env {
		out = append(out, "env")
	}
	if installed.Category != catalog.Category {
		out = append(out, "category")
	}
	if installed.Deprecated != catalog.Deprecated {
		out = append(out, "deprecated")
	}
	if installed.Replacement != catalog.Replacement {
		out = append(out, "replacement")
	}
	sort.Strings(out)
	return out
}
