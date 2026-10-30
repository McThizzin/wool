package config

import (
	_ "embed"
	"errors"
	"os"
	"path/filepath"
)

//go:embed example.toml
var ExampleTOML []byte

// WriteExample creates the sample config. It refuses to overwrite unless
// force is set.
func WriteExample(path string, force bool) error {
	path = ExpandPath(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	flags := os.O_CREATE | os.O_WRONLY
	if force {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("config already exists (use --force to overwrite)")
		}
		return err
	}
	defer f.Close()
	if _, err := f.Write(ExampleTOML); err != nil {
		return err
	}
	return f.Chmod(0o600)
}
