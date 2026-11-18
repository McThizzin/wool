package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"wool/internal/app"
	"wool/internal/config"
	"wool/internal/doctor"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// woolExecutable locates the running wool binary for the detached supervisor.
// Tests replace it to inject a fake supervisor.
var woolExecutable = os.Executable

const usageText = `wool - llama.cpp server launcher and router TUI

Usage:
  wool [--config PATH]                 open the TUI
  wool doctor [--json]                 check binary, models and VRAM fit
  wool fit TARGET [options]            recommend settings that fit VRAM
  wool bench TARGET [options]          benchmark prompt and generation throughput
  wool bench --url URL --model ID      benchmark an already-running server
  wool bench history [TARGET]          show recorded benchmark runs
  wool bench compare A B [options]     interleaved A/B benchmark comparison
  wool list [--json]                   list models and presets
  wool models scan [--json] [--jobs N] [--follow]  discover GGUF models
  wool models add PATH [--id ID] [--alias ALIAS] [--json]
  wool models disk [--json] [--hash]   sizes, unused, duplicates and missing files
  wool run TARGET [options]            start a model or preset detached
  wool supervise [options] TARGET      run a target under a detached supervisor
  wool restart [server|router] [--no-wait]  relaunch the recorded instance
  wool stop [server|router|all]        stop detached servers
  wool status [name] [--json]          show detached server state
  wool stats [MODEL] [options]         live inference rates from the running server
  wool endpoint [server|router|MODEL] [--json]  base URL and client snippets
  wool logs [name] [-n N] [-f] [--json] show a server log, or parsed requests
  wool alerts [--json]                 evaluate alert rules against current state
  wool router [start|stop|status|models|load ID|unload ID|load-group NAME|groups|reload|ini]
  wool config path                     print the config path
  wool config init [--force] [PATH]    write a sample config
  wool config check [--strict] [--json]  validate the config (strict rejects typos)
  wool config explain TARGET [--json]  show each value and where it came from
  wool config diff TARGET [--against defaults|model] [--json]
  wool version

TARGET is a preset name or a model name. run options:
  --dry-run        print the llama-server command without starting it
  --restart        stop an existing "server" first
  --port N         use this port instead of the configured one
  --no-wait        return as soon as the process is spawned
  --smoke          send a tiny prompt after health
  --strict         fail and stop the server if the smoke test fails

supervise options:
  --restart        stop an existing "server" first
  --port N         use this port instead of the configured one
  --no-wait        return as soon as the supervisor is spawned

Router options are accepted after "router start": --dry-run, --restart,
--port N, --no-wait.

fit options:
  --vram SIZE      total card size (7G, 7GiB); default is the live GPU budget
  --cache-type T   override cache_type_k and cache_type_v
  --gpu-layers N   override gpu_layers
  --n-cpu-moe N    override n_cpu_moe
  --save-preset NAME  write the recommendation as presets.NAME
  --force          replace an existing preset
  --calibrated     apply observed/estimate correction and label it
  --json           print the stable machine-readable result

bench options:
  --runs N         repetitions (default 3)
  --pp N           approximate prompt tokens (default 512)
  --tg N           generated tokens (default 128)
  --engine E       server (default) or llama-bench
  --url URL        benchmark an existing server instead of starting one
  --model ID       model ID for --url
  --no-warmup      skip the warm-up request
  --json           print the recorded measurement and raw runs

bench history options:
  --limit N        newest runs to show (default 20)
  --json           print the matching records as JSON

stats options:
  --window D       time between the two scrapes (default 1s)
  --json           print the stable machine-readable snapshot

endpoint options:
  --json           print the stable machine-readable result

The config defaults to ~/.config/wool/config.toml.
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	cfgPath, rest := extractConfig(args)
	if len(rest) == 0 {
		return openTUI(cfgPath)
	}
	cmd, rest := rest[0], rest[1:]
	switch cmd {
	case "-h", "--help", "help":
		fmt.Print(usageText)
		return 0
	case "-v", "--version", "version":
		fmt.Printf("wool %s (commit %s, built %s)\n", version, commit, date)
		return 0
	case "doctor":
		return cmdDoctor(cfgPath, rest)
	case "fit":
		return cmdFit(cfgPath, rest)
	case "bench":
		return cmdBench(cfgPath, rest)
	case "config":
		return cmdConfig(cfgPath, rest)
	case "list":
		return cmdList(cfgPath, rest)
	case "models":
		return cmdModels(cfgPath, rest)
	case "run", "start":
		return cmdRun(cfgPath, rest)
	case "supervise":
		return cmdSupervise(cfgPath, rest)
	case "restart":
		return cmdRestart(cfgPath, rest)
	case "stop":
		return cmdStop(cfgPath, rest)
	case "status":
		return cmdStatus(cfgPath, rest)
	case "stats":
		return cmdStats(cfgPath, rest)
	case "endpoint":
		return cmdEndpoint(cfgPath, rest)
	case "logs", "log":
		return cmdLogs(cfgPath, rest)
	case "alerts":
		return cmdAlerts(cfgPath, rest)
	case "router":
		return cmdRouter(cfgPath, rest)
	default:
		fmt.Fprintf(os.Stderr, "wool: unknown command %q\n\n%s", cmd, usageText)
		return 2
	}
}

// extractConfig accepts --config/-c before or after the subcommand so both
// "wool --config x doctor" and "wool doctor --config x" work.
func extractConfig(args []string) (string, []string) {
	path := config.DefaultPath()
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		// Everything after -- belongs to a supervised child. In particular,
		// llama-server's -c is the context size, not --config.
		if a == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		switch {
		case a == "--config" || a == "-c":
			if i+1 < len(args) {
				path = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--config="):
			path = strings.TrimPrefix(a, "--config=")
		default:
			rest = append(rest, a)
		}
	}
	return config.ExpandPath(path), rest
}

func openTUI(cfgPath string) int {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		// A terminal can show the parse error in an overlay. Scripts and
		// redirected runs still get the same text on stderr.
		if interactiveTerminal() {
			if runErr := app.RunError(err, cfgPath); runErr == nil {
				return 1
			}
		}
		printConfigError(cfgPath, err)
		return 1
	}
	if err := app.Run(cfg, cfgPath); err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}
	return 0
}

func interactiveTerminal() bool {
	for _, f := range []*os.File{os.Stdin, os.Stdout} {
		info, err := f.Stat()
		if err != nil || info.Mode()&os.ModeCharDevice == 0 {
			return false
		}
	}
	return true
}

func loadConfig(path string) (*config.Config, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	return config.Load(path)
}

func printConfigError(path string, err error) {
	if structured := config.AsErrors(err); len(structured) > 0 {
		for _, e := range structured {
			fmt.Fprintf(os.Stderr, "wool: %s\n", e)
		}
	} else {
		fmt.Fprintf(os.Stderr, "wool: config %s: %v\n", path, err)
	}
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "Create one with: wool config init\n")
	}
}

func cmdDoctor(cfgPath string, args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
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
	rep := doctor.Run(cfg, cfgPath)
	if *asJSON {
		if code := writeJSON(rep); code != 0 {
			return code
		}
	} else {
		fmt.Print(rep.Print())
	}
	if len(rep.Failures()) > 0 {
		return 1
	}
	return 0
}
