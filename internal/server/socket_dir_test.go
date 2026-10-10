package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func Test_checkPrivateDir(t *testing.T) {
	uid := os.Geteuid()
	base := t.TempDir()

	private := filepath.Join(base, "private")
	mustMkdir(t, private, 0o700)
	err := checkPrivateDir(private, uid)
	if err != nil {
		t.Fatalf("0700 dir owned by us must pass: %v", err)
	}
	err = checkPrivateDir(private, uid+1)
	if err == nil || !strings.Contains(err.Error(), "owned by uid") {
		t.Fatalf("dir owned by another uid must be refused, got %v", err)
	}

	for _, mode := range []os.FileMode{0o750, 0o705, 0o770, 0o755, 0o777} {
		dir := filepath.Join(base, "mode-"+mode.String())
		mustMkdir(t, dir, mode)
		err = checkPrivateDir(dir, uid)
		if err == nil {
			t.Errorf("mode %04o must be refused", mode)
		}
	}

	link := filepath.Join(base, "link")
	err = os.Symlink(private, link)
	if err != nil {
		t.Fatalf("symlink: %v", err)
	}
	err = checkPrivateDir(link, uid)
	if err == nil {
		t.Fatalf("a symlinked socket directory must be refused")
	}

	file := filepath.Join(base, "file")
	err = os.WriteFile(file, nil, 0o600)
	if err != nil {
		t.Fatalf("write file: %v", err)
	}
	err = checkPrivateDir(file, uid)
	if err == nil {
		t.Fatalf("a regular file must be refused")
	}
}

func Test_ensurePrivateDirCreatesMissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "deploy-agent")
	err := ensurePrivateDir(dir)
	if err != nil {
		t.Fatalf("ensurePrivateDir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("expected a 0700 dir, got %04o", info.Mode().Perm())
	}

	err = ensurePrivateDir(filepath.Join(t.TempDir(), "missing-parent", "deploy-agent"))
	if err == nil {
		t.Fatalf("only the last path element may be created")
	}
}

func TestServer_LocalListenerRefusesSharedDir(t *testing.T) {
	srv := &Server{}
	dir := filepath.Join(t.TempDir(), "shared")
	mustMkdir(t, dir, 0o755)
	socketPath := filepath.Join(dir, "agent.sock")

	err := srv.LocalListener(t.Context(), socketPath)
	if err == nil || !strings.Contains(err.Error(), "group or others") {
		t.Fatalf("expected LocalListener to refuse a group/world-accessible dir, got %v", err)
	}
	_, statErr := os.Stat(socketPath)
	if statErr == nil {
		t.Fatalf("no socket may be created in a shared directory")
	}
}

func mustMkdir(t *testing.T, dir string, mode os.FileMode) {
	t.Helper()
	err := os.Mkdir(dir, mode)
	if err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	// Mkdir is subject to the umask; set the exact mode under test.
	err = os.Chmod(dir, mode)
	if err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}
}

// privateTempDir returns a test temp dir narrowed to 0700, as the unix
// socket listener requires (t.TempDir itself is created group/world-readable).
func privateTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	err := os.Chmod(dir, 0o700)
	if err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}
	return dir
}
