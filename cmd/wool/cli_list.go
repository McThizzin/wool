package main

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
)

func cmdList(cfgPath string, args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printConfigError(cfgPath, err)
		return 1
	}
	var entries []listEntry
	for _, id := range cfg.ModelIDs() {
		r, err := cfg.Resolve(id, nil)
		if err != nil {
			continue
		}
		exists := false
		if _, err := os.Stat(r.ModelPath); err == nil {
			exists = true
		}
		entries = append(entries, listEntry{ID: id, Kind: "model", Alias: r.Alias, Path: r.ModelPath, Ctx: r.Ctx, Exists: exists})
	}
	for _, id := range cfg.PresetIDs() {
		p := cfg.Presets[id]
		r, err := cfg.Resolve("", &p)
		if err != nil {
			continue
		}
		exists := false
		if _, err := os.Stat(r.ModelPath); err == nil {
			exists = true
		}
		entries = append(entries, listEntry{ID: id, Kind: "preset", Model: p.Model, Path: r.ModelPath, Ctx: r.Ctx, Exists: exists})
	}
	if *asJSON {
		return writeJSON(entries)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tKIND\tCTX\tALIAS/MODEL\tSTATUS")
	for _, e := range entries {
		extra := e.Alias
		if e.Kind == "preset" {
			extra = e.Model
		}
		status := "ok"
		if !e.Exists {
			status = "missing"
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", e.ID, e.Kind, e.Ctx, extra, status)
	}
	w.Flush()
	return 0
}
