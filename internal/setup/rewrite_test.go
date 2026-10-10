package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/krwu/compose-deploy-agent/internal/dotenv"
)

func TestReplaceImage(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{"literal tag", "    image: repo/web:1.2.3", "    image: repo/web:${WEB_TAG:?WEB_TAG is required}"},
		{"no tag", "    image: repo/web", "    image: repo/web:${WEB_TAG:?WEB_TAG is required}"},
		{"other variable", "    image: repo/web:${IMAGE_TAG:?}", "    image: repo/web:${WEB_TAG:?WEB_TAG is required}"},
		{"default", "    image: repo/web:${TAG:-latest}", "    image: repo/web:${WEB_TAG:?WEB_TAG is required}"},
		{"comment", "    image: repo/web:1 # pinned", "    image: repo/web:${WEB_TAG:?WEB_TAG is required} # pinned"},
		{"double quoted", `    image: "repo/web:1"`, `    image: "repo/web:${WEB_TAG:?WEB_TAG is required}"`},
		{"single quoted", `    image: 'repo/web:1'`, `    image: 'repo/web:${WEB_TAG:?WEB_TAG is required}'`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "# top comment\nservices:\n  web:\n" + tt.line + "\n    restart: always\n  db:\n    image: postgres:16\n"
			updated, change, err := replaceImage([]byte(src), "web", GuardedImage("repo/web", "WEB_TAG"), english)
			if err != nil {
				t.Fatalf("replaceImage: %v", err)
			}
			want := strings.Replace(src, tt.line, tt.want, 1)
			if string(updated) != want {
				t.Fatalf("got:\n%s\nwant:\n%s", updated, want)
			}
			if change.LineNo != 4 || change.Old != tt.line || change.New != tt.want {
				t.Fatalf("unexpected change %+v", change)
			}
		})
	}
}

func TestReplaceImage_Unsupported(t *testing.T) {
	tests := map[string]string{
		"anchor":   "services:\n  web:\n    image: &img repo/web:1\n  api:\n    image: *img\n",
		"alias":    "x-img: &img repo/web:1\nservices:\n  web:\n    image: *img\n",
		"merge":    "x-base: &base\n  image: repo/web:1\nservices:\n  web:\n    <<: *base\n",
		"flow":     "services:\n  web: {image: repo/web:1}\n",
		"block":    "services:\n  web:\n    image: >-\n      repo/web:1\n",
		"escaped":  "services:\n  web:\n    image: \"repo/web:\\x31\"\n",
		"no image": "services:\n  web:\n    build: .\n",
	}
	for name, src := range tests {
		if _, _, err := replaceImage([]byte(src), "web", GuardedImage("repo/web", "WEB_TAG"), english); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestCurrentTag(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("IMAGE_TAG=\"abc123\"\nEMPTY=\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		ref   ImageRef
		want  string
		known bool
	}{
		{"literal", ImageRef{
			Repo: "r",
			Tag:  "1.0",
		}, "1.0", true},
		{"untagged", ImageRef{Repo: "r"}, "latest", true},
		{"from .env", ImageRef{
			Repo: "r",
			Var:  "IMAGE_TAG",
			Op:   ":?",
		}, "abc123", true},
		{"default", ImageRef{
			Repo: "r",
			Var:  "EMPTY",
			Op:   ":-",
			Arg:  "v2",
		}, "v2", true},
		{"error message is not a default", ImageRef{
			Repo: "r",
			Var:  "MISSING",
			Op:   ":?",
			Arg:  "required",
		}, "", false},
	}
	for _, tt := range tests {
		got, known, err := CurrentTag(Candidate{
			Dir: dir,
			Ref: tt.ref,
		})
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if got != tt.want || known != tt.known {
			t.Errorf("%s: CurrentTag = %q, %v; want %q, %v", tt.name, got, known, tt.want, tt.known)
		}
	}
}

// newRewriteFixture writes a compose project with a web service tagged by a
// literal and returns the matching rewrite.
func newRewriteFixture(t *testing.T) (Rewrite, string) {
	t.Helper()
	dir := t.TempDir()
	compose := "services:\n  web:\n    image: repo/web:1.0\n"
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte(compose), 0o640); err != nil {
		t.Fatal(err)
	}
	r := Rewrite{
		Candidate: Candidate{
			Project: "app", Dir: dir, File: "compose.yml", ComposeService: "web",
			Ref: ImageRef{
				Repo: "repo/web",
				Tag:  "1.0",
			}, TagEnv: "WEB_TAG",
		},
		CurrentTag: "1.0",
	}
	return r, compose
}

// interpolatingResolver resolves service's image from the compose file and
// its .env, like `docker compose config` would.
func interpolatingResolver(service string) func(dir, file string) (map[string]string, error) {
	return func(dir, file string) (map[string]string, error) {
		src, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			return nil, err
		}
		node, err := locateImage(src, service, english)
		if err != nil {
			return nil, err
		}
		ref, err := ParseImageRef(node.Value, english)
		if err != nil {
			return nil, err
		}
		tag := ref.Tag
		if ref.Var != "" {
			tag, _, err = dotenv.GetLine(filepath.Join(dir, ".env"), ref.Var)
			if err != nil {
				return nil, err
			}
		}
		return map[string]string{service: ref.Repo + ":" + tag}, nil
	}
}

