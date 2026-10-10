package setup

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/krwu/compose-deploy-agent/internal/config"
	"github.com/krwu/compose-deploy-agent/internal/dockerx"
	"gopkg.in/yaml.v3"
)

// defaultTag is what docker uses when an image reference has no tag.
const defaultTag = "latest"

// requiredOp is the compose interpolation operator that fails when the
// variable is unset or empty, so a missing tag can never deploy silently.
const requiredOp = ":?"

// tagExprPattern matches a tag that is exactly one variable reference:
// ${VAR}, ${VAR:?msg}, ${VAR?msg}, ${VAR:-default}, ${VAR-default} or $VAR.
var tagExprPattern = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)(?:(:?[-?])([^}]*))?\}$|^\$([A-Za-z_][A-Za-z0-9_]*)$`)

// ComposeInspector is the subset of dockerx.Client the wizard uses to
// discover compose services and verify rewritten compose files.
type ComposeInspector interface {
	ListProjects(ctx context.Context) ([]dockerx.ComposeProject, error)
	RawServiceImages(ctx context.Context, dir, file string) (map[string]string, error)
	ResolvedServiceImages(ctx context.Context, dir, file string) (map[string]string, error)
}

// TagEnvName returns the tag variable the wizard assigns to a compose
// service: the service name upper-cased with every character outside
// [A-Z0-9] replaced by '_', followed by "_TAG" (lss-web -> LSS_WEB_TAG).
func TagEnvName(composeService string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(composeService) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	name := b.String()
	if name == "" || (name[0] >= '0' && name[0] <= '9') {
		name = "_" + name
	}
	return name + "_TAG"
}

// GuardedImage returns the image reference the wizard writes into a compose
// file: repo tagged by tagEnv, failing loudly when tagEnv is unset.
func GuardedImage(repo, tagEnv string) string {
	return repo + ":${" + tagEnv + requiredOp + tagEnv + " is required}"
}

// ImageRef is a compose image reference, as written before interpolation.
type ImageRef struct {
	// Repo is the image repository without tag.
	Repo string
	// Tag is the literal tag; empty when the tag is a variable or omitted.
	Tag string
	// Var is the variable the tag comes from; empty for a literal tag.
	Var string
	// Op is the interpolation operator after Var: "", "-", ":-", "?" or ":?".
	Op string
	// Arg follows Op: a default value for "-"/":-", an error message for "?"/":?".
	Arg string
}

// Ready reports whether the reference already takes its tag from tagEnv
// with the required-variable guard, so no rewrite is needed.
func (r ImageRef) Ready(tagEnv string) bool {
	return r.Var == tagEnv && r.Op == requiredOp
}

// ParseImageRef parses a raw compose image value. The error explains, in
// the language of text, why an image cannot be managed by the agent.
func ParseImageRef(image string, text Text) (ImageRef, error) {
	image = strings.TrimSpace(image)
	if image == "" {
		return ImageRef{}, errors.New(text.ReasonNoImage)
	}
	if strings.Contains(image, "@") {
		return ImageRef{}, errors.New(text.ReasonDigest)
	}
	if i := strings.IndexByte(image, '$'); i >= 0 {
		return parseVariableTag(image, i, text)
	}
	if repo, tag, ok := strings.CutLast(image, ":"); ok && !strings.Contains(tag, "/") {
		return ImageRef{
			Repo: repo,
			Tag:  tag,
		}, nil
	}
	return ImageRef{Repo: image}, nil
}

func parseVariableTag(image string, dollar int, text Text) (ImageRef, error) {
	repo, ok := strings.CutSuffix(image[:dollar], ":")
	if !ok || repo == "" {
		return ImageRef{}, fmt.Errorf(text.ReasonRepoVariable, image)
	}
	m := tagExprPattern.FindStringSubmatch(image[dollar:])
	if m == nil {
		return ImageRef{}, fmt.Errorf(text.ReasonTagExpression, image[dollar:])
	}
	return ImageRef{
		Repo: repo,
		Var:  cmp.Or(m[1], m[4]),
		Op:   m[2],
		Arg:  m[3],
	}, nil
}

// Candidate is a compose service found on this host that could be added to
// services.yaml.
type Candidate struct {
	Project        string
	Dir            string
	File           string
	ComposeService string
	RawImage       string
	Ref            ImageRef
	// TagEnv is the {SERVICE}_TAG variable assigned to the service.
	TagEnv string
	// Unsupported explains why the service cannot be managed; empty if it can.
	Unsupported string
}

// ComposePath returns the absolute path of the candidate's compose file.
func (c Candidate) ComposePath() string {
	return filepath.Join(c.Dir, c.File)
}

// Label identifies the candidate as project/service.
func (c Candidate) Label() string {
	if c.ComposeService == "" {
		return c.Project
	}
	return c.Project + "/" + c.ComposeService
}

// NeedsRewrite reports whether the compose file must be changed before the
// agent can inject the tag through TagEnv.
func (c Candidate) NeedsRewrite() bool {
	return c.Unsupported == "" && !c.Ref.Ready(c.TagEnv)
}

// DiscoverCandidates lists every service of every compose project docker
// knows about, sorted by project and service name. Why a service cannot be
// managed is worded in the language of text.
func DiscoverCandidates(ctx context.Context, docker ComposeInspector, text Text) ([]Candidate, error) {
	projects, err := docker.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(projects, func(a, b dockerx.ComposeProject) int { return cmp.Compare(a.Name, b.Name) })

	var out []Candidate
	for _, p := range projects {
		out = append(out, projectCandidates(ctx, docker, p, text)...)
	}
	return out, nil
}

func projectCandidates(ctx context.Context, docker ComposeInspector, p dockerx.ComposeProject, text Text) []Candidate {
	files := p.ConfigFileList()
	if len(files) != 1 {
		return []Candidate{{
			Project:     p.Name,
			Unsupported: fmt.Sprintf(text.ReasonMultipleFiles, len(files), p.ConfigFiles),
		}}
	}
	base := Candidate{
		Project: p.Name,
		Dir:     filepath.Dir(files[0]),
		File:    filepath.Base(files[0]),
	}

	images, err := docker.RawServiceImages(ctx, base.Dir, base.File)
	if err != nil {
		base.Unsupported = fmt.Sprintf(text.ReasonConfigUnread, err)
		return []Candidate{base}
	}
	src, err := os.ReadFile(base.ComposePath())
	if err != nil {
		base.Unsupported = fmt.Sprintf(text.ReasonFileUnread, err)
		return []Candidate{base}
	}

	var out []Candidate
	for _, svc := range slices.Sorted(maps.Keys(images)) {
		c := base
		c.ComposeService = svc
		c.RawImage = images[svc]
		c.TagEnv = TagEnvName(svc)
		classify(&c, src, text)
		out = append(out, c)
	}
	markTagEnvCollisions(out, text)
	return out
}

func classify(c *Candidate, src []byte, text Text) {
	ref, err := ParseImageRef(c.RawImage, text)
	if err != nil {
		c.Unsupported = err.Error()
		return
	}
	c.Ref = ref
	if ref.Ready(c.TagEnv) {
		return
	}
	if _, err := locateImage(src, c.ComposeService, text); err != nil {
		c.Unsupported = err.Error()
	}
}

// markTagEnvCollisions flags services of one project whose names map to the
// same {SERVICE}_TAG (e.g. a-b and a_b); tag_env must be unique per directory.
func markTagEnvCollisions(candidates []Candidate, text Text) {
	owner := map[string]string{}
	for i := range candidates {
		c := &candidates[i]
		if c.Unsupported != "" {
			continue
		}
		if other, ok := owner[c.TagEnv]; ok {
			c.Unsupported = fmt.Sprintf(text.ReasonTagCollision, c.TagEnv, other)
			continue
		}
		owner[c.TagEnv] = c.ComposeService
	}
}

// ExistingManifest is the services.yaml being regenerated. Services it
// already manages keep their logical names, which pipelines deploy by.
type ExistingManifest struct {
	services map[string]config.Service
	// byCompose maps managedKey(dir, compose service) to the logical name.
	byCompose map[string]string
}

func managedKey(dir, composeService string) string {
	return filepath.Clean(dir) + "\x00" + composeService
}

// LoadExistingManifest parses the manifest at path for regeneration.
func LoadExistingManifest(path string) (*ExistingManifest, error) {
	m, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	e := &ExistingManifest{
		services:  m.Services,
		byCompose: map[string]string{},
	}
	for name, svc := range m.Services {
		e.byCompose[managedKey(svc.Dir, svc.ComposeService)] = name
	}
	return e, nil
}

// NameOf returns the logical name c is managed under, if any. It is safe
// to call on a nil ExistingManifest.
func (e *ExistingManifest) NameOf(c Candidate) (string, bool) {
	if e == nil {
		return "", false
	}
	name, ok := e.byCompose[managedKey(c.Dir, c.ComposeService)]
	return name, ok
}

// Unmatched returns the managed services that match none of candidates,
// e.g. projects docker no longer lists or services that cannot be rewritten.
func (e *ExistingManifest) Unmatched(candidates []Candidate) map[string]config.Service {
	if e == nil {
		return nil
	}
	out := maps.Clone(e.services)
	for _, c := range candidates {
		if name, ok := e.NameOf(c); ok {
			delete(out, name)
		}
	}
	return out
}

// LogicalNames returns the services.yaml name of each selected candidate:
// its existing name if already managed, otherwise its compose service name,
// prefixed with "<project>-" when that name is shared by another selected
// candidate or already used in the existing manifest.
func LogicalNames(selected []Candidate, existing *ExistingManifest) []string {
	taken := map[string]bool{}
	count := map[string]int{}
	for _, c := range selected {
		if name, ok := existing.NameOf(c); ok {
			taken[name] = true
		} else {
			count[c.ComposeService]++
		}
	}
	if existing != nil {
		for name := range existing.services {
			taken[name] = true
		}
	}

	names := make([]string, len(selected))
	for i, c := range selected {
		if name, ok := existing.NameOf(c); ok {
			names[i] = name
			continue
		}
		names[i] = c.ComposeService
		if count[c.ComposeService] > 1 || taken[c.ComposeService] {
			names[i] = c.Project + "-" + c.ComposeService
		}
	}
	return names
}

const manifestHeader = `# Generated by ` + "`deploy-agent setup`" + `; see deploy/services.example.yaml in
# the deploy-agent repository for the full format. Restart deploy-agent
# after editing: systemctl restart deploy-agent
`

// BuildManifest renders services.yaml for the selected candidates, named
// by names, plus the kept entries unchanged, and validates the result with
// the same parser serve uses.
func BuildManifest(selected []Candidate, names []string, kept map[string]config.Service) ([]byte, error) {
	m := config.Manifest{
		Version:  1,
		Services: maps.Clone(kept),
	}
	if m.Services == nil {
		m.Services = map[string]config.Service{}
	}
	for i, c := range selected {
		if _, dup := m.Services[names[i]]; dup {
			return nil, fmt.Errorf("duplicate service name %q in services.yaml", names[i])
		}
		m.Services[names[i]] = config.Service{
			Dir:            c.Dir,
			File:           c.File,
			ComposeService: c.ComposeService,
			Image:          c.Ref.Repo,
			TagEnv:         c.TagEnv,
		}
	}

	var buf bytes.Buffer
	buf.WriteString(manifestHeader)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("encode services.yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode services.yaml: %w", err)
	}
	if _, err := config.Parse(buf.Bytes()); err != nil {
		return nil, fmt.Errorf("generated services.yaml is invalid: %w", err)
	}
	return buf.Bytes(), nil
}
