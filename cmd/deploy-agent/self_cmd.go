package main

import (
	"context"
	"encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/krwu/compose-deploy-agent/internal/api"
	"github.com/krwu/compose-deploy-agent/internal/dotenv"
	"github.com/krwu/compose-deploy-agent/internal/paths"
	"github.com/krwu/compose-deploy-agent/internal/selfupdate"
	"github.com/krwu/compose-deploy-agent/internal/store"
)

const (
	// exitUpdateAvailable is self-check's exit code when a newer version is
	// published, so scripts can tell it apart from both "up to date" (0) and
	// errors (1).
	exitUpdateAvailable = 10

	serviceUnit         = "deploy-agent"
	defaultServeEnvPath = "/etc/deploy-agent/serve.env"
	bindEnvKey          = "DEPLOY_AGENT_BIND"

	// releaseGOOS/releaseGOARCH is the only platform the release workflow
	// builds; the published binary would not run anywhere else.
	releaseGOOS   = "linux"
	releaseGOARCH = "amd64"

	downloadTimeout      = 5 * time.Minute
	defaultHealthTimeout = time.Minute
	healthPollInterval   = time.Second
	healthRequestTimeout = 2 * time.Second
)

// upgradeOptions carries the serve flags self-upgrade needs to test the new
// binary against, and to verify the restarted service with.
type upgradeOptions struct {
	manifestPath  string
	dataDir       string
	bindAddr      string
	healthzAddr   string
	healthTimeout time.Duration
	force         bool
}

func newRemote() *selfupdate.Remote {
	return &selfupdate.Remote{
		HTTPClient: &http.Client{Timeout: downloadTimeout},
	}
}

// runSelfCheck implements `deploy-agent self-check`: compare this binary's
// version with the latest GitHub Release tag.
func runSelfCheck(args []string) error {
	fs := flag.NewFlagSet("self-check", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	current, err := selfupdate.ParseVersion(version)
	if err != nil {
		return fmt.Errorf("current version %q is not a release version and cannot be compared", version)
	}
	latest, err := newRemote().LatestVersion(context.Background())
	if err != nil {
		return err
	}

	fmt.Printf("current: %s\nlatest:  %s\n", current, latest)
	switch c := current.Compare(latest); {
	case c < 0:
		fmt.Println("a new version is available; run `sudo deploy-agent self-upgrade`")
		os.Exit(exitUpdateAvailable)
	case c > 0:
		fmt.Println("current version is newer than the latest published version")
	default:
		fmt.Println("already up to date")
	}
	return nil
}

// runSelfUpgrade implements `deploy-agent self-upgrade`: download the
// latest version next to the installed binary, have it validate the live
// configuration (`serve -t`), swap it in, restart the systemd service and
// wait for /healthz to report the new version, rolling back otherwise.
func runSelfUpgrade(args []string) error {
	fs := flag.NewFlagSet("self-upgrade", flag.ContinueOnError)
	var opts upgradeOptions
	fs.StringVar(&opts.manifestPath, "manifest", paths.DefaultManifestPath, "services.yaml the service runs with")
	fs.StringVar(&opts.dataDir, "data-dir", paths.DefaultDataDir, "data directory the service runs with")
	envFile := fs.String("env-file", defaultServeEnvPath, "environment file to read "+bindEnvKey+" from when --bind is empty")
	fs.StringVar(&opts.bindAddr, "bind", "", "--bind the service runs with (default: "+bindEnvKey+" from --env-file)")
	fs.StringVar(&opts.healthzAddr, "healthz-bind", defaultHealthzAddr, "--healthz-bind the service runs with")
	fs.DurationVar(&opts.healthTimeout, "health-timeout", defaultHealthTimeout, "how long to wait for the restarted service to report the new version")
	fs.BoolVar(&opts.force, "force", false, "upgrade even if not newer, on a dev build, or with deploys in progress")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := checkUpgradePlatform(); err != nil {
		return err
	}
	if opts.bindAddr == "" {
		bind, err := readBindAddr(*envFile)
		if err != nil {
			return err
		}
		opts.bindAddr = bind
	}

	ctx := context.Background()
	remote := newRemote()
	latest, err := remote.LatestVersion(ctx)
	if err != nil {
		return err
	}
	needed, err := upgradeNeeded(latest, opts.force)
	if err != nil {
		return err
	}
	if !needed {
		fmt.Printf("already up to date (%s)\n", version)
		return nil
	}

	target, err := executablePath()
	if err != nil {
		return err
	}
	active, err := unitActive()
	if err != nil {
		return err
	}

	fmt.Printf("downloading deploy-agent %s ...\n", latest)
	newPath, err := remote.Download(ctx, latest, filepath.Dir(target))
	if err != nil {
		return err
	}
	defer os.Remove(newPath)

	if err := verifyNewBinary(newPath, latest, opts); err != nil {
		return err
	}
	if active && !opts.force {
		if err := checkNoActiveDeploys(opts.dataDir); err != nil {
			return err
		}
	}
	if err := selfupdate.Replace(target, newPath); err != nil {
		return err
	}
	fmt.Printf("installed %s %s (previous binary kept at %s)\n", target, latest, selfupdate.BackupPath(target))

	if !active {
		fmt.Printf("%s service is not running; start it with `systemctl start %s`\n", serviceUnit, serviceUnit)
		return nil
	}
	return restartAndVerify(ctx, target, latest.String(), opts)
}

func checkUpgradePlatform() error {
	if runtime.GOOS != releaseGOOS || runtime.GOARCH != releaseGOARCH {
		return fmt.Errorf("self-upgrade only supports %s/%s, the only published platform (running on %s/%s)",
			releaseGOOS, releaseGOARCH, runtime.GOOS, runtime.GOARCH)
	}
	if os.Geteuid() != 0 {
		return errors.New("self-upgrade must be run as root")
	}
	return nil
}

func readBindAddr(envFile string) (string, error) {
	value, ok, err := dotenv.GetLine(envFile, bindEnvKey)
	if err != nil {
		return "", err
	}
	bind := strings.Trim(strings.TrimSpace(value), `"'`)
	if !ok || bind == "" {
		return "", fmt.Errorf("%s is not set in %s; pass --bind", bindEnvKey, envFile)
	}
	return bind, nil
}

// upgradeNeeded compares this binary's version with latest. Anything other
// than a plain upgrade (dev build, downgrade, reinstall) requires force.
func upgradeNeeded(latest selfupdate.Version, force bool) (bool, error) {
	current, err := selfupdate.ParseVersion(version)
	if err != nil {
		if force {
			return true, nil
		}
		return false, fmt.Errorf("current version %q is not a release version; rerun with --force to install %s", version, latest)
	}
	switch c := current.Compare(latest); {
	case c < 0:
		return true, nil
	case force:
		return true, nil
	case c > 0:
		return false, fmt.Errorf("current version %s is newer than the latest published %s; rerun with --force to downgrade", current, latest)
	default:
		return false, nil
	}
}

func executablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate current executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", exe, err)
	}
	return resolved, nil
}

