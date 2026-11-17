// Package discovery finds GGUF models under the configured model_dirs and
// compares them with the models in config.toml.
package discovery

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"wool/internal/config"
	"wool/internal/gguf"
)

// DefaultWorkers bounds concurrent metadata reads during a scan.
const DefaultWorkers = 4

// Options controls a scan. Zero values use the safe defaults.
type Options struct {
	Workers        int
	FollowSymlinks bool
}

// Entry is one GGUF file found on disk. Configured lists the model IDs that
// already point at this path.
type Entry struct {
	Path           string    `json:"path"`
	Name           string    `json:"name,omitempty"`
	Architecture   string    `json:"architecture,omitempty"`
	Parameters     uint64    `json:"parameters,omitempty"`
	CtxTrain       int       `json:"ctx_train,omitempty"`
	Size           uint64    `json:"size_bytes"`
	ModTime        time.Time `json:"mod_time"`
	SuggestedID    string    `json:"suggested_id"`
	SuggestedAlias string    `json:"suggested_alias,omitempty"`
	Configured     []string  `json:"configured_models,omitempty"`
	Duplicate      bool      `json:"duplicate,omitempty"`

	canonical string
}

// Configured describes one [models.<id>] entry, whether its file exists and
// whether the scan found it under model_dirs.
type Configured struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	Alias      string `json:"alias,omitempty"`
	Exists     bool   `json:"exists"`
	Discovered bool   `json:"discovered"`
	Duplicate  bool   `json:"duplicate,omitempty"`
}

// Warning is a non-fatal problem such as an unreadable directory, a partial
// download or a file that is not valid GGUF.
type Warning struct {
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

// Report is the result of one scan.
type Report struct {
	Dirs       []string     `json:"model_dirs"`
	Entries    []Entry      `json:"entries"`
	Configured []Configured `json:"configured"`
	Warnings   []Warning    `json:"warnings,omitempty"`
}

// Scan walks every configured model directory and reads GGUF metadata with a
// bounded number of workers.
func Scan(cfg *config.Config, opts Options) Report {
	rep := Report{Dirs: []string{}, Entries: []Entry{}, Configured: []Configured{}}
	if cfg == nil {
		return rep
	}
	workers := workerCount(opts.Workers)

	byCanonical := map[string][]string{}
	configuredIndex := map[string]int{}
	for _, id := range cfg.ModelIDs() {
		m := cfg.Models[id]
		path := config.AbsPath(m.Path)
		canonical := config.CanonicalPath(m.Path)
		exists := false
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			exists = true
		}
		configuredIndex[id] = len(rep.Configured)
		rep.Configured = append(rep.Configured, Configured{
			ID: id, Path: path, Alias: m.Alias, Exists: exists,
		})
		if canonical != "" {
			byCanonical[canonical] = append(byCanonical[canonical], id)
		}
	}
	for _, ids := range byCanonical {
		if len(ids) < 2 {
			continue
		}
		for _, id := range ids {
			rep.Configured[configuredIndex[id]].Duplicate = true
		}
	}

	scan := scanner{
		follow:    opts.FollowSymlinks,
		seenDirs:  map[string]bool{},
		seenFiles: map[string]bool{},
	}
	for _, dir := range cfg.App.ModelDirs {
		path := config.AbsPath(dir)
		rep.Dirs = append(rep.Dirs, path)
		info, err := os.Stat(path)
		if err != nil {
			rep.Warnings = append(rep.Warnings, Warning{Path: path, Message: err.Error()})
			continue
		}
		if !info.IsDir() {
			rep.Warnings = append(rep.Warnings, Warning{Path: path, Message: "model dir is not a directory"})
			continue
		}
		scan.walk(path)
	}
	rep.Warnings = append(rep.Warnings, scan.warnings...)

	entries, warnings := readAll(scan.files, workers)
	rep.Warnings = append(rep.Warnings, warnings...)

	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	taken := map[string]bool{}
	for _, id := range cfg.ModelIDs() {
		taken[id] = true
	}
	for i := range entries {
		base := strings.TrimSuffix(filepath.Base(entries[i].Path), filepath.Ext(entries[i].Path))
		entries[i].SuggestedID = uniqueID(slug(base), taken)
		if entries[i].SuggestedAlias == "" {
			entries[i].SuggestedAlias = aliasFor(entries[i].Name, entries[i].SuggestedID)
		}
		if ids := byCanonical[entries[i].canonical]; len(ids) > 0 {
			entries[i].Configured = append([]string(nil), ids...)
			entries[i].Duplicate = len(ids) > 1
			for _, id := range ids {
				rep.Configured[configuredIndex[id]].Discovered = true
			}
		}
	}
	rep.Entries = entries
	sort.Slice(rep.Warnings, func(i, j int) bool {
		if rep.Warnings[i].Path != rep.Warnings[j].Path {
			return rep.Warnings[i].Path < rep.Warnings[j].Path
		}
		return rep.Warnings[i].Message < rep.Warnings[j].Message
	})
	if rep.Warnings == nil {
		rep.Warnings = []Warning{}
	}
	return rep
}

// workerCount keeps metadata reads bounded so a huge model tree cannot spawn
// an unbounded number of goroutines or saturate the disk.
func workerCount(n int) int {
	if n <= 0 {
		return DefaultWorkers
	}
	if n > 32 {
		return 32
	}
	return n
}

