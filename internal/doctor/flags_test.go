package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"wool/internal/config"
	"wool/internal/flags"
)

func TestReportPrintShowsFlagVersions(t *testing.T) {
	rep := Report{
		ConfigPath:           "/tmp/config.toml",
		FlagCatalog:          flags.CatalogSource{Version: "b6000", Commit: "a1b2c3d"},
		InstalledLlamaServer: "version: b6000",
		Checks:               []Check{{Level: LevelOK, Name: "llama-server", Detail: "/bin/llama-server"}},
	}
	out := rep.Print()
	for _, want := range []string{
		"flag catalog: b6000 (a1b2c3d)",
		"llama-server: version: b6000",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q:\n%s", want, out)
		}
	}
}

func TestRunReportsCatalogAndInstalledHelp(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	help, err := os.ReadFile(filepath.Join(root, "tools", "flagsgen", "data", "llama-server-help.txt"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "llama-server")
	script := "#!/bin/sh\ncase \"$1\" in\n  --version) echo 'version: b6000' ;;\n  --help) cat <<'HELP'\n" + string(help) + "HELP\n  ;;\nesac\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse([]byte(fmt.Sprintf("[app]\nllama_server = %s\n", strconv.Quote(bin))), "flags.toml")
	if err != nil {
		t.Fatal(err)
	}

	rep := Run(cfg, "flags.toml")
	if rep.FlagCatalog.Version != "b6000" || rep.FlagCatalog.Commit != "a1b2c3d" {
		t.Fatalf("catalog source = %+v", rep.FlagCatalog)
	}
	if !strings.Contains(rep.InstalledLlamaServer, "b6000") {
		t.Fatalf("installed version = %q", rep.InstalledLlamaServer)
	}
	checks := map[string]Level{}
	for _, c := range rep.Checks {
		checks[c.Name] = c.Level
	}
	if level, ok := checks["flag catalog"]; !ok || level != LevelOK {
		t.Fatalf("flag catalog check = %+v", checks)
	}
	if _, ok := checks["flag catalog check"]; !ok {
		t.Fatalf("missing installed-help comparison: %+v", checks)
	}
}
