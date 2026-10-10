package deployer

import (
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/krwu/compose-deploy-agent/internal/api"
	"github.com/krwu/compose-deploy-agent/internal/config"
	"github.com/krwu/compose-deploy-agent/internal/dotenv"
)

func withPostDeploy(svc config.Service, timeout time.Duration) config.Service {
	svc.PostDeploy = &config.PostDeploy{
		Service: "app",
		User:    "www-data",
		Args:    []string{"php", "artisan", "lsky:preview-init"},
		Timeout: timeout,
	}
	return svc
}

func healthyUp(tag string) upOutcome {
	return upOutcome{
		tag:            tag,
		imageID:        "sha256-" + tag,
		hasHealthcheck: true,
		healthy:        true,
		running:        true,
	}
}

var wantPostDeployArgv = []string{
	"compose", "-f", "compose.yml", "exec", "-T", "-u", "www-data", "app", "php", "artisan", "lsky:preview-init",
}

func TestDeployer_PostDeployRunsFixedArgvAfterReady(t *testing.T) {
	svc := withPostDeploy(newTestService(t), time.Second)
	runner := newFakeRunner(svc.Image)
	runner.setInitialRunning("abc1234", "sha256-abc1234", true, true, true)
	runner.queueUp(healthyUp("def5678"))

	dep, st := newTestDeployer(t, svc, runner)
	deployID, _, err := dep.StartDeploy(t.Context(), "svc", "def5678")
	if err != nil {
		t.Fatalf("StartDeploy: %v", err)
	}

	status := awaitTerminal(t, dep, deployID)
	if status.Result != api.ResultSuccess {
		t.Fatalf("expected success, got result=%s message=%s", status.Result, status.Message)
	}
	execs := runner.recordedExecArgs()
	if len(execs) != 1 || !slices.Equal(execs[0], wantPostDeployArgv) {
		t.Fatalf("expected exactly the manifest argv %v, got %v", wantPostDeployArgv, execs)
	}
	lh, err := st.GetLastHealthy("svc")
	if err != nil {
		t.Fatalf("get last_healthy: %v", err)
	}
	if lh.Tag != "def5678" {
		t.Fatalf("expected last_healthy def5678 after post_deploy success, got %s", lh.Tag)
	}
}

func TestDeployer_PostDeployNonZeroExitRollsBack(t *testing.T) {
	svc := withPostDeploy(newTestService(t), time.Second)
	runner := newFakeRunner(svc.Image)
	runner.setInitialRunning("abc1234", "sha256-abc1234", true, true, true)
	runner.execExitCode = 3
	runner.queueUp(healthyUp("def5678"))
	runner.queueUp(healthyUp("abc1234"))

	dep, st := newTestDeployer(t, svc, runner)
	deployID, _, err := dep.StartDeploy(t.Context(), "svc", "def5678")
	if err != nil {
		t.Fatalf("StartDeploy: %v", err)
	}

	status := awaitTerminal(t, dep, deployID)
	assertPostDeployRolledBack(t, status, "exit code 3")
	assertEnvTag(t, svc, "abc1234")
	lh, err := st.GetLastHealthy("svc")
	if err != nil {
		t.Fatalf("get last_healthy: %v", err)
	}
	if lh.Tag != "abc1234" {
		t.Fatalf("a failed post_deploy must not update last_healthy, got %s", lh.Tag)
	}
	if tag, _ := runner.currentRunning(); tag != "abc1234" {
		t.Fatalf("expected container back on abc1234, running %s", tag)
	}
}

func TestDeployer_PostDeployTimeoutKillsAndRollsBack(t *testing.T) {
	svc := withPostDeploy(newTestService(t), 100*time.Millisecond)
	runner := newFakeRunner(svc.Image)
	runner.setInitialRunning("abc1234", "sha256-abc1234", true, true, true)
	runner.execHang = true
	runner.queueUp(healthyUp("def5678"))
	runner.queueUp(healthyUp("abc1234"))

	dep, _ := newTestDeployer(t, svc, runner)
	start := time.Now()
	deployID, _, err := dep.StartDeploy(t.Context(), "svc", "def5678")
	if err != nil {
		t.Fatalf("StartDeploy: %v", err)
	}

	status := awaitTerminal(t, dep, deployID)
	assertPostDeployRolledBack(t, status, "timed out after 100ms")
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("post_deploy timeout did not cut the hanging command short (took %s)", elapsed)
	}
	assertEnvTag(t, svc, "abc1234")
}

func TestDeployer_PostDeployFailureOnFirstDeployHasNoRollbackTarget(t *testing.T) {
	svc := withPostDeploy(newTestService(t), time.Second)
	runner := newFakeRunner(svc.Image)
	runner.execExitCode = 1
	runner.queueUp(healthyUp("abc1234"))

	dep, _ := newTestDeployer(t, svc, runner)
	deployID, _, err := dep.StartDeploy(t.Context(), "svc", "abc1234")
	if err != nil {
		t.Fatalf("StartDeploy: %v", err)
	}

	status := awaitTerminal(t, dep, deployID)
	if status.Result != api.ResultPostDeployFailed || status.Rollback != api.RollbackNone {
		t.Fatalf("expected post_deploy_failed without rollback, got result=%s rollback=%s message=%s",
			status.Result, status.Rollback, status.Message)
	}
	if !strings.Contains(status.Message, "no rollback target available") {
		t.Fatalf("unexpected message %q", status.Message)
	}
}

