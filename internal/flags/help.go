package flags

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

const helpTimeout = 5 * time.Second

// ReadHelp runs an installed llama-server with --help and returns its combined
// output. Some builds print help to stderr.
func ReadHelp(bin string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), helpTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--help").CombinedOutput()
	return string(out), err
}

// ReadVersion returns the first line of an installed binary's --version output.
// It returns an empty string when the binary cannot be queried.
func ReadVersion(bin string) string {
	ctx, cancel := context.WithTimeout(context.Background(), helpTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
