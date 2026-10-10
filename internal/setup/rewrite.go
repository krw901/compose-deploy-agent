package setup

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/krwu/compose-deploy-agent/internal/dotenv"
	"gopkg.in/yaml.v3"
)

const defaultFileMode = 0o644

// tagValuePattern is docker's tag grammar.
var tagValuePattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

// Rewrite is the change made to one compose service so that its tag comes
// from TagEnv: the image line is guarded by ${TagEnv:?} and CurrentTag is
// written to the project's .env so the running version stays the same.
type Rewrite struct {
	Candidate
	CurrentTag string
}

// NewImage returns the image value written into the compose file.
func (r Rewrite) NewImage() string {
	return GuardedImage(r.Ref.Repo, r.TagEnv)
}

// EnvFile returns the project's .env path.
func (r Rewrite) EnvFile() string {
	return filepath.Join(r.Dir, ".env")
}

// LineChange is one rewritten line of a compose file, for preview.
type LineChange struct {
	LineNo int
	Old    string
	New    string
}

// ComposeEdit is the full new content of one compose file.
type ComposeEdit struct {
	Path     string
	Original []byte
	Updated  []byte
	Changes  []LineChange
}

// ValidateTagValue checks a tag typed in by the user.
func ValidateTagValue(tag string, text Text) error {
	if !tagValuePattern.MatchString(tag) {
		return fmt.Errorf(text.ErrBadTag, tag)
	}
	return nil
}

// CurrentTag returns the tag compose resolves for c today from the compose
// file and .env alone, and false if it cannot be determined.
func CurrentTag(c Candidate) (string, bool, error) {
	if c.Ref.Var == "" {
		return cmp.Or(c.Ref.Tag, defaultTag), true, nil
	}
	value, ok, err := dotenv.GetLine(filepath.Join(c.Dir, ".env"), c.Ref.Var)
	if err != nil {
		return "", false, err
	}
	if value = strings.Trim(strings.TrimSpace(value), `"'`); ok && value != "" {
		return value, true, nil
	}
	if (c.Ref.Op == "-" || c.Ref.Op == ":-") && c.Ref.Arg != "" {
		return c.Ref.Arg, true, nil
	}
	return "", false, nil
}

// locateImage returns the scalar node holding services.<service>.image, and
// an error explaining why the line cannot be rewritten in place when the
// image is inherited, aliased or not a single-line scalar.
func locateImage(src []byte, service string, text Text) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, fmt.Errorf(text.ReasonParseCompose, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, errors.New(text.ReasonEmptyCompose)
	}
	svc := mappingValue(mappingValue(doc.Content[0], "services"), service)
	if svc == nil || svc.Kind != yaml.MappingNode {
		return nil, errors.New(text.ReasonNotInline)
	}
	if svc.Style&yaml.FlowStyle != 0 {
		return nil, errors.New(text.ReasonFlowStyle)
	}
	image := mappingValue(svc, "image")
	switch {
	case image == nil:
		return nil, errors.New(text.ReasonImageNotInline)
	case image.Kind != yaml.ScalarNode:
		return nil, errors.New(text.ReasonImageAlias)
	case image.Anchor != "":
		return nil, errors.New(text.ReasonImageAnchor)
	case image.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0:
		return nil, errors.New(text.ReasonImageBlock)
	default:
		return image, nil
	}
}

func mappingValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// replaceImage rewrites the image value of service in src to newImage,
// touching nothing but that value's characters on its line.
func replaceImage(src []byte, service, newImage string, text Text) ([]byte, LineChange, error) {
	node, err := locateImage(src, service, text)
	if err != nil {
		return nil, LineChange{}, err
	}
	lines := strings.SplitAfter(string(src), "\n")
	line := []rune(lines[node.Line-1])
	col := node.Column - 1
	if col < 0 || col > len(line) {
		return nil, LineChange{}, fmt.Errorf("line %d: unexpected image position", node.Line)
	}
	prefix, rest := string(line[:col]), string(line[col:])

	oldToken, newToken := node.Value, newImage
	switch {
	case node.Style&yaml.DoubleQuotedStyle != 0:
		oldToken, newToken = `"`+oldToken+`"`, `"`+newToken+`"`
	case node.Style&yaml.SingleQuotedStyle != 0:
		oldToken, newToken = `'`+oldToken+`'`, `'`+newToken+`'`
	default:
		// Unquoted scalars are already the raw image text.
	}
	after, ok := strings.CutPrefix(rest, oldToken)
	if !ok {
		return nil, LineChange{}, fmt.Errorf(text.ReasonImageNotInPlace, node.Line)
	}
	oldLine := lines[node.Line-1]
	lines[node.Line-1] = prefix + newToken + after
	updated := []byte(strings.Join(lines, ""))

	check, err := locateImage(updated, service, text)
	if err != nil {
		return nil, LineChange{}, fmt.Errorf("line %d: rewritten compose file does not parse: %w", node.Line, err)
	}
	if check.Value != newImage {
		return nil, LineChange{}, fmt.Errorf("line %d: rewritten image parses back as %q, want %q", node.Line, check.Value, newImage)
	}
	change := LineChange{
		LineNo: node.Line,
		Old:    strings.TrimRight(oldLine, "\r\n"),
		New:    strings.TrimRight(lines[node.Line-1], "\r\n"),
	}
	return updated, change, nil
}

