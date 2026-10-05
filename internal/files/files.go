// Package files writes files the stack generates.
package files

import (
	"errors"
	"os"
	"path/filepath"
)

// WriteAtomic writes content to a temporary file beside path, then renames it.
func WriteAtomic(path string, content []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, "."+filepath.Base(path)+".")
	if err != nil {
		return err
	}
	name := file.Name()
	_, writeErr := file.Write(content)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// WriteIfChanged atomically writes content to path unless the file already
// holds it. It reports whether the file changed.
func WriteIfChanged(path string, content []byte) (bool, error) {
	if current, err := os.ReadFile(path); err == nil && string(current) == string(content) {
		return false, nil
	}
	return true, WriteAtomic(path, content, 0o644)
}