func TestApplyRewrites(t *testing.T) {
	r, original := newRewriteFixture(t)
	edits, err := PlanComposeEdits([]Rewrite{r}, english)
	if err != nil {
		t.Fatalf("PlanComposeEdits: %v", err)
	}
	docker := &fakeInspector{resolve: interpolatingResolver("web")}

	backups, err := ApplyRewrites(t.Context(), docker, edits, []Rewrite{r})
	if err != nil {
		t.Fatalf("ApplyRewrites: %v", err)
	}
	if len(backups) != 1 || backups[0] != r.ComposePath()+".bak" {
		t.Fatalf("backups = %v", backups)
	}
	if data, _ := os.ReadFile(backups[0]); string(data) != original {
		t.Fatalf("backup content = %q", data)
	}
	data, err := os.ReadFile(r.ComposePath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "image: repo/web:${WEB_TAG:?WEB_TAG is required}") {
		t.Fatalf("compose not rewritten:\n%s", data)
	}
	if info, _ := os.Stat(r.ComposePath()); info.Mode().Perm() != 0o640 {
		t.Fatalf("compose mode = %v, want 0640", info.Mode().Perm())
	}
	if tag, ok, _ := dotenv.GetLine(r.EnvFile(), "WEB_TAG"); !ok || tag != "1.0" {
		t.Fatalf(".env WEB_TAG = %q, %v", tag, ok)
	}
}

func TestApplyRewrites_RestoresOnMismatch(t *testing.T) {
	r, original := newRewriteFixture(t)
	edits, err := PlanComposeEdits([]Rewrite{r}, english)
	if err != nil {
		t.Fatalf("PlanComposeEdits: %v", err)
	}
	docker := &fakeInspector{resolve: func(string, string) (map[string]string, error) {
		return map[string]string{"web": "repo/web:other"}, nil
	}}

	if _, err := ApplyRewrites(t.Context(), docker, edits, []Rewrite{r}); err == nil {
		t.Fatalf("expected verification error")
	}
	if data, _ := os.ReadFile(r.ComposePath()); string(data) != original {
		t.Fatalf("compose not restored:\n%s", data)
	}
	if _, err := os.Stat(r.EnvFile()); !os.IsNotExist(err) {
		t.Fatalf(".env created by the rewrite should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(r.ComposePath() + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("backup should be removed after restore, stat err = %v", err)
	}
}

func TestBackupFile_DoesNotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "serve.env")
	if err := os.WriteFile(path, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := BackupFile(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BackupFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if first != path+".bak" || second != path+".bak.1" {
		t.Fatalf("backups = %q, %q", first, second)
	}
}
