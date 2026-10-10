package setup

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/krwu/compose-deploy-agent/internal/config"
)

// setupManifest runs step 4 and reports whether services.yaml exists
// afterwards.
func (wizard *Wizard) setupManifest(ctx context.Context) (bool, error) {
	text := wizard.prompt.text
	path := wizard.opts.ManifestPath
	_, statErr := os.Stat(path)
	exists := statErr == nil

	if wizard.opts.Yes {
		if exists {
			wizard.prompt.Printf(text.KeepExisting, path)
		} else {
			wizard.prompt.Printf(text.ManifestSkippedYesMode, path)
		}
		return exists, nil
	}

	question := text.ConfirmScan
	if exists {
		wizard.prompt.Printf(text.ManifestExists, path)
		question = text.ConfirmRegenerate
	}
	scan, err := wizard.prompt.Confirm(question, !exists)
	if err != nil {
		return false, err
	}
	if !scan {
		return exists, nil
	}

	var existing *ExistingManifest
	if exists {
		existing, err = LoadExistingManifest(path)
		if err != nil {
			wizard.prompt.Printf(text.ManifestParseFailed, path, err)
		}
	}
	candidates, err := DiscoverCandidates(ctx, wizard.docker, text)
	if err != nil {
		wizard.prompt.Printf(text.ListProjectsFailed, err, path)
		return exists, nil
	}
	plan, err := wizard.planManifest(candidates, existing)
	if err != nil {
		return false, err
	}
	if len(plan.selected) == 0 && len(plan.kept) == 0 {
		wizard.prompt.Printf(text.NoServiceSelected, path)
		return exists, nil
	}
	written, err := wizard.generateManifest(ctx, plan)
	if err != nil {
		return false, err
	}
	return exists || written, nil
}

// manifestPlan is what services.yaml is regenerated from.
type manifestPlan struct {
	selected []Candidate
	names    []string
	// kept are entries of the existing manifest carried over unchanged.
	kept map[string]config.Service
}

// planManifest asks which services to manage. Services the existing
// manifest already manages are selected by default and keep their logical
// names; they are rewritten to {SERVICE}_TAG like any other service, since
// an old tag variable could collide with a generated one.
func (wizard *Wizard) planManifest(candidates []Candidate, existing *ExistingManifest) (manifestPlan, error) {
	var selectable []Candidate
	for _, c := range candidates {
		if c.Unsupported == "" {
			selectable = append(selectable, c)
		}
	}
	chosen, err := wizard.selectServices(candidates, selectable, existing)
	if err != nil {
		return manifestPlan{}, err
	}
	selected, err := wizard.confirmDropped(selectable, chosen, existing)
	if err != nil {
		return manifestPlan{}, err
	}
	kept, err := wizard.confirmKept(existing.Unmatched(selectable))
	if err != nil {
		return manifestPlan{}, err
	}
	return manifestPlan{
		selected: selected,
		names:    LogicalNames(selected, existing),
		kept:     kept,
	}, nil
}

func (wizard *Wizard) selectServices(candidates, selectable []Candidate, existing *ExistingManifest) ([]bool, error) {
	text := wizard.prompt.text
	var managed []string
	for i, c := range selectable {
		status := text.StatusReady
		if c.NeedsRewrite() {
			status = fmt.Sprintf(text.StatusRewrite, c.TagEnv)
		}
		if name, ok := existing.NameOf(c); ok {
			status = fmt.Sprintf(text.StatusManaged, name, status)
			managed = append(managed, strconv.Itoa(i+1))
		}
		wizard.prompt.Printf("  %-5s %-28s %s\n        %s\n", fmt.Sprintf("[%d]", i+1), c.Label(), c.RawImage, status)
	}
	wizard.printUnsupported(candidates)
	if len(selectable) == 0 {
		wizard.prompt.Print(text.NoManageableService)
		return nil, nil
	}

	answer, err := wizard.prompt.AskValid(text.AskServices, strings.Join(managed, ","), func(s string) error {
		_, err := ParseSelection(s, len(selectable), text)
		return err
	})
	if err != nil {
		return nil, err
	}
	indexes, err := ParseSelection(answer, len(selectable), text)
	if err != nil {
		return nil, err
	}
	chosen := make([]bool, len(selectable))
	for _, i := range indexes {
		chosen[i] = true
	}
	return chosen, nil
}

// confirmDropped warns about managed services left out of the selection:
// regenerating services.yaml without them stops the agent from deploying
// them. Unless the user confirms, they are selected again.
func (wizard *Wizard) confirmDropped(selectable []Candidate, chosen []bool, existing *ExistingManifest) ([]Candidate, error) {
	text := wizard.prompt.text
	var dropped []int
	for i, c := range selectable {
		if _, ok := existing.NameOf(c); ok && !chosen[i] {
			dropped = append(dropped, i)
		}
	}
	if len(dropped) > 0 {
		wizard.prompt.Print(text.DroppedHeader)
		for _, i := range dropped {
			name, _ := existing.NameOf(selectable[i])
			wizard.prompt.Printf("    - %s (%s)\n", name, selectable[i].Label())
		}
		remove, err := wizard.prompt.Confirm(text.ConfirmRemoveDropped, false)
		if err != nil {
			return nil, err
		}
		if !remove {
			for _, i := range dropped {
				chosen[i] = true
			}
		}
	}

	var selected []Candidate
	for i, c := range selectable {
		if chosen[i] {
			selected = append(selected, c)
		}
	}
	return selected, nil
}

