package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"wool/internal/daemon"
	"wool/internal/reqlog"
)

type logsSnapshot struct {
	Version    int          `json:"version"`
	Source     string       `json:"source"`
	BestEffort bool         `json:"best_effort"`
	Recognized bool         `json:"recognized"`
	Rows       []reqlog.Row `json:"rows"`
}

const logsVersion = 1

func cmdLogs(cfgPath string, args []string) int {
	_ = cfgPath
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	n := fs.Int("n", 100, "number of lines")
	follow := fs.Bool("f", false, "follow the log")
	asJSON := fs.Bool("json", false, "print parsed request rows as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *asJSON && *follow {
		fmt.Fprintln(os.Stderr, "wool: --json cannot be combined with -f")
		return 2
	}
	mgr := daemon.NewManager()
	name := fs.Arg(0)
	if name == "" {
		for _, candidate := range []string{"server", "router"} {
			if _, err := mgr.Status(candidate); err == nil {
				name = candidate
				break
			}
		}
	}
	if name == "" {
		fmt.Fprintln(os.Stderr, "wool: no server log found")
		return 1
	}
	lines, err := mgr.Tail(name, *n)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}
	if *asJSON {
		p := reqlog.New(0)
		at := time.Now()
		for _, line := range lines {
			p.Feed(line, at)
		}
		rows := p.Rows()
		if rows == nil {
			rows = []reqlog.Row{}
		} else {
			rows = append([]reqlog.Row(nil), rows...)
		}
		model := ""
		if inst, err := mgr.Status(name); err == nil {
			model = inst.Preset
			if model == "" {
				model = inst.Model
			}
			if model == "" && inst.Kind == daemon.KindRouter {
				model = "router"
			}
		}
		for i := range rows {
			if rows[i].Model == "" {
				rows[i].Model = model
			}
		}
		return writeJSON(logsSnapshot{
			Version:    logsVersion,
			Source:     name,
			BestEffort: true,
			Recognized: p.Recognized(),
			Rows:       rows,
		})
	}
	fmt.Println(strings.Join(lines, "\n"))
	if !*follow {
		return 0
	}
	path, err := mgr.LogPath(name)
	if err != nil {
		return 1
	}
	if err := followFile(path); err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}
	return 0
}

func followFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	buf := make([]byte, 32<<10)
	for {
		select {
		case <-sig:
			return nil
		case <-ticker.C:
			for {
				n, err := f.Read(buf)
				if n > 0 {
					os.Stdout.Write(buf[:n])
				}
				if err != nil {
					if errors.Is(err, io.EOF) {
						break
					}
					return err
				}
				if n == 0 {
					break
				}
			}
		}
	}
}
