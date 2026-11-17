package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"wool/internal/discovery"
	"wool/internal/ui"
)

func cmdModels(cfgPath string, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, modelsUsage)
		return 2
	}
	switch args[0] {
	case "scan":
		return cmdModelsScan(cfgPath, args[1:])
	case "add":
		return cmdModelsAdd(cfgPath, args[1:])
	case "disk":
		return cmdModelsDisk(cfgPath, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "wool models: unknown subcommand %q\n\n%s", args[0], modelsUsage)
		return 2
	}
}

const modelsUsage = `wool models - inspect and add GGUF models

Usage:
  wool models scan [--json] [--jobs N] [--follow]
  wool models add PATH [--id ID] [--alias ALIAS] [--json]
  wool models disk [--json] [--hash]

scan walks app.model_dirs for *.gguf files, reads their metadata and compares
them with [models.*]. Skip partial downloads by default. --follow includes
symlinked files and directories with loop detection.

disk reports per-model size, total disk usage, models unused by any preset,
duplicate paths and missing files. It is read-only. The default detects
duplicate content from size and GGUF metadata; --hash reads every file in full
and is much slower, but exact.
`

func cmdModelsScan(cfgPath string, args []string) int {
	fs := flag.NewFlagSet("models scan", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	jobs := fs.Int("jobs", discovery.DefaultWorkers, "concurrent metadata readers")
	follow := fs.Bool("follow", false, "follow symlinks")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printConfigError(cfgPath, err)
		return 1
	}
	rep := discovery.Scan(cfg, discovery.Options{Workers: *jobs, FollowSymlinks: *follow})
	if *asJSON {
		return writeJSON(rep)
	}
	printDiscovery(rep)
	return 0
}

