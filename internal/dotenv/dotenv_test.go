package dotenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetLine_CreatesFileWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := SetLine(path, "IMAGE_TAG", "abc123"); err != nil {
		t.Fatalf("SetLine: %v", err)
	}
	value, found, err := GetLine(path, "IMAGE_TAG")
	if err != nil {
		t.Fatalf("got value=%q found=%v err=%v", value, found, err)
	}
	if !found || value != "abc123" {
		t.Fatalf("got value=%q found=%v err=%v", value, found, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "IMAGE_TAG=abc123\n" {
		t.Fatalf("content = %q, want a single line", data)
	}
}

func TestSetLine_PreservesOtherLinesAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	original := "FOO=bar\nIMAGE_TAG=old\nBAZ=qux\n"
	if err := os.WriteFile(path, []byte(original), 0o640); err != nil {
		t.Fatalf("write initial .env: %v", err)
	}

	if err := SetLine(path, "IMAGE_TAG", "new"); err != nil {
		t.Fatalf("SetLine: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	want := "FOO=bar\nIMAGE_TAG=new\nBAZ=qux\n"
	if string(data) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", data, want)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat .env: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("expected mode 0640 preserved, got %o", info.Mode().Perm())
	}
}

func TestSetLine_AppendsWhenKeyAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("FOO=bar\n"), 0o644); err != nil {
		t.Fatalf("write initial .env: %v", err)
	}
	if err := SetLine(path, "IMAGE_TAG", "abc123"); err != nil {
		t.Fatalf("SetLine: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	want := "FOO=bar\nIMAGE_TAG=abc123\n"
	if string(data) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", data, want)
	}
}

func TestGetLine_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist", ".env")
	value, found, err := GetLine(path, "IMAGE_TAG")
	if err != nil {
		t.Fatalf("expected not found with no error, got value=%q found=%v err=%v", value, found, err)
	}
	if found || value != "" {
		t.Fatalf("expected not found with no error, got value=%q found=%v err=%v", value, found, err)
	}
}

func TestSetLine_ConcurrentReadersNeverSeePartialWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := SetLine(path, "IMAGE_TAG", "v1"); err != nil {
		t.Fatalf("SetLine: %v", err)
	}
	// Rewriting many times must never leave a temp file behind or corrupt
	// the target file (the rename is atomic).
	for i := 0; i < 20; i++ {
		if err := SetLine(path, "IMAGE_TAG", "v2"); err != nil {
			t.Fatalf("SetLine iteration %d: %v", i, err)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one file in dir, found %d", len(entries))
	}
}
