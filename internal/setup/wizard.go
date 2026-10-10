package setup

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/krwu/compose-deploy-agent/internal/ca"
	"github.com/krwu/compose-deploy-agent/internal/dotenv"
)

const (
	// DefaultPort is the agent's default mTLS port.
	DefaultPort = 8866

	bindEnvKey = "DEPLOY_AGENT_BIND"
)

// ErrAborted is returned when the user declines to continue.
var ErrAborted = errors.New("setup aborted")

// Options configures the wizard. Empty values are asked for interactively.
type Options struct {
	EnvFile      string
	ManifestPath string
	DataDir      string
	// BindIP is the IPv4 address serve binds to.
	BindIP string
	// Port is the bind port; 0 means ask, defaulting to DefaultPort.
	Port int
	// ExtraIPs is a comma-separated list of extra certificate IPs and DNS
	// names, used only when ExtraIPsSet (so an explicit empty value means
	// "none").
	ExtraIPs    string
	ExtraIPsSet bool
	// Yes accepts every default, never overwrites existing files and skips
	// services.yaml generation.
	Yes bool
}

// Result is what the wizard left on disk, for the final checks.
type Result struct {
	// BindAddr is the DEPLOY_AGENT_BIND value now in serve.env.
	BindAddr string
	// ManifestExists reports whether services.yaml exists.
	ManifestExists bool
}

// Wizard walks through the first-run configuration of the agent.
type Wizard struct {
	opts      Options
	prompt    *Prompter
	docker    ComposeInspector
	detectIPs func() ([]CandidateIP, error)
}

// New returns a Wizard asking questions through prompt and inspecting
// compose projects through docker.
func New(opts Options, prompt *Prompter, docker ComposeInspector) *Wizard {
	return &Wizard{
		opts:      opts,
		prompt:    prompt,
		docker:    docker,
		detectIPs: DetectIPs,
	}
}

// Run executes steps 1-4: bind address, certificate IPs, serve.env and
// certificates, and services.yaml.
func (wizard *Wizard) Run(ctx context.Context) (Result, error) {
	text := wizard.prompt.text
	existingBind, err := ReadBindAddr(wizard.opts.EnvFile)
	if err != nil {
		return Result{}, err
	}

	wizard.prompt.Print(text.StepBind)
	bindIP, err := wizard.chooseBindIP(existingBind)
	if err != nil {
		return Result{}, err
	}
	port, err := wizard.choosePort(existingBind)
	if err != nil {
		return Result{}, err
	}

	wizard.prompt.Print(text.StepCertIPs)
	certNames, err := wizard.chooseCertNames(bindIP)
	if err != nil {
		return Result{}, err
	}

	wizard.prompt.Print(text.StepFiles)
	bindAddr, err := wizard.writeServeEnv(netip.AddrPortFrom(bindIP, port).String(), existingBind)
	if err != nil {
		return Result{}, err
	}
	if err := wizard.ensureCertificates(certNames); err != nil {
		return Result{}, err
	}

	wizard.prompt.Print(text.StepManifest)
	manifestExists, err := wizard.setupManifest(ctx)
	if err != nil {
		return Result{}, err
	}
	return Result{
		BindAddr:       bindAddr,
		ManifestExists: manifestExists,
	}, nil
}

// ReadBindAddr returns DEPLOY_AGENT_BIND from envFile, or "" if the file or
// the key does not exist.
func ReadBindAddr(envFile string) (string, error) {
	value, _, err := dotenv.GetLine(envFile, bindEnvKey)
	if err != nil {
		return "", err
	}
	return strings.Trim(strings.TrimSpace(value), `"'`), nil
}

func (wizard *Wizard) chooseBindIP(existingBind string) (netip.Addr, error) {
	text := wizard.prompt.text
	if wizard.opts.BindIP != "" {
		wizard.prompt.Printf(text.UsingIPFlag, wizard.opts.BindIP)
		return resolveIPChoice(wizard.opts.BindIP, nil, text)
	}
	candidates, err := wizard.detectIPs()
	if err != nil {
		wizard.prompt.Printf(text.DetectIPsFailed, err)
	}
	if len(candidates) == 0 {
		wizard.prompt.Print(text.NoCandidateIP)
	}
	for i, c := range candidates {
		wizard.prompt.Printf("  [%d] %s\n", i+1, c)
	}

	def := ""
	if existing, err := netip.ParseAddrPort(existingBind); err == nil {
		def = existing.Addr().String()
	}
	if i := slices.IndexFunc(candidates, func(c CandidateIP) bool { return c.Addr.String() == def }); i >= 0 {
		def = strconv.Itoa(i + 1)
	}
	if def == "" && len(candidates) == 1 {
		def = "1"
	}

	answer, err := wizard.prompt.AskValid(text.AskBindIP, def, func(s string) error {
		_, err := resolveIPChoice(s, candidates, text)
		return err
	})
	if err != nil {
		return netip.Addr{}, err
	}
	return resolveIPChoice(answer, candidates, text)
}

