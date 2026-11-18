// Command flagsgen regenerates the embedded llama-server flag catalog and its
// Markdown reference from help text plus curated overrides.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"wool/internal/flags"
)

const (
	defaultHelp      = "tools/flagsgen/data/llama-server-help.txt"
	defaultOverrides = "tools/flagsgen/data/overrides.json"
	defaultCatalog   = "internal/flags/data/catalog.json"
	defaultMarkdown  = "docs/flags.md"
)

type overrideEntry struct {
	Value       *string  `json:"value"`
	Description *string  `json:"description"`
	Default     *string  `json:"default"`
	Env         *string  `json:"env"`
	Category    *string  `json:"category"`
	Deprecated  *bool    `json:"deprecated"`
	Replacement *string  `json:"replacement"`
	Aliases     []string `json:"aliases"`
}

type overrideFile struct {
	Source    flags.CatalogSource      `json:"source"`
	Overrides map[string]overrideEntry `json:"overrides"`
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("flagsgen", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	helpFile := fs.String("help", defaultHelp, "captured llama-server --help text")
	checkout := fs.String("llama-cpp", "", "local llama.cpp checkout to read help and version from")
	version := fs.String("version", "", "catalog source version (llama.cpp build)")
	commit := fs.String("commit", "", "catalog source commit")
	overridesFile := fs.String("overrides", defaultOverrides, "curated overrides JSON")
	catalogFile := fs.String("out", defaultCatalog, "generated catalog JSON")
	markdownFile := fs.String("markdown", defaultMarkdown, "generated Markdown reference")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	source := flags.CatalogSource{Version: *version, Commit: *commit}
	help := ""
	if *checkout != "" {
		text, err := helpFromCheckout(*checkout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "flagsgen: %v\n", err)
			return 1
		}
		help = text
		if source.Version == "" {
			source.Version = versionFromHelp(text)
		}
		if source.Commit == "" {
			source.Commit = gitCommit(*checkout)
		}
	} else {
		b, err := os.ReadFile(*helpFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "flagsgen: %v\n", err)
			return 1
		}
		help = string(b)
	}

	catalog, err := flags.ParseHelp(help, source)
	if err != nil {
		fmt.Fprintf(os.Stderr, "flagsgen: %v\n", err)
		return 1
	}
	overrides, err := loadOverrides(*overridesFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "flagsgen: %v\n", err)
		return 1
	}
	if source.Version == "" {
		source.Version = overrides.Source.Version
	}
	if source.Commit == "" {
		source.Commit = overrides.Source.Commit
	}
	if source.Version == "" || source.Commit == "" {
		fmt.Fprintln(os.Stderr, "flagsgen: source version and commit are required (use -version/-commit or overrides)")
		return 1
	}
	applyOverrides(catalog, overrides, source)

	data, err := flags.Encode(catalog)
	if err != nil {
		fmt.Fprintf(os.Stderr, "flagsgen: %v\n", err)
		return 1
	}
	if err := writeFile(*catalogFile, data); err != nil {
		fmt.Fprintf(os.Stderr, "flagsgen: %v\n", err)
		return 1
	}
	if err := writeFile(*markdownFile, renderMarkdown(catalog)); err != nil {
		fmt.Fprintf(os.Stderr, "flagsgen: %v\n", err)
		return 1
	}
	fmt.Printf("wrote %s and %s from %s (%s, %s)\n", *catalogFile, *markdownFile, helpSource(*checkout, *helpFile), source.Version, source.Commit)
	return 0
}

func helpSource(checkout, helpFile string) string {
	if checkout != "" {
		return checkout
	}
	return helpFile
}

func helpFromCheckout(dir string) (string, error) {
	candidates := []string{
		filepath.Join(dir, "build", "bin", "llama-server"),
		filepath.Join(dir, "llama-server"),
	}
	bin := ""
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			bin = candidate
			break
		}
	}
	if bin == "" {
		return "", fmt.Errorf("llama-server not found in %s (looked in build/bin and the checkout root)", dir)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--help").CombinedOutput()
	return string(out), err
}

func versionFromHelp(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			for _, field := range fields {
				f := strings.Trim(field, "()[],:")
				if strings.HasPrefix(f, "b") && len(f) > 1 && allDigits(f[1:]) {
					return f
				}
			}
		}
	}
	return ""
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func gitCommit(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func loadOverrides(path string) (overrideFile, error) {
	var ov overrideFile
	b, err := os.ReadFile(path)
	if err != nil {
		return ov, err
	}
	if err := json.Unmarshal(b, &ov); err != nil {
		return ov, fmt.Errorf("decode overrides %s: %w", path, err)
	}
	if ov.Overrides == nil {
		ov.Overrides = map[string]overrideEntry{}
	}
	return ov, nil
}

func applyOverrides(c *flags.Catalog, ov overrideFile, source flags.CatalogSource) {
	c.Source = source
	for i := range c.Flags {
		f := &c.Flags[i]
		applyEntry(f, ov.Overrides[f.Name], source)
	}
	flags.Sort(c.Flags)
}

func applyEntry(f *flags.Flag, o overrideEntry, source flags.CatalogSource) {
	if o.Value != nil {
		f.Value = *o.Value
	}
	if o.Description != nil {
		f.Description = *o.Description
	}
	if o.Default != nil {
		f.Default = *o.Default
	}
	if o.Env != nil {
		f.Env = *o.Env
	}
	if o.Category != nil {
		f.Category = *o.Category
	}
	if o.Deprecated != nil {
		f.Deprecated = *o.Deprecated
	}
	if o.Replacement != nil {
		f.Replacement = *o.Replacement
	}
	if len(o.Aliases) > 0 {
		f.Aliases = append(f.Aliases, o.Aliases...)
	}
	f.Source = source
}

func renderMarkdown(c *flags.Catalog) []byte {
	categories := map[string][]flags.Flag{}
	for _, f := range c.Flags {
		category := f.Category
		if category == "" {
			category = "general"
		}
		categories[category] = append(categories[category], f)
	}
	names := make([]string, 0, len(categories))
	for name := range categories {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	fmt.Fprintf(&b, "# llama-server flag reference\n\n")
	fmt.Fprintf(&b, "Generated by `go run ./tools/flagsgen`. Do not edit by hand.\n\n")
	fmt.Fprintf(&b, "Source: llama.cpp %s (%s). Unknown flags still work; this is a reference, not an allowlist.\n\n", c.Source.Version, c.Source.Commit)
	for _, name := range names {
		fmt.Fprintf(&b, "## %s\n\n", title(name))
		fmt.Fprintf(&b, "| Flag | Value | Description | Default | Environment |\n")
		fmt.Fprintf(&b, "| --- | --- | --- | --- | --- |\n")
		for _, f := range categories[name] {
			label := "`--" + f.Name + "`"
			if len(f.Aliases) > 0 {
				aliases := make([]string, 0, len(f.Aliases))
				for _, alias := range f.Aliases {
					aliases = append(aliases, "`--"+alias+"`")
				}
				label += " (" + strings.Join(aliases, ", ") + ")"
			}
			if f.Deprecated {
				label += " **deprecated**"
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
				label, f.Value, mdCell(f.Description), mdCell(f.Default), mdCell(f.Env))
		}
		b.WriteString("\n")
	}
	return []byte(strings.TrimRight(b.String(), "\n") + "\n")
}

func title(s string) string {
	if s == "" {
		return "General"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func mdCell(s string) string {
	if s == "" {
		return "—"
	}
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.ReplaceAll(s, "\n", " ")
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
