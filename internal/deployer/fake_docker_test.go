package deployer

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/krwu/compose-deploy-agent/internal/dockerx"
	"github.com/krwu/compose-deploy-agent/internal/dotenv"
)

// upOutcome describes what a single `docker compose up -d` invocation
// should simulate: either a failure, or a resulting container state.
type upOutcome struct {
	err            error
	tag            string
	imageID        string
	hasHealthcheck bool
	healthy        bool
	running        bool
}

// fakeRunner is an in-memory stand-in for the real docker CLI, driven by
// pre-programmed outcomes so the deployer state machine can be exercised
// without a real daemon.
type fakeRunner struct {
	mu sync.Mutex

	imageBase string

	// images maps "repo:tag" -> image ID, populated by pull.
	images map[string]string
	// registry maps "repo:tag" -> image ID currently in the registry. A
	// pull overwrites the local tag with it, simulating a re-pushed tag.
	registry map[string]string
	// pullErrByTag simulates a pull failure for a specific tag.
	pullErrByTag map[string]error
	// pullArgs records the arguments of every `compose pull` call.
	pullArgs [][]string
	// configImageOverride, if set, is returned verbatim by `compose
	// config` instead of the computed "repo:tag", to simulate a
	// misconfigured compose file (config_invalid).
	configImageOverride string
	// configPullPolicy is returned as the service's pull_policy by
	// `compose config`.
	configPullPolicy string

	// current running container, mutated by `up`.
	containerID    string
	runningImageID string
	runningTag     string
	hasHealthcheck bool
	healthy        bool
	running        bool
	restartCount   int

	upOutcomes []upOutcome

	// refSep joins imageBase and the version: ":" for tags, "@" for digests.
	refSep string
	// execArgs records the full docker argv of every `compose exec` call.
	execArgs [][]string
	// execExitCode, if non-zero, is the exit code `compose exec` returns.
	execExitCode int
	// execHang makes `compose exec` block until its context is done,
	// simulating a command that never finishes.
	execHang bool
	// execStdout and execStderr, if set, replace the scripted output of
	// a failing `compose exec`.
	execStdout string
	execStderr string
	// execEnv records the extra environment passed to every `compose exec`.
	execEnv [][]string
}

// execOutcome is the scripted result of a `compose exec` call.
type execOutcome struct {
	exitCode int
	hang     bool
}

func newFakeRunner(imageBase string) *fakeRunner {
	return &fakeRunner{
		imageBase:    imageBase,
		images:       map[string]string{},
		registry:     map[string]string{},
		pullErrByTag: map[string]error{},
		refSep:       ":",
	}
}

// setInitialRunning seeds the pre-deploy running container state.
func (f *fakeRunner) setInitialRunning(tag, imageID string, hasHealthcheck, healthy, running bool) {
	f.containerID = "container-initial"
	f.runningTag = tag
	f.runningImageID = imageID
	f.hasHealthcheck = hasHealthcheck
	f.healthy = healthy
	f.running = running
	f.images[f.ref(tag)] = imageID
}

func (f *fakeRunner) queueUp(o upOutcome) {
	f.upOutcomes = append(f.upOutcomes, o)
}

func (f *fakeRunner) ref(tag string) string { return f.imageBase + f.refSep + tag }

func (f *fakeRunner) recordedExecArgs() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.execArgs)
}

// runExec simulates `docker compose -f <file> exec ...` without holding the
// runner lock while it blocks, so a hanging exec only ends via ctx.
func (f *fakeRunner) runExec(ctx context.Context, extraEnv, argv []string) (dockerx.Output, error) {
	f.mu.Lock()
	f.execArgs = append(f.execArgs, slices.Clone(argv))
	f.execEnv = append(f.execEnv, slices.Clone(extraEnv))
	outcome := execOutcome{
		exitCode: f.execExitCode,
		hang:     f.execHang,
	}
	stdout, stderr := f.execStdout, cmp.Or(f.execStderr, "preview-init failed")
	f.mu.Unlock()

	if outcome.hang {
		<-ctx.Done()
		return dockerx.Output{ExitCode: -1, Stderr: "signal: killed"}, ctx.Err()
	}
	if outcome.exitCode != 0 {
		err := fmt.Errorf("exit status %d", outcome.exitCode)
		return dockerx.Output{ExitCode: outcome.exitCode, Stdout: stdout, Stderr: stderr}, err
	}
	return dockerx.Output{Stdout: "ok"}, nil
}

func isComposeExec(name string, args []string) bool {
	return name == "docker" && len(args) > 3 && args[0] == "compose" && args[3] == "exec"
}

func (f *fakeRunner) recordedPullArgs() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.pullArgs)
}

func (f *fakeRunner) currentRunning() (tag, imageID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runningTag, f.runningImageID
}

