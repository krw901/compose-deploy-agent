package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const (
	defaultOwner = "krwu"
	defaultRepo  = "compose-deploy-agent"
	defaultWeb   = "https://github.com"
	defaultAPI   = "https://api.github.com"

	assetName     = "deploy-agent"
	checksumAsset = "deploy-agent.sha256"
	userAgent     = "deploy-agent"
)

// Remote reads release artifacts from GitHub Releases. Redirects are
// followed by http.Client's default policy.
type Remote struct {
	Owner      string
	Repo       string
	WebBase    string
	APIBase    string
	HTTPClient *http.Client
}

// LatestVersion returns the tag of the repository's latest GitHub Release.
func (remote *Remote) LatestVersion(ctx context.Context) (Version, error) {
	url := remote.apiBase() + "/repos/" + remote.owner() + "/" + remote.repo() + "/releases/latest"
	resp, err := remote.do(ctx, http.MethodGet, url)
	if err != nil {
		return Version{}, err
	}
	defer resp.Body.Close()

	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.UnmarshalRead(resp.Body, &body); err != nil {
		return Version{}, fmt.Errorf("read latest release: %w", err)
	}
	tag := strings.TrimSpace(body.TagName)
	if tag == "" {
		return Version{}, fmt.Errorf("latest release has an empty tag")
	}
	v, err := ParseVersion(tag)
	if err != nil {
		return Version{}, fmt.Errorf("latest release tag: %w", err)
	}
	return v, nil
}

// Download fetches version's binary into a new temporary file in dir,
// verifies it against the release checksum file, and returns the file's
// path. The caller owns the file, and must remove it if it is not renamed
// into place.
func (remote *Remote) Download(ctx context.Context, version Version, dir string) (string, error) {
	expected, err := remote.checksum(ctx, version)
	if err != nil {
		return "", err
	}

	resp, err := remote.do(ctx, http.MethodGet, remote.assetURL(version, assetName))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	tmp, err := os.CreateTemp(dir, ".deploy-agent-upgrade-*")
	if err != nil {
		return "", fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hash), resp.Body); err != nil {
		tmp.Close()
		return "", fmt.Errorf("download %s: %w", version, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp file: %w", err)
	}
	if actual := hex.EncodeToString(hash.Sum(nil)); actual != expected {
		return "", fmt.Errorf("checksum mismatch for %s: expected %s, got %s", version, expected, actual)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return "", fmt.Errorf("chmod temp file: %w", err)
	}
	success = true
	return tmpPath, nil
}

func (remote *Remote) checksum(ctx context.Context, version Version) (string, error) {
	resp, err := remote.do(ctx, http.MethodGet, remote.assetURL(version, checksumAsset))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", checksumAsset, err)
	}
	sum, err := parseChecksum(string(data))
	if err != nil {
		return "", fmt.Errorf("%s: %w", checksumAsset, err)
	}
	return sum, nil
}

// parseChecksum accepts a bare hex digest or a sha256sum line
// ("<hex>  deploy-agent").
func parseChecksum(text string) (string, error) {
	line := strings.TrimSpace(text)
	if line == "" {
		return "", fmt.Errorf("checksum file is empty")
	}
	field, _, _ := strings.Cut(line, " ")
	field = strings.ToLower(field)
	if len(field) != sha256.Size*2 {
		return "", fmt.Errorf("invalid checksum %q", field)
	}
	if _, err := hex.DecodeString(field); err != nil {
		return "", fmt.Errorf("invalid checksum %q", field)
	}
	return field, nil
}

func (remote *Remote) assetURL(version Version, name string) string {
	return remote.webBase() + "/" + remote.owner() + "/" + remote.repo() +
		"/releases/download/" + version.String() + "/" + name
}

func (remote *Remote) do(ctx context.Context, method, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	client := remote.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, url, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s %s: unexpected status %s", method, url, resp.Status)
	}
	return resp, nil
}

func (remote *Remote) owner() string {
	if remote.Owner == "" {
		return defaultOwner
	}
	return remote.Owner
}

func (remote *Remote) repo() string {
	if remote.Repo == "" {
		return defaultRepo
	}
	return remote.Repo
}

func (remote *Remote) webBase() string {
	if remote.WebBase == "" {
		return defaultWeb
	}
	return strings.TrimSuffix(remote.WebBase, "/")
}

func (remote *Remote) apiBase() string {
	if remote.APIBase == "" {
		return defaultAPI
	}
	return strings.TrimSuffix(remote.APIBase, "/")
}
