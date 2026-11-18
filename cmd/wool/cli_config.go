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

func cmdConfig(cfgPath string, args []string) int {
	if len(args) == 0 {
		fmt.Print("usage: wool config path|init|check|explain|diff\n")
		return 2
	}
	switch args[0] {
	case "path":
		fmt.Println(cfgPath)
		return 0
	case "check":
		return cmdConfigCheck(cfgPath, args[1:])
	case "explain":
		return cmdConfigExplain(cfgPath, args[1:])
	case "diff":
		return cmdConfigDiff(cfgPath, args[1:])
	case "init":
		fs := flag.NewFlagSet("config init", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		force := fs.Bool("force", false, "overwrite an existing config")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		path := cfgPath
		if fs.NArg() > 0 {
			path = config.ExpandPath(fs.Arg(0))
		}
		if err := config.WriteExample(path, *force); err != nil {
			fmt.Fprintf(os.Stderr, "wool: %v\n", err)
			return 1
		}
		fmt.Printf("wrote %s\n", path)
		if _, err := config.Load(path); err != nil {
			fmt.Fprintf(os.Stderr, "warning: generated config does not load: %v\n", err)
			return 1
		}
		fmt.Println("Edit model paths, then run: wool doctor")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "wool: unknown config command %q\n", args[0])
		return 2
	}
}

type configCheckError struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Key     string `json:"key,omitempty"`
	Message string `json:"message"`
}

type configCheckOutput struct {
	OK       bool               `json:"ok"`
	Strict   bool               `json:"strict"`
	Errors   []configCheckError `json:"errors"`
	Warnings []flags.Warning    `json:"warnings,omitempty"`
}

// cmdConfigCheck validates the config without starting servers. --strict also
// rejects unknown keys and promotes unknown or deprecated extra flags to
// errors, while still allowing map fields.
func cmdConfigCheck(cfgPath string, args []string) int {
	fs := flag.NewFlagSet("config check", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	strict := fs.Bool("strict", false, "reject unknown keys and flags")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: wool config check [--strict] [--json]")
		return 2
	}

	cfg, err := config.LoadWithOptions(cfgPath, config.ParseOptions{Strict: *strict})
	structured := config.AsErrors(err)
	out := configCheckOutput{Strict: *strict, Errors: []configCheckError{}, Warnings: []flags.Warning{}}
	for _, e := range structured {
		out.Errors = append(out.Errors, configCheckError{
			File: e.File, Line: e.Line, Column: e.Column, Key: e.Key, Message: e.Message,
		})
	}

	catalog, catalogErr := flags.Embedded()
	var flagErrors []configCheckError
	var genericMessage string
	if err == nil && catalogErr == nil {
		out.Warnings = flags.ConfigWarnings(cfg, catalog)
		if *strict {
			if strictErr := flags.StrictError(out.Warnings); strictErr != nil {
				for _, message := range strings.Split(strictErr.Error(), "\n") {
					flagErrors = append(flagErrors, configCheckError{File: cfgPath, Message: message})
				}
				err = strictErr
			}
		}
	}
	if err != nil && len(structured) == 0 && len(flagErrors) == 0 {
		genericMessage = err.Error()
		out.Errors = append(out.Errors, configCheckError{File: cfgPath, Message: genericMessage})
	}
	out.Errors = append(out.Errors, flagErrors...)
	out.OK = err == nil
	if len(out.Warnings) == 0 {
		out.Warnings = nil
	}

	if *asJSON {
		code := writeJSON(out)
		if err != nil {
			return 1
		}
		return code
	}
	if err != nil {
		for _, e := range structured {
			fmt.Fprintf(os.Stderr, "wool: %s\n", e)
		}
		for _, e := range flagErrors {
			fmt.Fprintf(os.Stderr, "wool: %s: %s\n", cfgPath, e.Message)
		}
		if genericMessage != "" {
			fmt.Fprintf(os.Stderr, "wool: config %s: %s\n", cfgPath, genericMessage)
		}
		printConfigWarnings(flags.WarningMessages(out.Warnings))
		return 1
	}
	for _, warning := range flags.WarningMessages(out.Warnings) {
		fmt.Fprintln(os.Stderr, "wool: warning: "+warning)
	}
	fmt.Println("config OK")
	return 0
}

type explainField struct {
	Field  string `json:"field"`
	Value  any    `json:"value"`
	Source string `json:"source"`
	Layer  string `json:"layer"`
}

type explainOutput struct {
	Target   string         `json:"target"`
	Kind     string         `json:"kind"`
	Model    string         `json:"model"`
	Fields   []explainField `json:"fields"`
	Warnings []string       `json:"warnings,omitempty"`
}

