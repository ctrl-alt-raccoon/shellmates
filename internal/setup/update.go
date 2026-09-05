package setup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	DefaultReleaseRepo = "ctrl-alt-raccoon/shellmates"

	maxLatestReleaseSize = 1 << 20
	maxChecksumFileSize  = 1 << 20
	maxReleaseBinarySize = 128 << 20
)

type UpdateOptions struct {
	Version string
	Repo    string
}

type updateDeps struct {
	client       *http.Client
	apiBase      string
	downloadBase string
	goos         string
	goarch       string
	allowHTTP    bool
	install      func(string, string, InstallLayout) (InstallLedger, error)
}

func defaultUpdateDeps() updateDeps {
	allowedHosts := map[string]bool{
		"api.github.com":                       true,
		"github.com":                           true,
		"objects.githubusercontent.com":        true,
		"release-assets.githubusercontent.com": true,
	}
	return updateDeps{
		client: &http.Client{
			Timeout: 5 * time.Minute,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return errors.New("too many redirects")
				}
				if req.URL.Scheme != "https" || !allowedHosts[strings.ToLower(req.URL.Hostname())] {
					return errors.New("redirect target is not an allowed HTTPS release host")
				}
				return nil
			},
		},
		apiBase:      "https://api.github.com",
		downloadBase: "https://github.com",
		goos:         runtime.GOOS,
		goarch:       runtime.GOARCH,
		install:      InstallBinary,
	}
}

func Update(ctx context.Context, opts UpdateOptions, layout InstallLayout) (InstallLedger, error) {
	deps := defaultUpdateDeps()
	// As in the bootstrap installer, the verified candidate owns migrations.
	// An older candidate must reject an unfamiliar ledger instead of the new
	// updater installing it behind a scodex launcher it cannot understand.
	deps.install = func(source, version string, layout InstallLayout) (InstallLedger, error) {
		return installDownloadedRelease(ctx, source, version, layout)
	}
	return updateWithDeps(ctx, opts, layout, deps)
}

func installDownloadedRelease(ctx context.Context, source, version string, layout InstallLayout) (InstallLedger, error) {
	expectedLayout, err := DefaultInstallLayout(layout.BinDir)
	if err != nil {
		return InstallLedger{}, err
	}
	if expectedLayout != layout {
		return InstallLedger{}, errors.New("candidate install layout differs from the current user environment")
	}
	if err := inspectRegularSource(source); err != nil {
		return InstallLedger{}, err
	}
	if err := os.Chmod(source, 0o700); err != nil {
		return InstallLedger{}, err
	}
	cmd := exec.CommandContext(ctx, source, "_install-release", "--source", source, "--version", version, "--bin-dir", layout.BinDir)
	// Downloaded filenames are temporary; product dispatch must not mistake
	// them for a vendor CLI alias. _install-release is always noninteractive.
	cmd.Args[0] = "sclaude"
	if err := cmd.Run(); err != nil {
		return InstallLedger{}, fmt.Errorf("release installer failed (older releases may not support this install ledger): %w", err)
	}
	ledger, err := LoadInstallLedger(ledgerPath(layout))
	if err != nil {
		return InstallLedger{}, err
	}
	digest, err := digestRegularFile(source)
	if err != nil {
		return InstallLedger{}, err
	}
	if ledger.Current != version || ledger.Releases[version] != digest {
		return InstallLedger{}, errors.New("candidate did not publish the expected release ledger")
	}
	if err := preflightLedger(layout, ledger); err != nil {
		return InstallLedger{}, err
	}
	if _, err := os.Lstat(journalPath(layout)); err == nil {
		ledger.Warnings = append(ledger.Warnings, "release activated; installer cleanup is pending and will be retried by the next management operation")
	} else if !errors.Is(err, os.ErrNotExist) {
		ledger.Warnings = append(ledger.Warnings, "release activated; could not inspect pending installer cleanup")
	}
	return ledger, nil
}