// PlanComposeEdits computes the new content of every compose file touched
// by rewrites without writing anything. Errors about a line that cannot be
// rewritten are worded in the language of text.
func PlanComposeEdits(rewrites []Rewrite, text Text) ([]ComposeEdit, error) {
	var edits []ComposeEdit
	index := map[string]int{}
	for _, r := range rewrites {
		path := r.ComposePath()
		i, ok := index[path]
		if !ok {
			src, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", path, err)
			}
			edits = append(edits, ComposeEdit{
				Path:     path,
				Original: src,
				Updated:  src,
			})
			i = len(edits) - 1
			index[path] = i
		}
		updated, change, err := replaceImage(edits[i].Updated, r.ComposeService, r.NewImage(), text)
		if err != nil {
			return nil, fmt.Errorf("%s service %s: %w", path, r.ComposeService, err)
		}
		edits[i].Updated = updated
		edits[i].Changes = append(edits[i].Changes, change)
	}
	return edits, nil
}

// fileSnapshot remembers a file's content before it was changed, so the
// whole batch can be rolled back.
type fileSnapshot struct {
	path    string
	data    []byte
	existed bool
	backup  string
}

func snapshotFile(path string) (fileSnapshot, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileSnapshot{path: path}, nil
	}
	if err != nil {
		return fileSnapshot{}, fmt.Errorf("read %s: %w", path, err)
	}
	backup, err := BackupFile(path)
	if err != nil {
		return fileSnapshot{}, err
	}
	return fileSnapshot{
		path:    path,
		data:    data,
		existed: true,
		backup:  backup,
	}, nil
}

func (s fileSnapshot) restore() error {
	if !s.existed {
		err := os.Remove(s.path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := WriteFileKeepMode(s.path, s.data); err != nil {
		return err
	}
	return os.Remove(s.backup)
}

// ApplyRewrites writes the compose edits and .env tag lines, then checks
// with `docker compose config` that every rewritten service still resolves
// to the image it used before. Any failure restores all touched files. It
// returns the backup files created.
func ApplyRewrites(ctx context.Context, docker ComposeInspector, edits []ComposeEdit, rewrites []Rewrite) ([]string, error) {
	snapshots, err := writeRewrites(edits, rewrites)
	if err == nil {
		err = verifyRewrites(ctx, docker, rewrites)
	}
	if err == nil {
		var backups []string
		for _, s := range snapshots {
			if s.backup != "" {
				backups = append(backups, s.backup)
			}
		}
		return backups, nil
	}

	var restoreErrs []error
	for _, s := range snapshots {
		if restoreErr := s.restore(); restoreErr != nil {
			restoreErrs = append(restoreErrs, fmt.Errorf("restore %s: %w", s.path, restoreErr))
		}
	}
	if len(restoreErrs) > 0 {
		return nil, fmt.Errorf("%w; restoring the original files failed too: %w", err, errors.Join(restoreErrs...))
	}
	return nil, fmt.Errorf("%w; all changed files were restored", err)
}

func writeRewrites(edits []ComposeEdit, rewrites []Rewrite) ([]fileSnapshot, error) {
	var snapshots []fileSnapshot
	snapshotted := map[string]bool{}
	take := func(path string) error {
		if snapshotted[path] {
			return nil
		}
		s, err := snapshotFile(path)
		if err != nil {
			return err
		}
		snapshotted[path] = true
		snapshots = append(snapshots, s)
		return nil
	}

	for _, e := range edits {
		if err := take(e.Path); err != nil {
			return snapshots, err
		}
		if err := WriteFileKeepMode(e.Path, e.Updated); err != nil {
			return snapshots, err
		}
	}
	for _, r := range rewrites {
		if err := take(r.EnvFile()); err != nil {
			return snapshots, err
		}
		if err := dotenv.SetLine(r.EnvFile(), r.TagEnv, r.CurrentTag); err != nil {
			return snapshots, err
		}
	}
	return snapshots, nil
}

func verifyRewrites(ctx context.Context, docker ComposeInspector, rewrites []Rewrite) error {
	resolved := map[string]map[string]string{}
	for _, r := range rewrites {
		path := r.ComposePath()
		images, ok := resolved[path]
		if !ok {
			var err error
			images, err = docker.ResolvedServiceImages(ctx, r.Dir, r.File)
			if err != nil {
				return fmt.Errorf("verify %s: %w", path, err)
			}
			resolved[path] = images
		}
		want := r.Ref.Repo + ":" + r.CurrentTag
		if got := images[r.ComposeService]; withDefaultTag(got) != withDefaultTag(want) {
			return fmt.Errorf("verify %s: service %s now resolves to %q, want %q", path, r.ComposeService, got, want)
		}
	}
	return nil
}

// withDefaultTag appends docker's implicit ":latest" to an untagged image.
func withDefaultTag(image string) string {
	if _, tag, ok := strings.CutLast(image, ":"); ok && !strings.Contains(tag, "/") {
		return image
	}
	return image + ":" + defaultTag
}

// BackupFile copies path to path.bak, or path.bak.N if that is taken, and
// returns the backup's path.
func BackupFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", path, err)
	}
	backup := path + ".bak"
	for n := 1; ; n++ {
		f, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if errors.Is(err, os.ErrExist) {
			backup = fmt.Sprintf("%s.bak.%d", path, n)
			continue
		}
		if err != nil {
			return "", fmt.Errorf("create backup of %s: %w", path, err)
		}
		_, writeErr := f.Write(data)
		closeErr := f.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			return "", fmt.Errorf("write backup %s: %w", backup, err)
		}
		return backup, nil
	}
}

// WriteFileKeepMode writes data to path, keeping the existing file's
// permissions or using 0644 for a new file.
func WriteFileKeepMode(path string, data []byte) error {
	mode := os.FileMode(defaultFileMode)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