// Inspect reads one GGUF file and suggests an ID and alias for `models add`.
func Inspect(path string, taken map[string]bool) (Entry, error) {
	if taken == nil {
		taken = map[string]bool{}
	}
	abs := config.AbsPath(path)
	info, err := os.Stat(abs)
	if err != nil {
		return Entry{}, err
	}
	if !info.Mode().IsRegular() {
		return Entry{}, fmt.Errorf("%s is not a regular file", abs)
	}
	if info.Size() == 0 {
		return Entry{}, fmt.Errorf("%s is empty (partial download?)", abs)
	}
	meta, err := gguf.ReadMeta(abs)
	if err != nil {
		return Entry{}, err
	}
	base := strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
	entry := Entry{
		Path:         abs,
		Name:         meta.Name,
		Architecture: meta.Architecture,
		Parameters:   meta.Parameters(),
		CtxTrain:     meta.CtxTrain,
		Size:         uint64(info.Size()),
		ModTime:      info.ModTime(),
		canonical:    config.CanonicalPath(abs),
	}
	entry.SuggestedID = uniqueID(slug(base), taken)
	entry.SuggestedAlias = aliasFor(meta.Name, entry.SuggestedID)
	return entry, nil
}

type candidate struct {
	path      string
	size      uint64
	modTime   time.Time
	canonical string
}

type scanner struct {
	follow    bool
	seenDirs  map[string]bool
	seenFiles map[string]bool
	files     []candidate
	warnings  []Warning
}

func (s *scanner) walk(dir string) {
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		if s.seenDirs[real] {
			return
		}
		s.seenDirs[real] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		s.warnings = append(s.warnings, Warning{Path: dir, Message: err.Error()})
		return
	}
	for _, de := range entries {
		path := filepath.Join(dir, de.Name())
		if de.Type()&os.ModeSymlink != 0 {
			if !s.follow {
				continue
			}
			info, err := os.Stat(path)
			if err != nil {
				s.warnings = append(s.warnings, Warning{Path: path, Message: err.Error()})
				continue
			}
			if info.IsDir() {
				s.walk(path)
				continue
			}
			if info.Mode().IsRegular() {
				s.file(path, info)
			}
			continue
		}
		if de.IsDir() {
			s.walk(path)
			continue
		}
		if !de.Type().IsRegular() {
			continue
		}
		info, err := de.Info()
		if err != nil {
			s.warnings = append(s.warnings, Warning{Path: path, Message: err.Error()})
			continue
		}
		s.file(path, info)
	}
}

func (s *scanner) file(path string, info fs.FileInfo) {
	if isPartial(filepath.Base(path)) {
		s.warnings = append(s.warnings, Warning{Path: path, Message: "skipped partial download"})
		return
	}
	if !strings.EqualFold(filepath.Ext(path), ".gguf") {
		return
	}
	canonical := config.CanonicalPath(path)
	if s.seenFiles[canonical] {
		return
	}
	s.seenFiles[canonical] = true
	s.files = append(s.files, candidate{
		path: path, size: uint64(info.Size()), modTime: info.ModTime(), canonical: canonical,
	})
}

var partialSuffixes = []string{".part", ".partial", ".download", ".crdownload", ".tmp", ".incomplete"}

func isPartial(name string) bool {
	lower := strings.ToLower(name)
	if !strings.Contains(lower, ".gguf") {
		return false
	}
	for _, suffix := range partialSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

type readResult struct {
	entry   Entry
	warning *Warning
}

func readAll(candidates []candidate, workers int) ([]Entry, []Warning) {
	jobs := make(chan candidate)
	results := make(chan readResult)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for c := range jobs {
				results <- readOne(c)
			}
		}()
	}
	go func() {
		for _, c := range candidates {
			jobs <- c
		}
		close(jobs)
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	entries := make([]Entry, 0, len(candidates))
	var warnings []Warning
	for result := range results {
		if result.warning != nil {
			warnings = append(warnings, *result.warning)
			continue
		}
		entries = append(entries, result.entry)
	}
	return entries, warnings
}

func readOne(c candidate) readResult {
	if c.size == 0 {
		return readResult{warning: &Warning{Path: c.path, Message: "skipped empty file (partial download?)"}}
	}
	meta, err := gguf.ReadMeta(c.path)
	if err != nil {
		return readResult{warning: &Warning{Path: c.path, Message: "skipped: " + err.Error()}}
	}
	return readResult{entry: Entry{
		Path:           c.path,
		Name:           meta.Name,
		Architecture:   meta.Architecture,
		Parameters:     meta.Parameters(),
		CtxTrain:       meta.CtxTrain,
		Size:           c.size,
		ModTime:        c.modTime,
		SuggestedAlias: meta.Name,
		canonical:      c.canonical,
	}}
}

func aliasFor(name, fallback string) string {
	name = strings.TrimSpace(name)
	if name != "" {
		return name
	}
	return fallback
}

// slug turns a file stem into a legal config ID.
func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	dash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), ".-_")
	if out == "" {
		out = "model"
	}
	if !isAlnum(out[0]) {
		out = "model-" + out
	}
	if len(out) > 64 {
		out = strings.Trim(out[:64], ".-_")
	}
	return out
}

func isAlnum(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func uniqueID(base string, taken map[string]bool) string {
	base = slug(base)
	for n := 1; ; n++ {
		id := base
		if n > 1 {
			suffix := fmt.Sprintf("-%d", n)
			limit := 64 - len(suffix)
			trimmed := base
			if len(trimmed) > limit {
				trimmed = strings.Trim(trimmed[:limit], ".-_")
			}
			id = trimmed + suffix
		}
		if !taken[id] {
			taken[id] = true
			return id
		}
	}
}