func resolveIPChoice(answer string, candidates []CandidateIP, text Text) (netip.Addr, error) {
	if answer == "" {
		return netip.Addr{}, errors.New(text.ErrIPRequired)
	}
	if n, err := strconv.Atoi(answer); err == nil {
		if n < 1 || n > len(candidates) {
			return netip.Addr{}, fmt.Errorf(text.ErrNotInList, n)
		}
		return candidates[n-1].Addr, nil
	}
	addr, err := ParseIPv4(answer, text)
	if err != nil {
		return netip.Addr{}, err
	}
	if addr.IsUnspecified() {
		return netip.Addr{}, errors.New(text.ErrUnspecifiedIP)
	}
	return addr, nil
}

func (w *Wizard) choosePort(existingBind string) (uint16, error) {
	text := w.prompt.text
	if w.opts.Port != 0 {
		return parsePort(strconv.Itoa(w.opts.Port), text)
	}
	def := strconv.Itoa(DefaultPort)
	if existing, err := netip.ParseAddrPort(existingBind); err == nil {
		def = strconv.Itoa(int(existing.Port()))
	}
	answer, err := w.prompt.AskValid(text.AskPort, def, func(s string) error {
		_, err := parsePort(s, text)
		return err
	})
	if err != nil {
		return 0, err
	}
	return parsePort(answer, text)
}

func parsePort(s string, text Text) (uint16, error) {
	n, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		return 0, fmt.Errorf(text.ErrBadPort, s)
	}
	if n == 0 {
		return 0, errors.New(text.ErrZeroPort)
	}
	return uint16(n), nil
}

// certNames is the set of names the server certificate should carry.
type certNames struct {
	ips []netip.Addr
	dns []string
}

func (n certNames) list() []string {
	return append(addrStrings(n.ips), n.dns...)
}

func (wizard *Wizard) chooseCertNames(bindIP netip.Addr) (certNames, error) {
	text := wizard.prompt.text
	var def []string
	if mgr := ca.NewManager(wizard.opts.DataDir); mgr.Initialized() {
		if ips, dns, err := mgr.ServerNames(); err == nil {
			def = slices.DeleteFunc(ips, func(ip string) bool { return ip == bindIP.String() })
			def = append(def, dns...)
		}
	}
	wizard.prompt.Printf(text.CertIntro, bindIP)

	for {
		extras, dns, err := wizard.askExtraNames(strings.Join(def, ","))
		if err != nil {
			return certNames{}, err
		}
		names := certNames{
			ips: appendUnique([]netip.Addr{bindIP}, extras...),
			dns: dns,
		}
		wizard.prompt.Print(text.CertWillInclude)
		for i, ip := range names.ips {
			if i == 0 {
				wizard.prompt.Printf(text.CertBindItem, ip)
			} else {
				wizard.prompt.Printf("    - %s\n", ip)
			}
		}
		for _, name := range names.dns {
			wizard.prompt.Printf("    - %s\n", name)
		}
		ok, err := wizard.prompt.Confirm(text.ConfirmIPs, true)
		if err != nil {
			return certNames{}, err
		}
		if ok {
			return names, nil
		}
		if wizard.opts.ExtraIPsSet {
			return certNames{}, ErrAborted
		}
	}
}