func updateWithDeps(ctx context.Context, opts UpdateOptions, layout InstallLayout, deps updateDeps) (InstallLedger, error) {
	if deps.client == nil || deps.install == nil {
		return InstallLedger{}, errors.New("update dependencies are incomplete")
	}
	repo := opts.Repo
	if repo == "" {
		repo = DefaultReleaseRepo
	}
	if err := validateReleaseRepo(repo); err != nil {
		return InstallLedger{}, err
	}
	asset, err := releaseAssetName(deps.goos, deps.goarch)
	if err != nil {
		return InstallLedger{}, err
	}

	version := opts.Version
	if version == "" {
		version, err = fetchLatestReleaseTag(ctx, repo, deps)
		if err != nil {
			return InstallLedger{}, err
		}
	} else if err := validateReleaseTag(version); err != nil {
		return InstallLedger{}, err
	}

	base := strings.TrimRight(deps.downloadBase, "/") + "/" + repo + "/releases/download/" + url.PathEscape(version) + "/"
	manifestURL := base + "SHA256SUMS"
	manifest, err := fetchBounded(ctx, deps, manifestURL, maxChecksumFileSize, "checksum manifest")
	if err != nil {
		return InstallLedger{}, err
	}
	expected, err := parseChecksumManifest(manifest, asset)
	if err != nil {
		return InstallLedger{}, err
	}

	temp, actual, err := downloadReleaseBinary(ctx, deps, base+url.PathEscape(asset))
	if err != nil {
		return InstallLedger{}, err
	}
	path := temp.Name()
	defer os.Remove(path)
	if actual != expected {
		_ = temp.Close()
		return InstallLedger{}, fmt.Errorf("checksum mismatch for %s", asset)
	}
	if err := temp.Close(); err != nil {
		return InstallLedger{}, fmt.Errorf("close downloaded release: %w", err)
	}
	return deps.install(path, version, layout)
}

func validateReleaseRepo(repo string) error {
	if strings.TrimSpace(repo) != repo || strings.Count(repo, "/") != 1 {
		return fmt.Errorf("invalid release repository %q; expected owner/name", repo)
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || !validRepoComponent(owner) || !validRepoComponent(name) {
		return fmt.Errorf("invalid release repository %q; expected owner/name", repo)
	}
	return nil
}

func validRepoComponent(value string) bool {
	if value == "" || value == "." || value == ".." || len(value) > 100 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func releaseAssetName(goos, goarch string) (string, error) {
	if (goos != "darwin" && goos != "linux") || (goarch != "amd64" && goarch != "arm64") {
		return "", fmt.Errorf("unsupported update platform %s/%s", goos, goarch)
	}
	return "sclaude_" + goos + "_" + goarch, nil
}

func fetchLatestReleaseTag(ctx context.Context, repo string, deps updateDeps) (string, error) {
	endpoint := strings.TrimRight(deps.apiBase, "/") + "/repos/" + repo + "/releases/latest"
	data, err := fetchBounded(ctx, deps, endpoint, maxLatestReleaseSize, "latest release metadata")
	if err != nil {
		return "", err
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(&release); err != nil {
		return "", errors.New("parse latest release metadata: invalid JSON")
	}
	if err := requireJSONEOF(decoder); err != nil {
		return "", errors.New("parse latest release metadata: trailing data")
	}
	if err := validateReleaseTag(release.TagName); err != nil {
		return "", errors.New("latest release metadata contains an invalid tag_name")
	}
	return release.TagName, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("trailing JSON value")
	}
	return nil
}

func fetchBounded(ctx context.Context, deps updateDeps, endpoint string, limit int64, description string) ([]byte, error) {
	resp, err := getRelease(ctx, deps, endpoint)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", description, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch %s: HTTP %s", description, resp.Status)
	}
	if resp.ContentLength > limit {
		return nil, fmt.Errorf("fetch %s: response exceeds %s", description, byteLimitName(limit))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", description, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("fetch %s: response exceeds %s", description, byteLimitName(limit))
	}
	return data, nil
}

