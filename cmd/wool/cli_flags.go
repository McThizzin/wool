package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"wool/internal/config"
	"wool/internal/flags"
)

const flagsUsage = `usage: wool flags [--json] [--search TEXT] [--check] [--strict]

Shows the embedded llama-server flag catalog. Search matches canonical names,
aliases, descriptions and categories with deterministic ranking.

options:
  --search TEXT    fuzzy-filter flags (also matches names and descriptions)
  --check          compare installed llama-server --help with the catalog
  --strict         exit non-zero for catalog drift (requires --check)
  --json           print the stable machine-readable result`

const flagsSchemaVersion = 1

type flagsOutput struct {
	Version int                 `json:"schema_version"`
	Source  flags.CatalogSource `json:"source"`
	Flags   []flags.Flag        `json:"flags"`
}

type flagsCheckOutput struct {
	Version int               `json:"schema_version"`
	Result  flags.CheckResult `json:"check"`
}

func cmdFlags(cfgPath string, args []string) int {
	fs := flag.NewFlagSet("flags", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	search := fs.String("search", "", "fuzzy-filter flags")
	check := fs.Bool("check", false, "check the installed llama-server help")
	strict := fs.Bool("strict", false, "treat catalog drift as an error")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	for _, arg := range fs.Args() {
		switch arg {
		case "-h", "--help", "help":
			fmt.Println(flagsUsage)
			return 0
		default:
			fmt.Fprintf(os.Stderr, "wool: flags: unexpected argument %q\n%s\n", arg, flagsUsage)
			return 2
		}
	}
	if *strict && !*check {
		fmt.Fprintln(os.Stderr, "wool: flags: --strict requires --check")
		return 2
	}
	if *check {
		return cmdFlagsCheck(cfgPath, *asJSON, *strict)
	}
	return cmdFlagsList(*asJSON, *search)
}

func cmdFlagsList(asJSON bool, search string) int {
	catalog, err := flags.Embedded()
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: flags: %v\n", err)
		return 1
	}
	results := flags.Search(catalog, search, 0)
	matched := make([]flags.Flag, 0, len(results))
	for _, r := range results {
		matched = append(matched, r.Flag)
	}
	if asJSON {
		return writeJSON(flagsOutput{Version: flagsSchemaVersion, Source: catalog.Source, Flags: matched})
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "FLAG\tVALUE\tCATEGORY\tDESCRIPTION")
	for _, f := range matched {
		label := f.Usage()
		if f.Deprecated {
			label += " (deprecated)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", label, f.Value, f.Category, f.Description)
	}
	if err := w.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "wool: flags: %v\n", err)
		return 1
	}
	if search != "" && len(matched) == 0 {
		fmt.Fprintf(os.Stderr, "wool: flags: no flags match %q\n", search)
		return 1
	}
	return 0
}

func cmdFlagsCheck(cfgPath string, asJSON, strict bool) int {
	catalog, err := flags.Embedded()
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: flags: %v\n", err)
		return 1
	}
	bin := llamaServerBinary(cfgPath)
	help, err := flags.ReadHelp(bin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: flags: cannot read %s --help: %v\n", bin, err)
		return 1
	}
	version := flags.DetectVersion(help)
	if version == "" {
		version = flags.DetectVersion(flags.ReadVersion(bin))
	}
	installed, err := flags.ParseHelp(help, flags.CatalogSource{Version: version})
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: flags: %v\n", err)
		return 1
	}
	result := flags.Compare(installed, catalog)
	if asJSON {
		code := writeJSON(flagsCheckOutput{Version: flagsSchemaVersion, Result: result})
		if strict && !result.OK {
			return 1
		}
		return code
	}
	fmt.Printf("catalog: %s (%s)\n", catalog.Source.Version, catalog.Source.Commit)
	installedVersion := installed.Source.Version
	if installedVersion == "" {
		installedVersion = "unknown"
	}
	fmt.Printf("installed: %s (%s)\n", installedVersion, bin)
	fmt.Printf("new: %d, missing: %d, changed: %d\n", len(result.New), len(result.Missing), len(result.Changed))
	printFlagChanges("new", result.New)
	printFlagChanges("missing", result.Missing)
	for _, change := range result.Changed {
		fmt.Printf("changed %s: %s\n", change.Name, strings.Join(change.Fields, ", "))
	}
	if !result.OK {
		fmt.Fprintln(os.Stderr, "wool: flags: catalog is stale; run go run ./tools/flagsgen with a local llama.cpp checkout")
	}
	if strict && !result.OK {
		return 1
	}
	return 0
}

func printFlagChanges(label string, names []string) {
	for _, name := range names {
		fmt.Printf("%s: %s\n", label, name)
	}
}

func llamaServerBinary(cfgPath string) string {
	bin := "llama-server"
	cfg, err := config.Load(cfgPath)
	if err == nil && strings.TrimSpace(cfg.App.LlamaServer) != "" {
		bin = config.ExpandPath(cfg.App.LlamaServer)
	}
	return bin
}
