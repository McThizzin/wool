package flags

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadHelpAndVersion(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "llama-server")
	script := "#!/bin/sh\ncase \"$1\" in\n  --help) echo '  -m, --model FNAME  model path' >&2 ;;\n  --version) echo 'version: b6000' ;;\nesac\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	help, err := ReadHelp(bin)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(help, "--model") {
		t.Fatalf("help = %q", help)
	}
	if got := ReadVersion(bin); got != "version: b6000" {
		t.Fatalf("version = %q", got)
	}
}
