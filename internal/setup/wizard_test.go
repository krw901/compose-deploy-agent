package setup

import (
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/krwu/compose-deploy-agent/internal/ca"
	"github.com/krwu/compose-deploy-agent/internal/config"
	"github.com/krwu/compose-deploy-agent/internal/dockerx"
)

type wizardFixture struct {
	opts    Options
	compose string
	docker  *fakeInspector
}

func newWizardFixture(t *testing.T) wizardFixture {
	t.Helper()
	root := t.TempDir()
	projectDir := filepath.Join(root, "srv", "app")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	compose := filepath.Join(projectDir, "compose.yml")
	if err := os.WriteFile(compose, []byte("services:\n  worker:\n    image: repo/worker:1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return wizardFixture{
		opts: Options{
			EnvFile:      filepath.Join(root, "etc", "serve.env"),
			ManifestPath: filepath.Join(root, "etc", "services.yaml"),
			DataDir:      filepath.Join(root, "data"),
		},
		compose: compose,
		docker: &fakeInspector{
			projects: []dockerx.ComposeProject{{
				Name:        "app",
				ConfigFiles: compose,
			}},
			raw:     map[string]map[string]string{compose: {"worker": "repo/worker:1.0"}},
			resolve: interpolatingResolver("worker"),
		},
	}
}

func (fixture wizardFixture) run(t *testing.T, opts Options, input string) (Result, string) {
	t.Helper()
	return fixture.runIn(t, opts, input, english)
}

func (fixture wizardFixture) runIn(t *testing.T, opts Options, input string, text Text) (Result, string) {
	t.Helper()
	var out strings.Builder
	w := New(opts, NewPrompter(strings.NewReader(input), &out, opts.Yes, text), fixture.docker)
	w.detectIPs = func() ([]CandidateIP, error) {
		return []CandidateIP{
			{
				Interface: "eth0",
				Addr:      netip.MustParseAddr("10.0.0.5"),
			},
			{
				Interface: "eth1",
				Addr:      netip.MustParseAddr("10.0.0.6"),
			},
		}, nil
	}
	res, err := w.Run(t.Context())
	if err != nil {
		t.Fatalf("Run: %v\noutput:\n%s", err, out.String())
	}
	return res, out.String()
}

func serverIPs(t *testing.T, dataDir string) []string {
	t.Helper()
	ips, err := ca.NewManager(dataDir).ServerIPs()
	if err != nil {
		t.Fatalf("ServerIPs: %v", err)
	}
	return ips
}

func TestWizard_FirstRunThenRerun(t *testing.T) {
	f := newWizardFixture(t)
	// bind IP #1, default port, one extra IP, confirm, scan, select #1, apply.
	res, _ := f.run(t, f.opts, "1\n\n10.0.0.9\n\n\n1\n\n")
	if res.BindAddr != "10.0.0.5:8866" || !res.ManifestExists {
		t.Fatalf("unexpected result %+v", res)
	}
	if bind, _ := ReadBindAddr(f.opts.EnvFile); bind != "10.0.0.5:8866" {
		t.Fatalf("serve.env bind = %q", bind)
	}
	if got := serverIPs(t, f.opts.DataDir); !slices.Equal(got, []string{"10.0.0.5", "10.0.0.9"}) {
		t.Fatalf("server IPs = %v", got)
	}
	m, err := config.Load(f.opts.ManifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if svc, ok := m.Lookup("worker"); !ok || svc.TagEnv != "WORKER_TAG" || svc.Image != "repo/worker" {
		t.Fatalf("unexpected manifest entry %+v", svc)
	}
	if data, _ := os.ReadFile(f.compose); !strings.Contains(string(data), "${WORKER_TAG:?WORKER_TAG is required}") {
		t.Fatalf("compose not rewritten:\n%s", data)
	}

	// Rerun accepting every default: nothing is overwritten or reissued.
	caKey, err := os.ReadFile(filepath.Join(f.opts.DataDir, "ca", "ca.key"))
	if err != nil {
		t.Fatal(err)
	}
	res, out := f.run(t, f.opts, "\n\n\n\n\n")
	if res.BindAddr != "10.0.0.5:8866" || !res.ManifestExists {
		t.Fatalf("unexpected rerun result %+v", res)
	}
	if !strings.Contains(out, "unchanged") || strings.Contains(out, "Reissue") {
		t.Fatalf("rerun should leave serve.env and certificates alone:\n%s", out)
	}
	if after, _ := os.ReadFile(filepath.Join(f.opts.DataDir, "ca", "ca.key")); string(after) != string(caKey) {
		t.Fatalf("CA key changed on rerun")
	}
}

func TestWizard_Chinese(t *testing.T) {
	f := newWizardFixture(t)
	// Same answers as the first run above, with 是/否 for confirmations.
	res, out := f.runIn(t, f.opts, "1\n\n10.0.0.9\n是\n\n1\n是\n", chinese)
	if !res.ManifestExists {
		t.Fatalf("services.yaml not written:\n%s", out)
	}
	for _, want := range []string{"第 1/4 步", "不需要直接回车", "[Y/n, 默认 y]", "服务端证书将包含", "将改写 compose 文件，改用 WORKER_TAG", "已写入"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "%!") {
		t.Fatalf("badly formatted message in output:\n%s", out)
	}
}

func TestParseExtraNames(t *testing.T) {
	for _, in := range []string{"", "  ", "none", "NONE"} {
		ips, dns, err := parseExtraNames(in, english)
		if err != nil || len(ips) != 0 || len(dns) != 0 {
			t.Fatalf("parseExtraNames(%q) = %v, %v, %v; want nothing", in, ips, dns, err)
		}
	}
	ips, dns, err := parseExtraNames("10.0.0.9, agent.example.com, AGENT.example.com", english)
	if err != nil {
		t.Fatalf("parseExtraNames: %v", err)
	}
	if !slices.Equal(ips, []netip.Addr{netip.MustParseAddr("10.0.0.9")}) || !slices.Equal(dns, []string{"agent.example.com"}) {
		t.Fatalf("parseExtraNames = %v, %v", ips, dns)
	}
	for _, bad := range []string{"10.0.0.256", "bad host", "*.example.com"} {
		if _, _, err := parseExtraNames(bad, english); err == nil {
			t.Errorf("parseExtraNames(%q): expected error", bad)
		}
	}
}

func TestWizard_DNSNameOnServerCert(t *testing.T) {
	f := newWizardFixture(t)
	// bind IP, default port, one DNS name, confirm, skip services.yaml.
	_, out := f.run(t, f.opts, "1\n\nagent.example.com\n\nn\n")
	if !strings.Contains(out, "agent.example.com") {
		t.Fatalf("confirmation did not list the DNS name:\n%s", out)
	}
	if got := serverIPs(t, f.opts.DataDir); !slices.Equal(got, []string{"10.0.0.5"}) {
		t.Fatalf("server IPs = %v", got)
	}
	_, dns, err := ca.NewManager(f.opts.DataDir).ServerNames()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(dns, []string{"agent.example.com"}) {
		t.Fatalf("server DNS names = %v", dns)
	}

	// Enter keeps the DNS name already on the certificate.
	_, out = f.run(t, f.opts, "\n\n\n\nn\n")
	if !strings.Contains(out, "unchanged") {
		t.Fatalf("rerun should keep the certificate:\n%s", out)
	}
	_, dns, err = ca.NewManager(f.opts.DataDir).ServerNames()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(dns, []string{"agent.example.com"}) {
		t.Fatalf("DNS name dropped on rerun: %v", dns)
	}
}

func TestDiscoverCandidates_ChineseReasons(t *testing.T) {
	docker := &fakeInspector{projects: []dockerx.ComposeProject{{Name: "multi", ConfigFiles: "/a.yml,/b.yml"}}}
	got, err := DiscoverCandidates(t.Context(), docker, chinese)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Unsupported, "只支持单文件项目") {
		t.Fatalf("unexpected candidates %+v", got)
	}
}

func TestWizard_RegenerateKeepsManagedServices(t *testing.T) {
	f := newWizardFixture(t)
	manifest := "version: 1\nservices:\n  jobs:\n    dir: " + filepath.Dir(f.compose) +
		"\n    file: compose.yml\n    service: worker\n    image: repo/worker\n    tag_env: OLD_TAG\n"
	if err := os.MkdirAll(filepath.Dir(f.opts.ManifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.opts.ManifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	// bind IP, port, extra IPs, confirm, regenerate, deselect everything,
	// decline removing the managed service, apply.
	_, out := f.run(t, f.opts, "1\n\n\n\ny\nnone\n\n\n")
	if !strings.Contains(out, "managed as jobs") {
		t.Fatalf("managed service not marked:\n%s", out)
	}
	m, err := config.Load(f.opts.ManifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	svc, ok := m.Lookup("jobs")
	if !ok || svc.TagEnv != "WORKER_TAG" || len(m.Services) != 1 {
		t.Fatalf("expected jobs kept under its name with WORKER_TAG, got %+v", m.Services)
	}
	if _, err := os.Stat(f.opts.ManifestPath + ".bak"); err != nil {
		t.Fatalf("existing manifest not backed up: %v", err)
	}
}

func TestWizard_YesReissuesWithoutTouchingFiles(t *testing.T) {
	f := newWizardFixture(t)
	if err := ca.NewManager(f.opts.DataDir).Init([]string{"10.0.0.6", "10.0.0.9"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(f.opts.EnvFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.opts.EnvFile, []byte("DEPLOY_AGENT_BIND=10.0.0.6:8866\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := f.opts
	opts.Yes = true
	opts.BindIP = "10.0.0.5"
	res, _ := f.run(t, opts, "")

	if res.BindAddr != "10.0.0.6:8866" || res.ManifestExists {
		t.Fatalf("unexpected result %+v", res)
	}
	// Existing SANs other than the bind IP are carried over.
	if got := serverIPs(t, f.opts.DataDir); !slices.Equal(got, []string{"10.0.0.5", "10.0.0.6", "10.0.0.9"}) {
		t.Fatalf("server IPs = %v", got)
	}
	if _, err := os.Stat(f.opts.ManifestPath); !os.IsNotExist(err) {
		t.Fatalf("--yes must not generate services.yaml, stat err = %v", err)
	}
}