func TestDeployer_PostDeploySkippedOnNoop(t *testing.T) {
	svc := withPostDeploy(newTestService(t), time.Second)
	runner := newFakeRunner(svc.Image)
	runner.setInitialRunning("abc1234", "sha256-abc1234", true, true, true)

	dep, _ := newTestDeployer(t, svc, runner)
	deployID, _, err := dep.StartDeploy(t.Context(), "svc", "abc1234")
	if err != nil {
		t.Fatalf("StartDeploy: %v", err)
	}

	status := awaitTerminal(t, dep, deployID)
	if status.Result != api.ResultNoop {
		t.Fatalf("expected noop, got %s", status.Result)
	}
	if execs := runner.recordedExecArgs(); len(execs) != 0 {
		t.Fatalf("post_deploy must not run on noop, got %v", execs)
	}
}

func TestDeployer_PostDeployArgvIgnoresRequestedRef(t *testing.T) {
	svc := withPostDeploy(newTestService(t), time.Second)
	runner := newFakeRunner(svc.Image)
	runner.queueUp(healthyUp("u-root-sh"))

	dep, _ := newTestDeployer(t, svc, runner)
	_, _, err := dep.StartDeploy(t.Context(), "svc", "abc;id")
	if err == nil {
		t.Fatalf("a tag carrying shell syntax must be rejected before anything runs")
	}

	deployID, _, err := dep.StartDeploy(t.Context(), "svc", "u-root-sh")
	if err != nil {
		t.Fatalf("StartDeploy: %v", err)
	}
	awaitTerminal(t, dep, deployID)
	execs := runner.recordedExecArgs()
	if len(execs) != 1 || !slices.Equal(execs[0], wantPostDeployArgv) {
		t.Fatalf("post_deploy argv must come from the manifest only, got %v", execs)
	}
}

func assertPostDeployRolledBack(t *testing.T, status api.DeployStatus, wantMessage string) {
	t.Helper()
	if status.Result != api.ResultPostDeployFailed {
		t.Fatalf("expected post_deploy_failed, got result=%s message=%s", status.Result, status.Message)
	}
	if status.Rollback != api.RollbackRolledBack || status.RolledBackTo != "abc1234" {
		t.Fatalf("expected rolled_back to abc1234, got %s to %q (message=%s)",
			status.Rollback, status.RolledBackTo, status.Message)
	}
	if !strings.Contains(status.Message, wantMessage) {
		t.Fatalf("expected message to contain %q, got %q", wantMessage, status.Message)
	}
}

func assertEnvTag(t *testing.T, svc config.Service, want string) {
	t.Helper()
	got, found, err := dotenv.GetLine(svc.EnvFile(), svc.EffectiveTagEnv())
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if !found || got != want {
		t.Fatalf("expected .env %s=%s, got %q (found=%v)", svc.EffectiveTagEnv(), want, got, found)
	}
}

// TestDeployer_PostDeployRecordKeepsOnlyStderrTail checks what a failed
// post_deploy leaves in the deploy record: the exit code and a capped
// stderr tail, never stdout, the agent's environment, or extra arguments.
func TestDeployer_PostDeployRecordKeepsOnlyStderrTail(t *testing.T) {
	const secret = "s3cr3t-db-password-value"
	t.Setenv("DB_PASSWORD", secret)

	svc := withPostDeploy(newTestService(t), time.Second)
	runner := newFakeRunner(svc.Image)
	runner.setInitialRunning("abc1234", "sha256-abc1234", true, true, true)
	runner.execExitCode = 2
	runner.execStdout = "DB_PASSWORD=" + secret
	runner.execStderr = "HEAD DB_PASSWORD=" + secret + "\n" + strings.Repeat("x", 4096) + "\nmigration failed"
	runner.queueUp(healthyUp("def5678"))
	runner.queueUp(healthyUp("abc1234"))

	dep, _ := newTestDeployer(t, svc, runner)
	deployID, _, err := dep.StartDeploy(t.Context(), "svc", "def5678")
	if err != nil {
		t.Fatalf("StartDeploy: %v", err)
	}
	status := awaitTerminal(t, dep, deployID)
	assertPostDeployRolledBack(t, status, "exit code 2")

	if strings.Contains(status.Message, secret) {
		t.Fatalf("deploy record leaks output beyond the stderr tail: %q", status.Message)
	}
	if !strings.Contains(status.Message, "migration failed") {
		t.Fatalf("expected the stderr tail in the record, got %q", status.Message)
	}
	if len(status.Message) > postDeployOutputLimit+256 {
		t.Fatalf("record message not capped: %d bytes", len(status.Message))
	}
	for _, env := range runner.execEnv {
		if len(env) != 0 {
			t.Fatalf("post_deploy must not pass extra environment, got %v", env)
		}
	}
	execs := runner.recordedExecArgs()
	if len(execs) != 1 || !slices.Equal(execs[0], wantPostDeployArgv) {
		t.Fatalf("post_deploy argv must be exactly the manifest args, got %v", execs)
	}
}

func Test_tailOutput(t *testing.T) {
	short := "  boom \n"
	if got := tailOutput(short); got != "boom" {
		t.Fatalf("expected trimmed output, got %q", got)
	}
	long := strings.Repeat("错", postDeployOutputLimit)
	got := tailOutput(long)
	if len(got) > postDeployOutputLimit+len("...") {
		t.Fatalf("tail not capped: %d bytes", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("tail must stay valid UTF-8")
	}
}
