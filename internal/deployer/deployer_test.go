package deployer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/krwu/compose-deploy-agent/internal/api"
	"github.com/krwu/compose-deploy-agent/internal/config"
	"github.com/krwu/compose-deploy-agent/internal/dockerx"
	"github.com/krwu/compose-deploy-agent/internal/dotenv"
	"github.com/krwu/compose-deploy-agent/internal/store"
)

func newTestDeployer(t *testing.T, svc config.Service, runner *fakeRunner) (*Deployer, *store.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "agent.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	manifest := &config.Manifest{
		Version:  1,
		Services: map[string]config.Service{"svc": svc},
	}
	docker := dockerx.NewClientWithRunner(runner)
	dep := New(manifest, docker, st)
	dep.SetTimingForTest(2*time.Second, 300*time.Millisecond, 20*time.Millisecond, 50*time.Millisecond)
	return dep, st
}

func newTestService(t *testing.T) config.Service {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte("services:\n  app:\n    image: x\n"), 0o644); err != nil {
		t.Fatalf("write compose.yml: %v", err)
	}
	return config.Service{
		Dir:            dir,
		File:           "compose.yml",
		ComposeService: "app",
		Image:          "registry.example.com/app",
	}
}

func awaitTerminal(t *testing.T, dep *Deployer, deployID string) api.DeployStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, err := dep.GetDeploy(deployID)
		if err != nil {
			t.Fatalf("get deploy: %v", err)
		}
		if status.State.IsTerminal() {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("deploy %s did not reach a terminal state in time (last state=%s)", deployID, status.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDeployer_SuccessFromCleanSlate(t *testing.T) {
	svc := newTestService(t)
	runner := newFakeRunner(svc.Image)
	// No prior running container: first-ever deploy of this service.
	runner.queueUp(upOutcome{
		tag:            "abc1234",
		imageID:        "sha256-abc1234",
		hasHealthcheck: true,
		healthy:        true,
		running:        true,
	})

	dep, st := newTestDeployer(t, svc, runner)
	deployID, reused, err := dep.StartDeploy(context.Background(), "svc", "abc1234")
	if err != nil {
		t.Fatalf("StartDeploy: %v", err)
	}
	if reused {
		t.Fatalf("expected a new deploy, got reused")
	}

	status := awaitTerminal(t, dep, deployID)
	if status.Result != api.ResultSuccess {
		t.Fatalf("expected success, got result=%s message=%s", status.Result, status.Message)
	}
	if status.Rollback != api.RollbackNone {
		t.Fatalf("expected no rollback, got %s", status.Rollback)
	}

	lh, err := st.GetLastHealthy("svc")
	if err != nil {
		t.Fatalf("get last_healthy: %v", err)
	}
	if lh.Tag != "abc1234" {
		t.Fatalf("expected last_healthy tag abc1234, got %s", lh.Tag)
	}

	envTag, found, err := dotenv.GetLine(svc.EnvFile(), "IMAGE_TAG")
	if err != nil {
		t.Fatalf("expected .env to contain IMAGE_TAG, found=%v err=%v", found, err)
	}
	if !found {
		t.Fatalf("expected .env to contain IMAGE_TAG, found=%v err=%v", found, err)
	}
	if envTag != "abc1234" {
		t.Fatalf("expected .env IMAGE_TAG=abc1234, got %s", envTag)
	}
}

func TestDeployer_Noop(t *testing.T) {
	svc := newTestService(t)
	runner := newFakeRunner(svc.Image)
	runner.setInitialRunning("abc1234", "sha256-abc1234", true, true, true)

	dep, _ := newTestDeployer(t, svc, runner)
	deployID, _, err := dep.StartDeploy(context.Background(), "svc", "abc1234")
	if err != nil {
		t.Fatalf("StartDeploy: %v", err)
	}

	status := awaitTerminal(t, dep, deployID)
	if status.Result != api.ResultNoop {
		t.Fatalf("expected noop, got result=%s message=%s", status.Result, status.Message)
	}
}

func TestDeployer_PullFailed(t *testing.T) {
	svc := newTestService(t)
	runner := newFakeRunner(svc.Image)
	runner.setInitialRunning("abc1234", "sha256-abc1234", true, true, true)
	runner.pullErrByTag["def5678"] = errors.New("unauthorized")

	dep, _ := newTestDeployer(t, svc, runner)
	deployID, _, err := dep.StartDeploy(context.Background(), "svc", "def5678")
	if err != nil {
		t.Fatalf("StartDeploy: %v", err)
	}

	status := awaitTerminal(t, dep, deployID)
	if status.Result != api.ResultPullFailed {
		t.Fatalf("expected pull_failed, got result=%s message=%s", status.Result, status.Message)
	}
	if status.Rollback != api.RollbackNone {
		t.Fatalf("pull_failed must never roll back, got %s", status.Rollback)
	}

	// .env must be untouched: the target tag was never written.
	envTag, found, err := dotenv.GetLine(svc.EnvFile(), "IMAGE_TAG")
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if found && envTag == "def5678" {
		t.Fatalf(".env must not be rewritten when pull fails")
	}
}

func TestDeployer_ConfigInvalid(t *testing.T) {
	svc := newTestService(t)
	runner := newFakeRunner(svc.Image)
	runner.setInitialRunning("abc1234", "sha256-abc1234", true, true, true)
	runner.configImageOverride = "registry.example.com/app:wrong-tag"

	dep, _ := newTestDeployer(t, svc, runner)
	deployID, _, err := dep.StartDeploy(context.Background(), "svc", "def5678")
	if err != nil {
		t.Fatalf("StartDeploy: %v", err)
	}

	status := awaitTerminal(t, dep, deployID)
	if status.Result != api.ResultConfigInvalid {
		t.Fatalf("expected config_invalid, got result=%s message=%s", status.Result, status.Message)
	}
}

func TestDeployer_ConfigInvalidOnUnsupportedPullPolicy(t *testing.T) {
	for _, policy := range []string{"never", "build"} {
		t.Run(policy, func(t *testing.T) {
			svc := newTestService(t)
			runner := newFakeRunner(svc.Image)
			runner.setInitialRunning("abc1234", "sha256-abc1234", true, true, true)
			runner.configPullPolicy = policy

			dep, _ := newTestDeployer(t, svc, runner)
			deployID, _, err := dep.StartDeploy(t.Context(), "svc", "def5678")
			if err != nil {
				t.Fatalf("StartDeploy: %v", err)
			}

			status := awaitTerminal(t, dep, deployID)
			if status.Result != api.ResultConfigInvalid {
				t.Fatalf("expected config_invalid, got result=%s message=%s", status.Result, status.Message)
			}
			if pulls := runner.recordedPullArgs(); len(pulls) != 0 {
				t.Fatalf("pre-flight rejection must not pull, got %v", pulls)
			}
			if _, found, _ := dotenv.GetLine(svc.EnvFile(), "IMAGE_TAG"); found {
				t.Fatalf(".env must not be written when pre-flight fails")
			}
		})
	}
}

func TestDeployer_RepushedSameTagIsRedeployed(t *testing.T) {
	svc := newTestService(t)
	runner := newFakeRunner(svc.Image)
	runner.setInitialRunning("test", "sha256-old", true, true, true)
	runner.configPullPolicy = "missing"
	runner.registry[svc.Image+":test"] = "sha256-new"
	runner.queueUp(upOutcome{
		tag:            "test",
		imageID:        "sha256-new",
		hasHealthcheck: true,
		healthy:        true,
		running:        true,
	})

	dep, _ := newTestDeployer(t, svc, runner)
	deployID, _, err := dep.StartDeploy(t.Context(), "svc", "test")
	if err != nil {
		t.Fatalf("StartDeploy: %v", err)
	}

	status := awaitTerminal(t, dep, deployID)
	if status.Result != api.ResultSuccess {
		t.Fatalf("expected success, got result=%s message=%s", status.Result, status.Message)
	}
	if status.ImageID != "sha256-new" {
		t.Fatalf("expected image_id sha256-new, got %s", status.ImageID)
	}
	pulls := runner.recordedPullArgs()
	if len(pulls) != 1 || !slices.Equal(pulls[0], []string{"--policy", "always", "app"}) {
		t.Fatalf("expected a single forced pull, got %v", pulls)
	}
}

func TestDeployer_SameImageUnderNewTagIsNotNoop(t *testing.T) {
	svc := newTestService(t)
	runner := newFakeRunner(svc.Image)
	runner.setInitialRunning("abc1234", "sha256-shared", true, true, true)
	runner.registry[svc.Image+":def5678"] = "sha256-shared"
	runner.queueUp(upOutcome{
		tag:            "def5678",
		imageID:        "sha256-shared",
		hasHealthcheck: true,
		healthy:        true,
		running:        true,
	})

	dep, _ := newTestDeployer(t, svc, runner)
	deployID, _, err := dep.StartDeploy(t.Context(), "svc", "def5678")
	if err != nil {
		t.Fatalf("StartDeploy: %v", err)
	}

	status := awaitTerminal(t, dep, deployID)
	if status.Result != api.ResultSuccess {
		t.Fatalf("expected success, got result=%s message=%s", status.Result, status.Message)
	}
	if tag, _ := runner.currentRunning(); tag != "def5678" {
		t.Fatalf("expected container recreated on tag def5678, still running %s", tag)
	}
}

func TestDeployer_StartFailedRollsBackToHealthySnapshot(t *testing.T) {
	svc := newTestService(t)
	runner := newFakeRunner(svc.Image)
	runner.setInitialRunning("abc1234", "sha256-abc1234", true, true, true)
	// Target up succeeds but never becomes healthy.
	runner.queueUp(upOutcome{
		tag:            "def5678",
		imageID:        "sha256-def5678",
		hasHealthcheck: true,
		healthy:        false,
		running:        true,
	})
	// Rollback up succeeds and is healthy.
	runner.queueUp(upOutcome{
		tag:            "abc1234",
		imageID:        "sha256-abc1234",
		hasHealthcheck: true,
		healthy:        true,
		running:        true,
	})

	dep, _ := newTestDeployer(t, svc, runner)
	deployID, _, err := dep.StartDeploy(context.Background(), "svc", "def5678")
	if err != nil {
		t.Fatalf("StartDeploy: %v", err)
	}

	status := awaitTerminal(t, dep, deployID)
	if status.Result != api.ResultStartFailed {
		t.Fatalf("expected start_failed, got result=%s message=%s", status.Result, status.Message)
	}
	if status.Rollback != api.RollbackRolledBack {
		t.Fatalf("expected rolled_back, got %s (message=%s)", status.Rollback, status.Message)
	}
	if status.RolledBackTo != "abc1234" {
		t.Fatalf("expected rolled back to abc1234, got %s", status.RolledBackTo)
	}

	envTag, found, err := dotenv.GetLine(svc.EnvFile(), "IMAGE_TAG")
	if err != nil {
		t.Fatalf(".env should reflect the rolled-back tag abc1234, got %q found=%v err=%v", envTag, found, err)
	}
	if !found || envTag != "abc1234" {
		t.Fatalf(".env should reflect the rolled-back tag abc1234, got %q found=%v err=%v", envTag, found, err)
	}
}

func TestDeployer_StartFailedNoRollbackTargetOnFirstDeploy(t *testing.T) {
	svc := newTestService(t)
	runner := newFakeRunner(svc.Image)
	// No prior running container and up never becomes healthy.
	runner.queueUp(upOutcome{
		tag:            "abc1234",
		imageID:        "sha256-abc1234",
		hasHealthcheck: true,
		healthy:        false,
		running:        true,
	})

	dep, _ := newTestDeployer(t, svc, runner)
	deployID, _, err := dep.StartDeploy(context.Background(), "svc", "abc1234")
	if err != nil {
		t.Fatalf("StartDeploy: %v", err)
	}

	status := awaitTerminal(t, dep, deployID)
	if status.Result != api.ResultStartFailed {
		t.Fatalf("expected start_failed, got result=%s message=%s", status.Result, status.Message)
	}
	if status.Rollback != api.RollbackNone {
		t.Fatalf("expected no rollback target on first deploy, got %s", status.Rollback)
	}
}

func TestDeployer_StatusOfNeverDeployedServiceDoesNotErrorOnMissingTagEnv(t *testing.T) {
	// Regression test: before the very first deploy, the compose
	// project's .env has no value for tag_env at all, and the compose
	// file's ${TAG:?...} guard makes `docker compose ps` fail unless the
	// agent supplies a placeholder (composeEnvFallback). This is exactly
	// the "reverse calibration: read status failed ... is missing a
	// value" warning seen in production on a freshly added service.
	svc := newTestService(t)
	runner := newFakeRunner(svc.Image)
	// No setInitialRunning call: no container, and (critically) no .env
	// file has ever been written for this service either.

	dep, _ := newTestDeployer(t, svc, runner)
	status, err := dep.GetServiceStatus(context.Background(), "svc")
	if err != nil {
		t.Fatalf("GetServiceStatus should not fail for a never-deployed service: %v", err)
	}
	if status.Running.Tag != "" || status.Running.ImageID != "" {
		t.Fatalf("expected no running info, got %+v", status.Running)
	}
	if status.EnvTag != "" {
		t.Fatalf("expected no .env tag yet, got %q", status.EnvTag)
	}
}

func TestDeployer_RejectsUnknownServiceAndBadTag(t *testing.T) {
	svc := newTestService(t)
	runner := newFakeRunner(svc.Image)
	dep, _ := newTestDeployer(t, svc, runner)

	if _, _, err := dep.StartDeploy(context.Background(), "does-not-exist", "abc1234"); err == nil {
		t.Fatalf("expected ErrServiceNotFound")
	} else if _, ok := asServiceNotFound(err); !ok {
		t.Fatalf("expected ErrServiceNotFound, got %v", err)
	}

	if _, _, err := dep.StartDeploy(context.Background(), "svc", "1.0.0"); err == nil {
		t.Fatalf("expected ErrInvalidTag for '1.0.0'")
	} else if _, ok := asInvalidTag(err); !ok {
		t.Fatalf("expected ErrInvalidTag, got %v", err)
	}
}

func TestDeployer_ConcurrentRequestsDedupAndConflict(t *testing.T) {
	svc := newTestService(t)
	runner := newFakeRunner(svc.Image)
	runner.setInitialRunning("abc1234", "sha256-abc1234", true, true, true)
	runner.queueUp(upOutcome{
		tag:            "def5678",
		imageID:        "sha256-def5678",
		hasHealthcheck: true,
		healthy:        true,
		running:        true,
	})

	dep, _ := newTestDeployer(t, svc, runner)
	id1, reused1, err := dep.StartDeploy(context.Background(), "svc", "def5678")
	if err != nil {
		t.Fatalf("StartDeploy: %v", err)
	}
	if reused1 {
		t.Fatalf("first request should not be reused")
	}

	// Same tag while in flight: must be deduped onto the same deploy_id.
	id2, reused2, err := dep.StartDeploy(context.Background(), "svc", "def5678")
	if err != nil {
		t.Fatalf("StartDeploy (dedup): %v", err)
	}
	if !reused2 || id2 != id1 {
		t.Fatalf("expected dedup onto %s, got id=%s reused=%v", id1, id2, reused2)
	}

	// Different tag while in flight: must conflict.
	_, _, err = dep.StartDeploy(context.Background(), "svc", "zzz9999")
	var conflict ErrAlreadyDeploying
	if !errors.As(err, &conflict) {
		t.Fatalf("expected ErrAlreadyDeploying, got %v", err)
	}

	awaitTerminal(t, dep, id1)
}

func TestDeployer_MarkInterruptedOnStartup(t *testing.T) {
	svc := newTestService(t)
	runner := newFakeRunner(svc.Image)
	dep, st := newTestDeployer(t, svc, runner)

	if err := st.CreateDeploy(store.DeployRecord{
		DeployID:  "stale-1",
		Service:   "svc",
		TargetTag: "abc1234",
		StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed stale deploy: %v", err)
	}

	n, err := dep.MarkInterruptedOnStartup()
	if err != nil {
		t.Fatalf("MarkInterruptedOnStartup: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 record marked interrupted, got %d", n)
	}

	status, err := dep.GetDeploy("stale-1")
	if err != nil {
		t.Fatalf("get deploy: %v", err)
	}
	if status.Result != api.ResultInterrupted {
		t.Fatalf("expected interrupted, got %s", status.Result)
	}
}

func asServiceNotFound(err error) (ErrServiceNotFound, bool) {
	var e ErrServiceNotFound
	ok := errors.As(err, &e)
	return e, ok
}

func asInvalidTag(err error) (ErrInvalidTag, bool) {
	var e ErrInvalidTag
	ok := errors.As(err, &e)
	return e, ok
}