func (w *Wizard) askExtraNames(def string) ([]netip.Addr, []string, error) {
	text := w.prompt.text
	if w.opts.ExtraIPsSet {
		return parseExtraNames(w.opts.ExtraIPs, text)
	}
	answer, err := w.prompt.AskValid(text.AskExtraIPs, def, func(s string) error {
		_, _, err := parseExtraNames(s, text)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return parseExtraNames(answer, text)
}

func parseExtraNames(s string, text Text) ([]netip.Addr, []string, error) {
	s = strings.TrimSpace(s)
	// An empty answer adds nothing. "none" still clears a shown default.
	if s == "" || strings.EqualFold(s, "none") {
		return nil, nil, nil
	}
	var ips []netip.Addr
	var dns []string
	for field := range strings.SplitSeq(s, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		ip, host, err := ca.ClassifyServerName(field)
		if err != nil {
			return nil, nil, fmt.Errorf(text.ErrBadName, field)
		}
		if ip != nil {
			addr, ok := netip.AddrFromSlice(ip)
			if !ok {
				return nil, nil, fmt.Errorf(text.ErrBadName, field)
			}
			ips = appendUnique(ips, addr.Unmap())
			continue
		}
		if slices.ContainsFunc(dns, func(existing string) bool {
			return strings.EqualFold(existing, host)
		}) {
			continue
		}
		dns = append(dns, host)
	}
	return ips, dns, nil
}

func serveEnvContent(bindAddr string) []byte {
	return []byte(`# Written by ` + "`deploy-agent setup`" + `. DEPLOY_AGENT_BIND is the internal IP:port
# the mTLS listener binds to; never bind 0.0.0.0, only the internal NIC.
` + bindEnvKey + "=" + bindAddr + "\n")
}

// writeServeEnv writes serve.env and returns the bind address it ends up
// holding, which is the existing one if the user keeps the current file.
func (wizard *Wizard) writeServeEnv(bindAddr, existingBind string) (string, error) {
	text := wizard.prompt.text
	path := wizard.opts.EnvFile
	if _, err := os.Stat(path); err == nil {
		if existingBind == bindAddr {
			wizard.prompt.Printf(text.EnvUnchanged, path, bindEnvKey, bindAddr)
			return bindAddr, nil
		}
		wizard.prompt.Printf(text.EnvExists, path, bindEnvKey, existingBind)
		overwrite, err := wizard.prompt.Confirm(fmt.Sprintf(text.ConfirmOverwriteEnv, bindEnvKey, bindAddr), false)
		if err != nil {
			return "", err
		}
		if !overwrite {
			wizard.prompt.Printf(text.KeepExisting, path)
			return existingBind, nil
		}
		backup, err := BackupFile(path)
		if err != nil {
			return "", err
		}
		wizard.prompt.Printf(text.BackedUp, backup)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := WriteFileKeepMode(path, serveEnvContent(bindAddr)); err != nil {
		return "", err
	}
	wizard.prompt.Printf(text.WroteEnv, path, bindEnvKey, bindAddr)
	return bindAddr, nil
}

// ensureCertificates creates the CA and server certificate, or reissues the
// server certificate when its names differ. An existing CA is never replaced:
// that would invalidate every client certificate issued so far.
func (wizard *Wizard) ensureCertificates(names certNames) error {
	text := wizard.prompt.text
	want := names.list()
	wantList := strings.Join(want, ", ")
	mgr := ca.NewManager(wizard.opts.DataDir)
	if err := os.MkdirAll(wizard.opts.DataDir, 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	if !mgr.Initialized() {
		if err := mgr.Init(want); err != nil {
			return err
		}
		wizard.prompt.Printf(text.CACreated, wizard.opts.DataDir, wantList)
		return nil
	}

	currentIPs, currentDNS, err := mgr.ServerNames()
	if err != nil {
		wizard.prompt.Printf(text.ServerCertUnreadable, err)
	} else {
		current := append(currentIPs, currentDNS...)
		if sameSet(current, want) {
			wizard.prompt.Printf(text.ServerCertUnchanged, wantList)
			return nil
		}
		wizard.prompt.Printf(text.ServerCertCovers, strings.Join(current, ", "))
	}
	reissue, err := wizard.prompt.Confirm(fmt.Sprintf(text.ConfirmReissue, wantList), true)
	if err != nil {
		return err
	}
	if !reissue {
		wizard.prompt.Print(text.KeepServerCert)
		return nil
	}
	if err := mgr.ReissueServer(want); err != nil {
		return err
	}
	wizard.prompt.Printf(text.ServerCertReissued, wantList)
	return nil
}

func sameSet(a, b []string) bool {
	norm := func(in []string) []string {
		out := make([]string, len(in))
		for i, s := range in {
			out[i] = strings.ToLower(s)
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	return slices.Equal(norm(a), norm(b))
}
