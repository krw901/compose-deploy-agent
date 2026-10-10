package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReplaceAndRestore(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "deploy-agent")
	writeFile(t, target, "old")
	writeFile(t, BackupPath(target), "stale backup")
	newPath := filepath.Join(dir, ".deploy-agent-upgrade-1")
	writeFile(t, newPath, "new")

	if err := Replace(target, newPath); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if got := readFile(t, target); got != "new" {
		t.Fatalf("target = %q, want new", got)
	}
	if got := readFile(t, BackupPath(target)); got != "old" {
		t.Fatalf("backup = %q, want old", got)
	}
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be renamed away, stat err = %v", newPath, err)
	}

	if err := Restore(target); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := readFile(t, target); got != "old" {
		t.Fatalf("target after restore = %q, want old", got)
	}
}

func TestReplace_MissingTarget(t *testing.T) {
	dir := t.TempDir()
	newPath := filepath.Join(dir, "new")
	writeFile(t, newPath, "new")

	if err := Replace(filepath.Join(dir, "deploy-agent"), newPath); err == nil {
		t.Fatal("expected error when target does not exist")
	}
	if got := readFile(t, newPath); got != "new" {
		t.Fatalf("new file should be left untouched, got %q", got)
	}
}