func (runner *fakeRunner) Run(ctx context.Context, dir string, extraEnv []string, name string, args ...string) (dockerx.Output, error) {
	if isComposeExec(name, args) {
		return runner.runExec(ctx, extraEnv, args)
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()

	if name != "docker" {
		return dockerx.Output{}, fmt.Errorf("unexpected command %q", name)
	}
	if len(args) == 0 {
		return dockerx.Output{}, fmt.Errorf("no arguments")
	}

	switch args[0] {
	case "compose":
		return runner.runCompose(dir, extraEnv, args[1:])
	case "image":
		return runner.runImageInspect(args[1:])
	case "inspect":
		return runner.runContainerInspect(args[1:])
	default:
		return dockerx.Output{}, fmt.Errorf("unexpected docker subcommand %q", args[0])
	}
}

func (runner *fakeRunner) runCompose(dir string, extraEnv []string, args []string) (dockerx.Output, error) {
	// args: ["-f", file, subcommand, ...]
	if len(args) < 3 {
		return dockerx.Output{}, fmt.Errorf("unexpected compose args %v", args)
	}
	subcmd := args[2]
	tag := tagFromEnv(extraEnv)

	switch subcmd {
	case "config":
		image := runner.ref(tag)
		if runner.configImageOverride != "" {
			image = runner.configImageOverride
		}
		payload := map[string]any{
			"services": map[string]any{
				"app": map[string]any{"image": image, "pull_policy": runner.configPullPolicy},
			},
		}
		data, _ := json.Marshal(payload)
		return dockerx.Output{Stdout: string(data)}, nil
	case "pull":
		runner.pullArgs = append(runner.pullArgs, args[3:])
		if err := runner.pullErrByTag[tag]; err != nil {
			return dockerx.Output{Stderr: err.Error()}, err
		}
		ref := runner.ref(tag)
		if id, ok := runner.registry[ref]; ok {
			runner.images[ref] = id
		} else if _, ok := runner.images[ref]; !ok {
			runner.images[ref] = "sha256-" + tag
		}
		return dockerx.Output{}, nil
	case "up":
		if len(runner.upOutcomes) == 0 {
			return dockerx.Output{}, fmt.Errorf("no queued up outcome")
		}
		outcome := runner.upOutcomes[0]
		runner.upOutcomes = runner.upOutcomes[1:]
		if outcome.err != nil {
			return dockerx.Output{Stderr: outcome.err.Error()}, outcome.err
		}
		runner.containerID = "container-" + outcome.imageID
		runner.runningImageID = outcome.imageID
		runner.runningTag = outcome.tag
		runner.hasHealthcheck = outcome.hasHealthcheck
		runner.healthy = outcome.healthy
		runner.running = outcome.running
		runner.restartCount = 0
		return dockerx.Output{}, nil
	case "ps":
		// Mirror real `docker compose`: every subcommand fully
		// interpolates the compose file first, and process env vars
		// take precedence over .env but fall back to it when unset. A
		// required tag_env variable with no value *anywhere* (neither
		// process env nor .env) makes even a read-only `ps` fail.
		// Exercises the composeEnvFallback fix (currentSnapshot/
		// probeOnce must always pass a non-empty placeholder when
		// .env has no value yet).
		if tag == "" {
			if envTag, found, _ := dotenv.GetLine(filepath.Join(dir, ".env"), "IMAGE_TAG"); found && envTag != "" {
				tag = envTag
			}
		}
		if tag == "" {
			err := errors.New("exit status 15")
			return dockerx.Output{Stderr: "required variable LSS_API_IMAGE_TAG is missing a value"}, err
		}
		return dockerx.Output{Stdout: runner.containerID}, nil
	default:
		return dockerx.Output{}, fmt.Errorf("unexpected compose subcommand %q", subcmd)
	}
}

func (f *fakeRunner) runImageInspect(args []string) (dockerx.Output, error) {
	// args: ["inspect", ref, "--format", "{{.Id}}"]
	if len(args) < 2 {
		return dockerx.Output{}, fmt.Errorf("missing image ref")
	}
	ref := args[1]
	id, ok := f.images[ref]
	if !ok {
		err := errors.New("exit status 1")
		return dockerx.Output{Stderr: "Error: No such image: " + ref}, err
	}
	return dockerx.Output{Stdout: id}, nil
}

func (runner *fakeRunner) runContainerInspect(args []string) (dockerx.Output, error) {
	if len(args) == 0 || args[0] != runner.containerID {
		return dockerx.Output{}, fmt.Errorf("no such container")
	}
	type health struct {
		Status string `json:"Status"`
	}
	type inspectResult struct {
		Image  string `json:"Image"`
		Config struct {
			Image string `json:"Image"`
		} `json:"Config"`
		State struct {
			Running bool    `json:"Running"`
			Health  *health `json:"Health"`
		} `json:"State"`
		RestartCount int `json:"RestartCount"`
	}
	var r inspectResult
	r.Image = runner.runningImageID
	r.Config.Image = runner.ref(runner.runningTag)
	r.State.Running = runner.running
	r.RestartCount = runner.restartCount
	if runner.hasHealthcheck {
		status := "unhealthy"
		if runner.healthy {
			status = "healthy"
		}
		r.State.Health = &health{Status: status}
	}
	data, _ := json.Marshal([]inspectResult{r})
	return dockerx.Output{Stdout: string(data)}, nil
}

func tagFromEnv(extraEnv []string) string {
	for _, kv := range extraEnv {
		if strings.HasPrefix(kv, "IMAGE_TAG=") {
			return strings.TrimPrefix(kv, "IMAGE_TAG=")
		}
	}
	return ""
}
