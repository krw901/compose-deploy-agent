package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/krwu/compose-deploy-agent/internal/dockerx"
	"github.com/krwu/compose-deploy-agent/internal/paths"
	"github.com/krwu/compose-deploy-agent/internal/setup"
)

func runSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	ip := fs.String("ip", "", "internal IPv4 address serve binds to (asked for if empty)")
	port := fs.Int("port", 0, fmt.Sprintf("port serve binds to (default %d)", setup.DefaultPort))
	extraIPs := fs.String("extra-ips", "", "comma-separated extra server-certificate IPs and DNS names, e.g. a proxy IP or agent.example.com")
	yes := fs.Bool("yes", false, "non-interactive: accept defaults, keep existing files, skip services.yaml generation and service start")
	envFile := fs.String("env-file", defaultServeEnvPath, "serve environment file holding "+bindEnvKey)
	manifestPath := fs.String("manifest", paths.DefaultManifestPath, "path to services.yaml")
	dataDir := fs.String("data-dir", paths.DefaultDataDir, "data directory (sqlite db, CA material)")
	lang := fs.String("lang", "auto", "wizard language: zh, en or auto (Chinese when the locale's charset is UTF-8)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	text, err := setup.TextByName(*lang)
	if err != nil {
		return err
	}
	opts := setup.Options{
		EnvFile:      *envFile,
		ManifestPath: *manifestPath,
		DataDir:      *dataDir,
		BindIP:       *ip,
		Port:         *port,
		ExtraIPs:     *extraIPs,
		Yes:          *yes,
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "extra-ips" {
			opts.ExtraIPsSet = true
		}
	})

	if !opts.Yes && !stdinIsTerminal() {
		fmt.Print(text.NonInteractiveHint)
		return nil
	}
	prompt := setup.NewPrompter(os.Stdin, os.Stdout, opts.Yes, text)
	res, err := setup.New(opts, prompt, dockerx.NewClient()).Run(context.Background())
	if err != nil {
		return err
	}
	return finishSetup(prompt, opts, res)
}

// stdinIsTerminal reports whether stdin is a character device other than
// /dev/null, which is what ssh without -t or a pipe never provide.
func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	devNull, err := os.Stat(os.DevNull)
	if err != nil {
		return true
	}
	return !os.SameFile(info, devNull)
}

// finishSetup is step 5: run the serve -t checks and offer to start the
// service.
func finishSetup(prompt *setup.Prompter, opts setup.Options, res setup.Result) error {
	text := prompt.Text()
	prompt.Print(text.FinishChecking)
	if !res.ManifestExists {
		prompt.Printf(text.ManifestMissing, opts.ManifestPath, res.BindAddr, serviceUnit)
		return nil
	}
	cfg, err := loadServeConfig(opts.ManifestPath, opts.DataDir)
	if err != nil {
		return fmt.Errorf("configuration test failed: %w", err)
	}
	notAfter, err := testServeConfig(cfg, res.BindAddr)
	if err != nil {
		return fmt.Errorf("configuration test failed: %w", err)
	}
	prompt.Printf(text.ConfigTestOK, len(cfg.manifest.Services), notAfter.Format(time.DateTime))

	if !usesUnitPaths(opts) {
		prompt.Printf(text.CustomPaths, serviceUnit)
	} else if err := startService(prompt, opts.Yes); err != nil {
		return err
	}
	prompt.Print(text.NextSteps)
	return nil
}

// usesUnitPaths reports whether setup wrote the files the systemd unit
// installed by install.sh actually reads.
func usesUnitPaths(opts setup.Options) bool {
	return opts.EnvFile == defaultServeEnvPath &&
		opts.ManifestPath == paths.DefaultManifestPath &&
		opts.DataDir == paths.DefaultDataDir
}

func startService(prompt *setup.Prompter, nonInteractive bool) error {
	text := prompt.Text()
	if nonInteractive {
		prompt.Printf(text.StartHint, serviceUnit)
		return nil
	}
	active, err := unitActive()
	if err != nil {
		prompt.Printf(text.UnitUnmanageable, err)
		return nil
	}

	question := fmt.Sprintf(text.ConfirmEnable, serviceUnit)
	if active {
		question = fmt.Sprintf(text.ConfirmRestart, serviceUnit)
	}
	ok, err := prompt.Confirm(question, true)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if active {
		if err := restartUnit(); err != nil {
			return err
		}
		prompt.Printf(text.UnitRestarted, serviceUnit)
		return nil
	}
	if err := enableUnit(); err != nil {
		return err
	}
	prompt.Printf(text.UnitEnabled, serviceUnit)
	return nil
}

func enableUnit() error {
	for _, args := range [][]string{{"daemon-reload"}, {"enable", "--now", serviceUnit}} {
		out, err := exec.Command("systemctl", args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}
