package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"wool/internal/measure"
)

const benchHistoryUsage = `usage: wool bench history [TARGET] [options]

options:
  --limit N        newest runs to show (default 20)
  --json           print the matching records as JSON`

type benchHistoryOptions struct {
	target string
	limit  int
	asJSON bool
}

func parseBenchHistoryArgs(args []string) (benchHistoryOptions, error) {
	o := benchHistoryOptions{limit: 20}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			o.asJSON = true
		case a == "-h" || a == "--help":
			return o, flag.ErrHelp
		case a == "--limit":
			if i+1 >= len(args) {
				return o, fmt.Errorf("--limit needs a value")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 {
				return o, fmt.Errorf("--limit must be a positive integer")
			}
			o.limit = n
		case strings.HasPrefix(a, "--limit="):
			n, err := strconv.Atoi(strings.TrimPrefix(a, "--limit="))
			if err != nil || n < 1 {
				return o, fmt.Errorf("--limit must be a positive integer")
			}
			o.limit = n
		case strings.HasPrefix(a, "-"):
			return o, fmt.Errorf("unknown option %q", a)
		default:
			if o.target != "" {
				return o, fmt.Errorf("only one TARGET is allowed")
			}
			o.target = a
		}
	}
	return o, nil
}
func cmdBenchHistory(args []string) int {
	opts, err := parseBenchHistoryArgs(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Println(benchHistoryUsage)
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n%s\n", err, benchHistoryUsage)
		return 2
	}
	store := measure.NewStore()
	records, err := store.BenchHistory(opts.target, opts.limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: bench history: %v\n", err)
		return 1
	}
	if opts.asJSON {
		return writeJSON(records)
	}
	if len(records) == 0 {
		if opts.target != "" {
			fmt.Printf("no benchmarks recorded for %s\n", opts.target)
		} else {
			fmt.Println("no benchmarks recorded")
		}
		return 0
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "WHEN\tTARGET\tPROMPT T/S\tGEN T/S\tVRAM\tLOAD\tCONFIG\tENGINE")
	for _, r := range records {
		b := r.Bench
		if b == nil {
			continue
		}
		fmt.Fprintf(w, "%s\t%s\t%.1f\t%.1f\t%s\t%s\t%s\t%s\n",
			r.At.Local().Format("2006-01-02 15:04"),
			measure.BenchTargetName(r),
			b.Median.PromptTPS,
			b.Median.GenerationTPS,
			benchHistoryVRAM(b),
			benchHistoryLoad(b),
			fallbackDash(r.ConfigHash),
			b.Engine,
		)
	}
	w.Flush()
	fmt.Printf("\n%d run(s) · %s\n", len(records), store.Path)
	return 0
}
func benchHistoryVRAM(b *measure.Bench) string {
	switch {
	case b.VRAMKnown:
		return humanSize(b.VRAMDeltaBytes)
	case b.PeakBytes > 0:
		return "~" + humanSize(b.PeakBytes)
	default:
		return "-"
	}
}
func benchHistoryLoad(b *measure.Bench) string {
	if !b.LoadKnown {
		return "-"
	}
	return fmt.Sprintf("%.1fs", b.LoadMS/1000)
}
func fallbackDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
