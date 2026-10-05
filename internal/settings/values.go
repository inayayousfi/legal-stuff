// Package settings owns the saved values in .env and the setup progress file.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// renamedKeys maps keys written by earlier versions to their current names.
var renamedKeys = map[string]string{
	"JELLYFIN_USER": "JELLYFIN_ADMIN_USER",
	"JELLYFIN_PASS": "JELLYFIN_ADMIN_PASS",
}

// Values holds .env entries in the order they were first set, so a rewrite
// keeps the file's existing order.
type Values struct {
	keys []string
	m    map[string]string
}

func NewValues() *Values {
	return &Values{m: map[string]string{}}
}

func (v *Values) Get(key string) string { return v.m[key] }

func (v *Values) Has(key string) bool { return v.m[key] != "" }

func (v *Values) Set(key, value string) {
	if _, ok := v.m[key]; !ok {
		v.keys = append(v.keys, key)
	}
	v.m[key] = value
}

// SetDefault sets key only when it has no entry yet.
func (v *Values) SetDefault(key, value string) {
	if _, ok := v.m[key]; !ok {
		v.Set(key, value)
	}
}

func (v *Values) Keys() []string { return append([]string(nil), v.keys...) }

// Read loads path. A missing file yields empty values. Keys renamed since an
// earlier version are moved to their new names and the file is rewritten.
func Read(path string) (*Values, error) {
	values := NewValues()
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return values, nil
	}
	if err != nil {
		return nil, err
	}
	renamed := false
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		key, rawValue, ok := strings.Cut(line, "=")
		if line == "" || strings.HasPrefix(line, "#") || !ok {
			continue
		}
		var value string
		if json.Unmarshal([]byte(rawValue), &value) != nil {
			value = rawValue
		}
		if newKey, ok := renamedKeys[key]; ok {
			key, renamed = newKey, true
		}
		values.Set(key, strings.ReplaceAll(value, "$$", "$"))
	}
	if renamed {
		if err := Write(path, values); err != nil {
			return nil, err
		}
	}
	return values, nil
}

// Write replaces path atomically. On Unix the file is readable only by its owner.
func Write(path string, values *Values) error {
	var content strings.Builder
	for _, key := range values.keys {
		encoded, err := encodeValue(values.m[key])
		if err != nil {
			return err
		}
		fmt.Fprintf(&content, "%s=%s\n", key, encoded)
	}
	return WriteFileAtomic(path, []byte(content.String()), 0o600)
}

// encodeValue writes a double-quoted value that Docker Compose reads back
// unchanged: "$" is doubled to stop interpolation and non-ASCII is escaped.
func encodeValue(value string) (string, error) {
	if strings.ContainsAny(value, "\r\n") {
		return "", errors.New("Environment values cannot contain line breaks.")
	}
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range strings.ReplaceAll(value, "$", "$$") {
		switch {
		case r == '"' || r == '\\':
			out.WriteByte('\\')
			out.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&out, `\u%04x`, r)
		case r < utf8.RuneSelf:
			out.WriteRune(r)
		case r > 0xffff:
			r1, r2 := surrogates(r)
			fmt.Fprintf(&out, `\u%04x\u%04x`, r1, r2)
		default:
			fmt.Fprintf(&out, `\u%04x`, r)
		}
	}
	out.WriteByte('"')
	return out.String(), nil
}

func surrogates(r rune) (rune, rune) {
	r -= 0x10000
	return 0xd800 + (r>>10)&0x3ff, 0xdc00 + r&0x3ff
}

// WriteFileAtomic writes content to a temporary file beside path, then renames it.
func WriteFileAtomic(path string, content []byte, mode os.FileMode) error {
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

// WriteIfChanged writes content to path unless the file already holds it.
// It reports whether the file changed.
func WriteIfChanged(path string, content []byte) (bool, error) {
	if current, err := os.ReadFile(path); err == nil && string(current) == string(content) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, content, 0o644)
}
