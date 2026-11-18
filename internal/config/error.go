package config

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Error is a config problem tied to a location in the TOML document. Line and
// column are one-indexed as reported by go-toml. Validation errors have no
// document position, so their line and column stay zero.
type Error struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Key     string `json:"key,omitempty"`
	Message string `json:"message"`
	Err     error  `json:"-"`
}

// Error renders as file:line:col: message, the form used by compilers and
// expected by editors and scripts.
func (e *Error) Error() string {
	msg := e.Message
	if e.Key != "" {
		msg = fmt.Sprintf("%s %q", msg, e.Key)
	}
	if e.File == "" {
		switch {
		case e.Line > 0 && e.Column > 0:
			return fmt.Sprintf("%d:%d: %s", e.Line, e.Column, msg)
		case e.Line > 0:
			return fmt.Sprintf("%d: %s", e.Line, msg)
		default:
			return msg
		}
	}
	switch {
	case e.Line > 0 && e.Column > 0:
		return fmt.Sprintf("%s:%d:%d: %s", e.File, e.Line, e.Column, msg)
	case e.Line > 0:
		return fmt.Sprintf("%s:%d: %s", e.File, e.Line, msg)
	default:
		return fmt.Sprintf("%s: %s", e.File, msg)
	}
}

func (e *Error) Unwrap() error { return e.Err }

// Errors holds every structured error found in a document. Strict decoding
// reports all unknown keys at once instead of stopping at the first one.
type Errors []*Error

func (e Errors) Error() string {
	lines := make([]string, len(e))
	for i, err := range e {
		lines[i] = err.Error()
	}
	return strings.Join(lines, "\n")
}

func (e Errors) Unwrap() []error {
	errs := make([]error, len(e))
	for i, err := range e {
		errs[i] = err
	}
	return errs
}

// structuredDecodeError converts go-toml's decode errors into config errors.
// Unknown fields from strict decoding keep their key and position; ordinary
// syntax errors carry the key go-toml was processing when it failed.
func structuredDecodeError(source string, err error) error {
	var strictErr *toml.StrictMissingError
	if errors.As(err, &strictErr) && len(strictErr.Errors) > 0 {
		out := make(Errors, 0, len(strictErr.Errors))
		for i := range strictErr.Errors {
			out = append(out, decodeError(source, &strictErr.Errors[i]))
		}
		return out
	}
	var de *toml.DecodeError
	if errors.As(err, &de) {
		return decodeError(source, de)
	}
	return err
}

func decodeError(source string, de *toml.DecodeError) *Error {
	line, column := de.Position()
	return &Error{
		File:    source,
		Line:    line,
		Column:  column,
		Key:     strings.Join(de.Key(), "."),
		Message: strings.TrimPrefix(de.Error(), "toml: "),
		Err:     de,
	}
}

// AsErrors returns the structured config errors carried by err, in document
// order. It returns nil for unrelated errors such as a missing file.
func AsErrors(err error) []*Error {
	var list Errors
	if errors.As(err, &list) && len(list) > 0 {
		return []*Error(list)
	}
	var one *Error
	if errors.As(err, &one) {
		return []*Error{one}
	}
	return nil
}

// ErrorLines renders err as one or more "file:line:col: message" lines. The
// CLI and the TUI overlay use the same rendering.
func ErrorLines(err error) []string {
	if errs := AsErrors(err); len(errs) > 0 {
		lines := make([]string, len(errs))
		for i, e := range errs {
			lines[i] = e.Error()
		}
		return lines
	}
	return []string{err.Error()}
}