// unitActive reports whether the systemd service is running, failing if the
// unit is not installed at all.
func unitActive() (bool, error) {
	out, err := exec.Command("systemctl", "show", "--property=LoadState,ActiveState", serviceUnit).Output()
	if err != nil {
		return false, fmt.Errorf("systemctl show %s: %w", serviceUnit, err)
	}
	props := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			props[key] = value
		}
	}
	if props["LoadState"] != "loaded" {
		return false, fmt.Errorf("systemd unit %s.service is not installed (LoadState=%s); install with deploy/install.sh first",
			serviceUnit, props["LoadState"])
	}
	return props["ActiveState"] == "active", nil
}

// verifyNewBinary runs the downloaded binary before it replaces anything:
// it must report the expected version and accept the live configuration.
func verifyNewBinary(path string, want selfupdate.Version, opts upgradeOptions) error {
	out, err := exec.Command(path, "version").Output()
	if err != nil {
		return fmt.Errorf("run downloaded binary: %w", err)
	}
	if got := strings.TrimSpace(string(out)); got != want.String() {
		return fmt.Errorf("downloaded binary reports version %q, want %s", got, want)
	}

	out, err = exec.Command(path, "serve", "-t",
		"--manifest", opts.manifestPath, "--data-dir", opts.dataDir, "--bind", opts.bindAddr).CombinedOutput()
	if err != nil {
		return fmt.Errorf("new version %s rejected the current configuration, nothing was changed: %w\n%s", want, err, out)
	}
	fmt.Print(string(out))
	return nil
}

func checkNoActiveDeploys(dataDir string) error {
	st, err := store.OpenReadOnly(paths.DBPath(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer st.Close()

	n, err := st.CountActiveDeploys()
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%d deploy(s) in progress; restarting now would interrupt them, retry later or rerun with --force", n)
	}
	return nil
}

// restartAndVerify restarts the service on the new binary and, if it does
// not report the new version on /healthz in time, puts the previous binary
// back and restarts again.
func restartAndVerify(ctx context.Context, target, want string, opts upgradeOptions) error {
	err := restartUnit()
	if err == nil {
		err = waitHealthy(ctx, opts.healthzAddr, want, opts.healthTimeout)
	}
	if err == nil {
		fmt.Printf("%s restarted and healthy on %s\n", serviceUnit, want)
		return nil
	}

	fmt.Fprintf(os.Stderr, "new version failed to come up: %v\nrolling back to %s ...\n", err, version)
	if restoreErr := selfupdate.Restore(target); restoreErr != nil {
		return fmt.Errorf("upgrade to %s failed: %w; rollback failed too: %v", want, err, restoreErr)
	}
	if restartErr := restartUnit(); restartErr != nil {
		return fmt.Errorf("upgrade to %s failed: %w; binary restored but restart failed: %v", want, err, restartErr)
	}
	if healthErr := waitHealthy(ctx, opts.healthzAddr, version, opts.healthTimeout); healthErr != nil {
		return fmt.Errorf("upgrade to %s failed: %w; binary restored but service is not healthy: %v", want, err, healthErr)
	}
	return fmt.Errorf("upgrade to %s failed and was rolled back to %s: %w", want, version, err)
}

func restartUnit() error {
	out, err := exec.Command("systemctl", "restart", serviceUnit).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl restart %s: %w: %s", serviceUnit, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// waitHealthy polls /healthz until it reports want. Matching the version,
// not just a 200, rules out answers from a process that was not replaced.
func waitHealthy(ctx context.Context, addr, want string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := &http.Client{Timeout: healthRequestTimeout}

	var lastErr error
	for {
		got, err := healthzVersion(ctx, client, addr)
		switch {
		case err != nil:
			lastErr = err
		case got == want:
			return nil
		default:
			lastErr = fmt.Errorf("healthz reports version %s, want %s", got, want)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("not healthy within %s: %w", timeout, lastErr)
		case <-time.After(healthPollInterval):
		}
	}
}

func healthzVersion(ctx context.Context, client *http.Client, addr string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/healthz", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("healthz returned %s", resp.Status)
	}
	var body api.HealthzResponse
	if err := json.UnmarshalRead(resp.Body, &body); err != nil {
		return "", fmt.Errorf("decode healthz response: %w", err)
	}
	return body.Version, nil
}
