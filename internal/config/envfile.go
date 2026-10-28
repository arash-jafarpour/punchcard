package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrEnvFileNotFound reports a missing env file that the caller named.
// The caller can test it with errors.Is to select an exit code.
var ErrEnvFileNotFound = errors.New("env file does not exist")

// LoadOptions selects the env file for Load.
type LoadOptions struct {
	// EnvFile is the path of a file with KEY=VALUE lines. Empty means no file.
	EnvFile string
	// Required makes a missing EnvFile an error. Set it when the user named
	// the file on the command line. A missing default file stays silent.
	Required bool
}

// DefaultEnvFile returns the per-user env file path:
// $XDG_CONFIG_HOME/punchcard/env, or ~/.config/punchcard/env on Linux.
func DefaultEnvFile() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "punchcard", "env"), nil
}

// ParseEnvFile reads KEY=VALUE lines from r.
//
// The parser reads data only. It does not expand $VAR and it does not run
// commands. Empty lines and lines that start with # are skipped. The "export"
// prefix is permitted. One pair of surrounding quote marks is removed.
func ParseEnvFile(r io.Reader) (map[string]string, error) {
	values := make(map[string]string)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(strings.TrimSuffix(scanner.Text(), "\r"))
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		text = trimExport(text)

		key, value, ok := strings.Cut(text, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected KEY=VALUE", line)
		}
		key = strings.TrimSpace(key)
		if !validKey(key) {
			return nil, fmt.Errorf("line %d: invalid key %q", line, key)
		}
		values[key] = unquote(strings.TrimSpace(value))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

// readEnvFile opens and parses an env file. The found result is false when a
// file that the caller did not require is absent.
func readEnvFile(path string, required bool) (values map[string]string, found bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if !required {
				return nil, false, nil
			}
			return nil, false, fmt.Errorf("%w: %s", ErrEnvFileNotFound, path)
		}
		return nil, false, fmt.Errorf("env file %s: %w", path, err)
	}
	defer f.Close()

	values, err = ParseEnvFile(f)
	if err != nil {
		return nil, true, fmt.Errorf("env file %s: %w", path, err)
	}
	return values, true, nil
}

// lookup gives the process environment priority over the file values.
func lookup(getenv func(string) string, file map[string]string) func(string) string {
	return func(key string) string {
		if value := getenv(key); value != "" {
			return value
		}
		return file[key]
	}
}

func trimExport(text string) string {
	rest, ok := strings.CutPrefix(text, "export")
	if !ok || rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
		return text
	}
	return strings.TrimSpace(rest)
}

func unquote(value string) string {
	if len(value) < 2 {
		return value
	}
	first, last := value[0], value[len(value)-1]
	if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
		return value[1 : len(value)-1]
	}
	return value
}

// validKey accepts letters, digits and underscores. A digit cannot go first.
func validKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		switch {
		case r == '_', r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}
