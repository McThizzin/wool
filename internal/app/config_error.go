package app

import (
	"strings"

	"wool/internal/config"
	"wool/internal/daemon"
)

// RunError starts the TUI with a config error overlay. It is used when the
// config cannot be loaded at all; the error is shown instead of an empty
// dashboard. The returned error only reports a failure to open the terminal.
func RunError(err error, cfgPath string) error {
	return runModel(NewError(err, cfgPath))
}

// NewError builds a TUI model whose only content is a config error overlay.
// Dismissing the overlay quits the program because there is no usable config.
func NewError(err error, cfgPath string) *Model {
	return newError(err, cfgPath, daemon.NewManager())
}

func newError(err error, cfgPath string, mgr *daemon.Manager) *Model {
	cfg := &config.Config{}
	cfg.ApplyDefaults()
	m := newModel(cfg, cfgPath, mgr)
	m.configError = true
	m.overlayTitle = "config error"
	m.overlay = strings.Join(config.ErrorLines(err), "\n")
	m.overlayScroll = 0
	return m
}