// cmdConfigExplain prints every resolved launch option with the layer that
// supplied it. It is the CLI counterpart of the TUI editor's source column.
func cmdConfigExplain(cfgPath string, args []string) int {
	// Accept --json before or after TARGET; flag.Parse stops at the first
	// positional argument.
	asJSON := false
	var positional []string
	for _, arg := range args {
		switch arg {
		case "--json":
			asJSON = true
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(os.Stderr, "wool: unknown option %q\n", arg)
				return 2
			}
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, "usage: wool config explain TARGET [--json]")
		return 2
	}
	target := positional[0]
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printConfigError(cfgPath, err)
		return 1
	}

	var r config.Resolved
	var sources map[string]config.Provenance
	kind := ""
	r, sources, kind, err = resolveNamedTarget(cfg, target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}

	out := explainOutput{
		Target:   target,
		Kind:     kind,
		Model:    r.ModelID,
		Warnings: resolvedExtraWarnings(r.Extra),
	}
	for _, field := range config.FieldNames(r) {
		p := sources[field]
		out.Fields = append(out.Fields, explainField{
			Field:  field,
			Value:  config.FieldValue(r, field),
			Source: p.Source,
			Layer:  p.Layer,
		})
	}
	if asJSON {
		return writeJSON(out)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Printf("%s (%s), model %s\n", out.Target, out.Kind, out.Model)
	fmt.Fprintln(w, "FIELD\tVALUE\tSOURCE\tLAYER")
	for _, f := range out.Fields {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", f.Field, config.DisplayFieldValue(f.Field, f.Value), f.Source, f.Layer)
	}
	w.Flush()
	printConfigWarnings(out.Warnings)
	return 0
}

func resolvedExtraWarnings(extra map[string]string) []string {
	catalog, err := flags.Embedded()
	if err != nil {
		return config.ExtraConflictWarnings(extra)
	}
	return flags.WarningMessages(flags.CheckExtras(extra, catalog))
}

func printConfigWarnings(warnings []string) {
	for _, warning := range warnings {
		fmt.Fprintln(os.Stderr, "wool: warning: "+warning)
	}
}

// resolveNamedTarget resolves a preset or model by name.
func resolveNamedTarget(cfg *config.Config, target string) (config.Resolved, map[string]config.Provenance, string, error) {
	if _, ok := cfg.Presets[target]; ok {
		r, sources, err := cfg.ResolveDetailed("", target)
		return r, sources, "preset", err
	}
	if _, ok := cfg.Models[target]; ok {
		r, sources, err := cfg.ResolveDetailed(target, "")
		return r, sources, "model", err
	}
	return config.Resolved{}, nil, "", fmt.Errorf("no preset or model named %q", target)
}

type diffOutput struct {
	Target   string             `json:"target"`
	Kind     string             `json:"kind"`
	Model    string             `json:"model"`
	Against  string             `json:"against"`
	Fields   []config.DiffField `json:"fields"`
	Warnings []string           `json:"warnings,omitempty"`
}

// cmdConfigDiff prints only the resolved launch options that differ from a
// comparison layer. A preset defaults to its model; a model defaults to the
// defaults layer. No fields means no output, like a quiet diff.
func cmdConfigDiff(cfgPath string, args []string) int {
	asJSON := false
	against := ""
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			asJSON = true
		case arg == "--against":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "usage: wool config diff TARGET [--against defaults|model] [--json]")
				return 2
			}
			i++
			against = args[i]
		case strings.HasPrefix(arg, "--against="):
			against = strings.TrimPrefix(arg, "--against=")
		case strings.HasPrefix(arg, "-"):
			fmt.Fprintf(os.Stderr, "wool: unknown option %q\n", arg)
			return 2
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, "usage: wool config diff TARGET [--against defaults|model] [--json]")
		return 2
	}
	if against != "" && against != config.SourceDefaults && against != config.SourceModel {
		fmt.Fprintf(os.Stderr, "wool: --against must be defaults or model, not %q\n", against)
		return 2
	}
	target := positional[0]
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printConfigError(cfgPath, err)
		return 1
	}
	r, _, kind, err := resolveNamedTarget(cfg, target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}
	presetName := ""
	if kind == "preset" {
		presetName = target
	}
	res, err := cfg.Diff(r.ModelID, presetName, against)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}
	out := diffOutput{
		Target:   target,
		Kind:     kind,
		Model:    r.ModelID,
		Against:  res.Against,
		Fields:   res.Fields,
		Warnings: resolvedExtraWarnings(r.Extra),
	}
	if asJSON {
		return writeJSON(out)
	}
	if len(res.Fields) == 0 {
		printConfigWarnings(out.Warnings)
		return 0
	}
	fmt.Printf("%s (%s), model %s, against %s\n", out.Target, out.Kind, out.Model, out.Against)
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "FIELD\tTARGET\tAGAINST")
	for _, f := range res.Fields {
		fmt.Fprintf(w, "%s\t%s (%s %s)\t%s (%s %s)\n",
			f.Field,
			config.DisplayFieldValue(f.Field, f.Target.Value), f.Target.Source, f.Target.Layer,
			config.DisplayFieldValue(f.Field, f.Against.Value), f.Against.Source, f.Against.Layer)
	}
	w.Flush()
	printConfigWarnings(out.Warnings)
	return 0
}

type listEntry struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Alias  string `json:"alias,omitempty"`
	Model  string `json:"model,omitempty"`
	Path   string `json:"path,omitempty"`
	Ctx    int    `json:"ctx"`
	Exists bool   `json:"exists"`
}