func cmdModelsDisk(cfgPath string, args []string) int {
	fs := flag.NewFlagSet("models disk", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	hash := fs.Bool("hash", false, "hash full file contents (slower)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printConfigError(cfgPath, err)
		return 1
	}
	rep := discovery.Disk(cfg, discovery.DiskOptions{Hash: *hash})
	if *asJSON {
		return writeJSON(rep)
	}
	printDisk(rep)
	return 0
}

func printDisk(rep discovery.DiskReport) {
	fmt.Printf("total: %s across %d unique file(s)\n", ui.HumanBytes(rep.TotalBytes), rep.UniqueFiles)
	fmt.Printf("models: %d · unused: %d · missing: %d · duplicate paths: %d · duplicate content: %d\n",
		len(rep.Models), len(rep.Unused), rep.Missing, len(rep.DuplicatePaths), len(rep.DuplicateContent))
	fmt.Printf("duplicate detection: %s\n", rep.DuplicateDetection)

	fmt.Printf("\nconfigured (%d)\n", len(rep.Models))
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSIZE\tSTATUS\tPRESETS\tPATH")
	for _, m := range rep.Models {
		size := "-"
		if m.Status == discovery.DiskStatusOK {
			size = ui.HumanBytes(m.Size)
		}
		status := m.Status
		if m.DuplicatePath {
			status += " dup"
		}
		presets := "-"
		if len(m.Presets) > 0 {
			presets = fmt.Sprintf("%d", len(m.Presets))
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", m.ID, size, status, presets, m.Path)
	}
	w.Flush()

	if len(rep.Unused) > 0 {
		fmt.Printf("\nunused by presets (%d)\n", len(rep.Unused))
		for _, id := range rep.Unused {
			fmt.Printf("  %s\n", id)
		}
	}
	if len(rep.DuplicatePaths) > 0 {
		fmt.Printf("\nduplicate paths (%d)\n", len(rep.DuplicatePaths))
		for _, group := range rep.DuplicatePaths {
			fmt.Printf("  %s\n    %s\n", strings.Join(group.Models, ", "), group.Path)
		}
	}
	if len(rep.DuplicateContent) > 0 {
		fmt.Printf("\nduplicate content (%d, %s)\n", len(rep.DuplicateContent), rep.DuplicateDetection)
		for _, group := range rep.DuplicateContent {
			fmt.Printf("  %s  %s\n", ui.HumanBytes(group.Size), strings.Join(group.Models, ", "))
		}
	}
	if len(rep.Warnings) > 0 {
		fmt.Printf("\nwarnings (%d)\n", len(rep.Warnings))
		for _, warning := range rep.Warnings {
			if warning.Path == "" {
				fmt.Printf("  %s\n", warning.Message)
			} else {
				fmt.Printf("  %s: %s\n", warning.Path, warning.Message)
			}
		}
	}
}

func printDiscovery(rep discovery.Report) {
	fmt.Printf("model dirs: %s\n", strings.Join(rep.Dirs, ", "))

	fmt.Printf("\nconfigured (%d)\n", len(rep.Configured))
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tALIAS\tPATH")
	for _, c := range rep.Configured {
		status := "ok"
		switch {
		case c.Duplicate:
			status = "duplicate"
		case !c.Exists:
			status = "missing"
		case !c.Discovered:
			status = "outside model_dirs"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", c.ID, status, ui.Fallback(c.Alias, "-"), c.Path)
	}
	w.Flush()

	var fresh []discovery.Entry
	for _, e := range rep.Entries {
		if len(e.Configured) == 0 {
			fresh = append(fresh, e)
		}
	}
	fmt.Printf("\ndiscovered (%d)\n", len(fresh))
	if len(fresh) == 0 {
		fmt.Println("(none)")
	} else {
		w = tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tARCH\tPARAMS\tCTX\tSIZE\tPATH")
		for _, e := range fresh {
			params := "-"
			if e.Parameters > 0 {
				params = ui.HumanCount(e.Parameters)
			}
			ctx := "-"
			if e.CtxTrain > 0 {
				ctx = ui.HumanTokens(e.CtxTrain)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				e.SuggestedID, ui.Fallback(e.Architecture, "-"), params, ctx, ui.HumanBytes(e.Size), e.Path)
		}
		w.Flush()
	}

	if len(rep.Warnings) > 0 {
		fmt.Printf("\nwarnings (%d)\n", len(rep.Warnings))
		for _, warning := range rep.Warnings {
			if warning.Path == "" {
				fmt.Printf("  %s\n", warning.Message)
			} else {
				fmt.Printf("  %s: %s\n", warning.Path, warning.Message)
			}
		}
	}
}

func cmdModelsAdd(cfgPath string, args []string) int {
	fs := flag.NewFlagSet("models add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	idFlag := fs.String("id", "", "model ID (default: derived from the filename)")
	aliasFlag := fs.String("alias", "", "display alias (default: GGUF general.name or the ID)")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(reorderModelAddArgs(args)); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprint(os.Stderr, "wool models add: exactly one PATH is required\n\n"+modelsUsage)
		return 2
	}
	path := fs.Arg(0)
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		printConfigError(cfgPath, err)
		return 1
	}
	taken := map[string]bool{}
	for _, id := range cfg.ModelIDs() {
		taken[id] = true
	}
	entry, err := discovery.Inspect(path, taken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool models add: %v\n", err)
		return 1
	}
	id := strings.TrimSpace(*idFlag)
	if id == "" {
		id = entry.SuggestedID
	}
	alias := strings.TrimSpace(*aliasFlag)
	if alias == "" {
		alias = entry.SuggestedAlias
	}
	if err := cfg.AddModel(cfgPath, id, entry.Path, alias); err != nil {
		fmt.Fprintf(os.Stderr, "wool models add: %v\n", err)
		return 1
	}
	result := addedModel{ID: id, Path: entry.Path, Alias: alias}
	if *asJSON {
		return writeJSON(result)
	}
	fmt.Printf("added models.%s (%s)\n", result.ID, result.Path)
	return 0
}

type addedModel struct {
	ID    string `json:"id"`
	Path  string `json:"path"`
	Alias string `json:"alias"`
}

// reorderModelAddArgs lets PATH appear before or after the command's flags.
func reorderModelAddArgs(args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			if (a == "--id" || a == "-id" || a == "--alias" || a == "-alias") && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}