func getRelease(ctx context.Context, deps updateDeps, endpoint string) (*http.Response, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, errors.New("invalid release URL")
	}
	if deps.allowHTTP {
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return nil, errors.New("release URL uses an unsupported scheme")
		}
	} else if parsed.Scheme != "https" {
		return nil, errors.New("release URL must use HTTPS")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, errors.New("build release request")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "sclaude-updater")
	return deps.client.Do(req)
}

func parseChecksumManifest(data []byte, wanted string) (string, error) {
	if strings.ContainsRune(string(data), '\x00') {
		return "", errors.New("checksum manifest contains invalid data")
	}
	checksums := make(map[string]string)
	lines := strings.Split(string(data), "\n")
	for index, line := range lines {
		if line == "" && index == len(lines)-1 {
			continue
		}
		if strings.TrimSpace(line) == "" {
			return "", fmt.Errorf("invalid checksum manifest line %d", index+1)
		}
		if len(line) < sha256.Size*2+3 {
			return "", fmt.Errorf("invalid checksum manifest line %d", index+1)
		}
		digest := line[:sha256.Size*2]
		separator := line[sha256.Size*2 : sha256.Size*2+2]
		if separator != "  " && separator != " *" {
			return "", fmt.Errorf("invalid checksum manifest line %d", index+1)
		}
		name := line[sha256.Size*2+2:]
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != sha256.Size || len(digest) != sha256.Size*2 {
			return "", fmt.Errorf("invalid SHA-256 digest on checksum manifest line %d", index+1)
		}
		if !validChecksumAssetName(name) {
			return "", fmt.Errorf("invalid asset name on checksum manifest line %d", index+1)
		}
		if _, exists := checksums[name]; exists {
			return "", errors.New("checksum manifest contains a duplicate asset entry")
		}
		checksums[name] = strings.ToLower(digest)
	}
	digest, ok := checksums[wanted]
	if !ok {
		return "", fmt.Errorf("checksum manifest is missing %s", wanted)
	}
	return digest, nil
}

func validChecksumAssetName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func downloadReleaseBinary(ctx context.Context, deps updateDeps, endpoint string) (*os.File, string, error) {
	resp, err := getRelease(ctx, deps, endpoint)
	if err != nil {
		return nil, "", fmt.Errorf("download release binary: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("download release binary: HTTP %s", resp.Status)
	}
	if resp.ContentLength > maxReleaseBinarySize {
		return nil, "", fmt.Errorf("download release binary: response exceeds %s", byteLimitName(maxReleaseBinarySize))
	}
	temp, err := os.CreateTemp("", "sclaude-update-*")
	if err != nil {
		return nil, "", err
	}
	cleanup := func(failure error) (*os.File, string, error) {
		_ = temp.Close()
		_ = os.Remove(temp.Name())
		return nil, "", failure
	}
	if err := temp.Chmod(0o600); err != nil {
		return cleanup(err)
	}
	digest := sha256.New()
	written, err := copyBounded(io.MultiWriter(temp, digest), resp.Body, maxReleaseBinarySize)
	if err != nil {
		return cleanup(fmt.Errorf("download release binary: %w", err))
	}
	if written > maxReleaseBinarySize {
		return cleanup(fmt.Errorf("download release binary: response exceeds %s", byteLimitName(maxReleaseBinarySize)))
	}
	if err := temp.Sync(); err != nil {
		return cleanup(err)
	}
	if _, err := temp.Seek(0, io.SeekStart); err != nil {
		return cleanup(err)
	}
	return temp, hex.EncodeToString(digest.Sum(nil)), nil
}

func copyBounded(destination io.Writer, source io.Reader, limit int64) (int64, error) {
	return io.Copy(destination, io.LimitReader(source, limit+1))
}

func byteLimitName(limit int64) string {
	if limit%(1<<20) == 0 {
		return fmt.Sprintf("%d MiB", limit>>20)
	}
	return fmt.Sprintf("%d bytes", limit)
}
