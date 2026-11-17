package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

var (
	// ErrModelExists is returned when a model ID is already configured.
	ErrModelExists = errors.New("model already exists")
	// ErrModelPathExists is returned when another model already uses the path.
	ErrModelPathExists = errors.New("model path is already configured")
)

// ValidID reports whether id is a legal model, preset, group or theme name.
func ValidID(id string) bool {
	return validateID("id", id) == nil
}

// CanonicalPath expands ~, makes the path absolute and resolves symlinks when
// possible. It is used to compare configured and discovered model paths.
func CanonicalPath(path string) string {
	path = ExpandPath(path)
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return filepath.Clean(abs)
}

// AddModel writes a [models.<id>] section with path and alias. It preserves
// the rest of the config and refuses to reuse an existing ID or path.
func (c *Config) AddModel(cfgPath, id, modelPath, alias string) error {
	id = strings.TrimSpace(id)
	modelPath = strings.TrimSpace(modelPath)
	alias = strings.TrimSpace(alias)
	if err := validateID("models."+id, id); err != nil {
		return err
	}
	if modelPath == "" {
		return fmt.Errorf("models.%s.path is empty", id)
	}
	if _, ok := c.Models[id]; ok {
		return fmt.Errorf("%w: %s", ErrModelExists, id)
	}
	target := CanonicalPath(modelPath)
	for _, other := range c.ModelIDs() {
		if CanonicalPath(c.Models[other].Path) == target {
			return fmt.Errorf("%w: %s is already used by %s", ErrModelPathExists, modelPath, other)
		}
	}
	if alias == "" {
		alias = id
	}
	return SetValues(cfgPath, "models."+id, []Value{
		{Key: "path", Value: strconv.Quote(ExpandPath(modelPath))},
		{Key: "alias", Value: strconv.Quote(alias)},
	})
}
