package deployer

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/krwu/compose-deploy-agent/internal/api"
	"github.com/krwu/compose-deploy-agent/internal/config"
)

var (
	digestA = "sha256:" + strings.Repeat("a", 64)
	digestB = "sha256:" + strings.Repeat("b", 64)
)

func newDigestService(t *testing.T) (config.Service, *fakeRunner) {
	t.Helper()
	svc := newTestService(t)
	svc.ImageRef = config.ImageRefDigest
	runner := newFakeRunner(svc.Image)
	runner.refSep = "@"
	return svc, runner
}

func TestDeployer_DigestDeploySuccess(t *testing.T) {
	svc, runner := newDigestService(t)
	runner.setInitialRunning(digestA, "sha256-imgA", true, true, true)
	runner.queueUp(upOutcome{
		tag:            digestB,
		imageID:        "sha256-" + digestB,
		hasHealthcheck: true,
		healthy:        true,
		running:        true,
	})

	dep, st := newTestDeployer(t, svc, runner)
	deployID, _, err := dep.StartDeployDigest(t.Context(), "svc", digestB)
	if err != nil {
		t.Fatalf("StartDeployDigest: %v", err)
	}

	status := awaitTerminal(t, dep, deployID)
	if status.Result != api.ResultSuccess {
		t.Fatalf("expected success, got result=%s message=%s", status.Result, status.Message)
	}
	assertEnvTag(t, svc, digestB)
	lh, err := st.GetLastHealthy("svc")
	if err != nil {
		t.Fatalf("get last_healthy: %v", err)
	}
	if lh.Tag != digestB {
		t.Fatalf("expected last_healthy %s, got %s", digestB, lh.Tag)
	}
	pulls := runner.recordedPullArgs()
	if len(pulls) != 1 || !slices.Equal(pulls[0], []string{"--policy", "always", "app"}) {
		t.Fatalf("expected a single pull, got %v", pulls)
	}
}

func TestDeployer_DigestNoopWhenAlreadyRunning(t *testing.T) {
	svc, runner := newDigestService(t)
	runner.setInitialRunning(digestA, "sha256-imgA", true, true, true)

	dep, _ := newTestDeployer(t, svc, runner)
	deployID, _, err := dep.StartDeployDigest(t.Context(), "svc", digestA)
	if err != nil {
		t.Fatalf("StartDeployDigest: %v", err)
	}
	status := awaitTerminal(t, dep, deployID)
	if status.Result != api.ResultNoop {
		t.Fatalf("expected noop (running digest parsed from repo@sha256:...), got %s: %s",
			status.Result, status.Message)
	}
}

func TestDeployer_DigestRollbackRestoresPreviousDigest(t *testing.T) {
	svc, runner := newDigestService(t)
	runner.setInitialRunning(digestA, "sha256-imgA", true, true, true)
	runner.queueUp(upOutcome{
		tag:            digestB,
		imageID:        "sha256-" + digestB,
		hasHealthcheck: true,
		healthy:        false,
		running:        true,
	})
	runner.queueUp(upOutcome{
		tag:            digestA,
		imageID:        "sha256-imgA",
		hasHealthcheck: true,
		healthy:        true,
		running:        true,
	})

	dep, _ := newTestDeployer(t, svc, runner)
	deployID, _, err := dep.StartDeployDigest(t.Context(), "svc", digestB)
	if err != nil {
		t.Fatalf("StartDeployDigest: %v", err)
	}

	status := awaitTerminal(t, dep, deployID)
	if status.Rollback != api.RollbackRolledBack || status.RolledBackTo != digestA {
		t.Fatalf("expected rollback to %s, got %s to %q (message=%s)",
			digestA, status.Rollback, status.RolledBackTo, status.Message)
	}
	assertEnvTag(t, svc, digestA)
}

func TestDeployer_DigestModeRejectsTagsAndViceVersa(t *testing.T) {
	svc, runner := newDigestService(t)
	dep, _ := newTestDeployer(t, svc, runner)

	_, _, err := dep.StartDeploy(t.Context(), "svc", "abc1234")
	if _, ok := asInvalidTag(err); !ok {
		t.Fatalf("digest-only service must reject a tag, got %v", err)
	}
	for _, bad := range []string{"sha256:abc", digestA + "\nX=1", strings.ToUpper(digestA), "abc1234"} {
		_, _, err = dep.StartDeployDigest(t.Context(), "svc", bad)
		if _, ok := asInvalidTag(err); !ok {
			t.Errorf("digest %q: expected ErrInvalidTag, got %v", bad, err)
		}
	}

	tagSvc := newTestService(t)
	tagDep, _ := newTestDeployer(t, tagSvc, newFakeRunner(tagSvc.Image))
	_, _, err = tagDep.StartDeployDigest(t.Context(), "svc", digestA)
	var invalid ErrInvalidTag
	if !errors.As(err, &invalid) {
		t.Fatalf("tag service must reject a digest, got %v", err)
	}
}

func TestDeployer_DigestModeIgnoresTagRollbackTarget(t *testing.T) {
	svc, runner := newDigestService(t)
	// The container still runs a tag-referenced image from before the
	// service switched to image_ref: digest.
	runner.refSep = ":"
	runner.setInitialRunning("abc1234", "sha256-abc1234", true, true, true)
	runner.refSep = "@"
	runner.queueUp(upOutcome{
		tag:            digestB,
		imageID:        "sha256-" + digestB,
		hasHealthcheck: true,
		healthy:        false,
		running:        true,
	})

	dep, _ := newTestDeployer(t, svc, runner)
	deployID, _, err := dep.StartDeployDigest(t.Context(), "svc", digestB)
	if err != nil {
		t.Fatalf("StartDeployDigest: %v", err)
	}

	status := awaitTerminal(t, dep, deployID)
	if status.Result != api.ResultStartFailed || status.Rollback != api.RollbackNone {
		t.Fatalf("expected start_failed without rollback, got %s/%s: %s",
			status.Result, status.Rollback, status.Message)
	}
	if !strings.Contains(status.Message, "no usable rollback target") {
		t.Fatalf("unexpected message %q", status.Message)
	}
}
