// Package dotenv atomically rewrites a single KEY= line inside a compose
// project's .env file, preserving every other line and the file's
// permissions. This is how the agent hands the target tag to `docker
// compose up`, so that a human running `docker compose up` from the same
// directory later sees the same tag the agent last deployed.
package dotenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultFileMode = 0o644

// SetLine rewrites (or appends) the "key=value" line in the .env file at
// path, leaving all other lines untouched. The write is atomic: it writes
// to a temporary file in the same directory and renames it over path, so a
// concurrent reader never observes a partially written file.
func SetLine(path, key, value string) error {
	mode := os.FileMode(defaultFileMode)
	original, err := os.ReadFile(path)
	switch {
	case err == nil:
		if info, statErr := os.Stat(path); statErr == nil {
			mode = info.Mode()
		}
	case os.IsNotExist(err):
		original = nil
	default:
		return fmt.Errorf("read %s: %w", path, err)
	}

	updated := setLineInContent(string(original), key, value)

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".env.tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup if we fail before the rename below.
	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.WriteString(updated); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp file over %s: %w", path, err)
	}
	success = true
	return nil
}

// GetLine reads the current value of key from the .env file at path. It
// returns ("", false, nil) if the file does not exist or the key is absent.
func GetLine(path, key string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", path, err)
	}
	prefix := key + "="
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix), true, nil
		}
	}
	return "", false, nil
}

func setLineInContent(content, key, value string) string {
	prefix := key + "="
	newLine := prefix + value

	var lines []string
	if content != "" {
		lines = strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	}

	found := false
	for i, line := range lines {
		if strings.HasPrefix(line, prefix) {
			lines[i] = newLine
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, newLine)
	}

	out := strings.Join(lines, "\n")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out
}