// confirmKept offers to carry over managed services that could not be
// matched to a selectable compose service.
func (w *Wizard) confirmKept(unmatched map[string]config.Service) (map[string]config.Service, error) {
	if len(unmatched) == 0 {
		return nil, nil
	}
	text := w.prompt.text
	w.prompt.Print(text.UnmatchedHeader)
	for _, name := range slices.Sorted(maps.Keys(unmatched)) {
		svc := unmatched[name]
		w.prompt.Printf(text.UnmatchedItem, name, svc.ComposeService, svc.ComposeFile())
	}
	keep, err := w.prompt.Confirm(text.ConfirmKeepUnmatched, true)
	if err != nil {
		return nil, err
	}
	if !keep {
		return nil, nil
	}
	return unmatched, nil
}

func (w *Wizard) printUnsupported(candidates []Candidate) {
	text := w.prompt.text
	header := false
	for _, c := range candidates {
		if c.Unsupported == "" {
			continue
		}
		if !header {
			w.prompt.Print(text.NotSelectable)
			header = true
		}
		w.prompt.Printf("  %-5s %-28s %s\n", " -", c.Label(), c.Unsupported)
		if c.TagEnv != "" && c.RawImage != "" {
			w.prompt.Printf(text.ManualRewriteHint, c.TagEnv)
		}
	}
}

// generateManifest previews every change for the selected services and,
// once confirmed, rewrites compose files and writes services.yaml.
func (wizard *Wizard) generateManifest(ctx context.Context, plan manifestPlan) (bool, error) {
	text := wizard.prompt.text
	rewrites, err := wizard.planRewrites(plan.selected)
	if err != nil {
		return false, err
	}
	edits, err := PlanComposeEdits(rewrites, text)
	if err != nil {
		return false, err
	}
	data, err := BuildManifest(plan.selected, plan.names, plan.kept)
	if err != nil {
		return false, err
	}

	wizard.previewChanges(edits, rewrites, data)
	apply, err := wizard.prompt.Confirm(text.ConfirmApply, true)
	if err != nil {
		return false, err
	}
	if !apply {
		wizard.prompt.Print(text.NothingChanged)
		return false, nil
	}

	backups, err := ApplyRewrites(ctx, wizard.docker, edits, rewrites)
	if err != nil {
		return false, err
	}
	for _, b := range backups {
		wizard.prompt.Printf(text.BackedUp, b)
	}
	if err := wizard.writeManifest(data); err != nil {
		return false, err
	}
	return true, nil
}

func (wizard *Wizard) planRewrites(selected []Candidate) ([]Rewrite, error) {
	text := wizard.prompt.text
	var rewrites []Rewrite
	for _, c := range selected {
		if !c.NeedsRewrite() {
			continue
		}
		tag, known, err := CurrentTag(c)
		if err != nil {
			return nil, err
		}
		if !known {
			question := fmt.Sprintf(text.AskCurrentTag, c.Label(), c.Ref.Var, filepath.Join(c.Dir, ".env"))
			tag, err = wizard.prompt.AskValid(question, "", func(s string) error {
				return ValidateTagValue(s, text)
			})
			if err != nil {
				return nil, err
			}
		}
		rewrites = append(rewrites, Rewrite{
			Candidate:  c,
			CurrentTag: tag,
		})
	}
	return rewrites, nil
}

func (w *Wizard) previewChanges(edits []ComposeEdit, rewrites []Rewrite, manifest []byte) {
	text := w.prompt.text
	w.prompt.Print(text.PreviewHeader)
	for _, e := range edits {
		w.prompt.Printf(text.PreviewComposeFile, e.Path)
		for _, c := range e.Changes {
			w.prompt.Printf(text.PreviewLine, c.LineNo, strings.TrimSpace(c.Old), strings.TrimSpace(c.New))
		}
	}
	lastEnvFile := ""
	for _, r := range rewrites {
		if r.EnvFile() != lastEnvFile {
			w.prompt.Printf("  %s\n", r.EnvFile())
			lastEnvFile = r.EnvFile()
		}
		w.prompt.Printf("      + %s=%s\n", r.TagEnv, r.CurrentTag)
	}
	w.prompt.Printf("  %s\n", w.opts.ManifestPath)
	for line := range strings.SplitSeq(strings.TrimRight(string(manifest), "\n"), "\n") {
		w.prompt.Printf("      %s\n", line)
	}
}

func (w *Wizard) writeManifest(data []byte) error {
	text := w.prompt.text
	path := w.opts.ManifestPath
	if _, err := os.Stat(path); err == nil {
		backup, err := BackupFile(path)
		if err != nil {
			return err
		}
		w.prompt.Printf(text.BackedUp, backup)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := WriteFileKeepMode(path, data); err != nil {
		return err
	}
	w.prompt.Printf(text.Wrote, path)
	return nil
}
