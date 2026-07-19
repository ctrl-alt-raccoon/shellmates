package setup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestReleaseAssetName(t *testing.T) {
	for _, test := range []struct {
		goos, goarch, want string
		wantError          bool
	}{
		{goos: "darwin", goarch: "amd64", want: "sclaude_darwin_amd64"},
		{goos: "darwin", goarch: "arm64", want: "sclaude_darwin_arm64"},
		{goos: "linux", goarch: "amd64", want: "sclaude_linux_amd64"},
		{goos: "linux", goarch: "arm64", want: "sclaude_linux_arm64"},
		{goos: "windows", goarch: "amd64", wantError: true},
		{goos: "linux", goarch: "386", wantError: true},
	} {
		got, err := releaseAssetName(test.goos, test.goarch)
		if test.wantError {
			if err == nil {
				t.Fatalf("releaseAssetName(%q, %q) succeeded", test.goos, test.goarch)
			}
		} else if err != nil || got != test.want {
			t.Fatalf("releaseAssetName(%q, %q) = %q, %v; want %q", test.goos, test.goarch, got, err, test.want)
		}
	}
}

func TestParseChecksumManifestStrict(t *testing.T) {
	valid := strings.Repeat("a", 64) + "  sclaude_linux_amd64\n" + strings.Repeat("b", 64) + " *other\n"
	got, err := parseChecksumManifest([]byte(valid), "sclaude_linux_amd64")
	if err != nil || got != strings.Repeat("a", 64) {
		t.Fatalf("valid manifest = %q, %v", got, err)
	}

	for _, test := range []struct {
		name, manifest, want string
	}{
		{name: "missing", manifest: strings.Repeat("a", 64) + "  other\n", want: "missing"},
		{name: "duplicate", manifest: strings.Repeat("a", 64) + "  sclaude_linux_amd64\n" + strings.Repeat("b", 64) + "  sclaude_linux_amd64\n", want: "duplicate"},
		{name: "nul", manifest: strings.Repeat("a", 64) + "  sclaude_linux_amd64\x00\n", want: "invalid data"},
		{name: "nonhex", manifest: strings.Repeat("z", 64) + "  sclaude_linux_amd64\n", want: "invalid SHA-256"},
		{name: "short digest", manifest: "abcd  sclaude_linux_amd64\n", want: "invalid checksum manifest"},
		{name: "extra field", manifest: strings.Repeat("a", 64) + "  sclaude_linux_amd64 extra\n", want: "invalid asset name"},
		{name: "bad separator", manifest: strings.Repeat("a", 64) + " sclaude_linux_amd64\n", want: "invalid checksum manifest"},
		{name: "blank line", manifest: strings.Repeat("a", 64) + "  sclaude_linux_amd64\n\nother\n", want: "invalid checksum manifest"},
		{name: "path", manifest: strings.Repeat("a", 64) + "  sub/sclaude_linux_amd64\n", want: "invalid asset name"},
		{name: "carriage return", manifest: strings.Repeat("a", 64) + "  sclaude_linux_amd64\r\n", want: "invalid asset name"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseChecksumManifest([]byte(test.manifest), "sclaude_linux_amd64")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestUpdateExplicitVersionDownloadsVerifiedAsset(t *testing.T) {
	const version = "v1.2.3"
	const asset = "sclaude_linux_amd64"
	binary := []byte("published binary")
	digest := sha256.Sum256(binary)
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		switch r.URL.Path {
		case "/owner/repo/releases/download/" + version + "/SHA256SUMS":
			_, _ = io.WriteString(w, hex.EncodeToString(digest[:])+"  "+asset+"\n")
		case "/owner/repo/releases/download/" + version + "/" + asset:
			_, _ = w.Write(binary)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	installed := false
	ledger, err := updateWithDeps(context.Background(), UpdateOptions{Version: version, Repo: "owner/repo"}, InstallLayout{}, updateDeps{
		client:       server.Client(),
		apiBase:      server.URL,
		downloadBase: server.URL,
		goos:         "linux",
		goarch:       "amd64",
		allowHTTP:    true,
		install: func(path, gotVersion string, _ InstallLayout) (InstallLedger, error) {
			installed = true
			if gotVersion != version {
				t.Fatalf("version = %q", gotVersion)
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(data) != string(binary) {
				t.Fatalf("binary = %q", data)
			}
			if info, statErr := os.Stat(path); statErr != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("temp mode = %v, %v", info.Mode().Perm(), statErr)
			}
			return InstallLedger{Current: gotVersion}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !installed || ledger.Current != version {
		t.Fatalf("installed=%v ledger=%+v", installed, ledger)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %q; explicit version must not fetch latest", requests)
	}
}

func TestUpdateLatestUsesValidatedTag(t *testing.T) {
	binary := []byte("latest binary")
	digest := sha256.Sum256(binary)
	latestCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/releases/latest":
			latestCalls++
			_, _ = io.WriteString(w, `{"tag_name":"v2.0.0"}`)
		case "/owner/repo/releases/download/v2.0.0/SHA256SUMS":
			_, _ = io.WriteString(w, hex.EncodeToString(digest[:])+"  sclaude_darwin_arm64\n")
		case "/owner/repo/releases/download/v2.0.0/sclaude_darwin_arm64":
			_, _ = w.Write(binary)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	_, err := updateWithDeps(context.Background(), UpdateOptions{Repo: "owner/repo"}, InstallLayout{}, updateDeps{
		client: server.Client(), apiBase: server.URL, downloadBase: server.URL,
		goos: "darwin", goarch: "arm64", allowHTTP: true,
		install: func(_ string, version string, _ InstallLayout) (InstallLedger, error) {
			if version != "v2.0.0" {
				t.Fatalf("version = %q", version)
			}
			return InstallLedger{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if latestCalls != 1 {
		t.Fatalf("latest calls = %d", latestCalls)
	}
}

func TestUpdateTreatsLatestAsInvalidExplicitVersion(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	_, err := updateWithDeps(context.Background(), UpdateOptions{Version: "latest", Repo: "owner/repo"}, InstallLayout{}, testUpdateDeps(server))
	if err == nil || requests != 0 {
		t.Fatalf("error=%v requests=%d", err, requests)
	}
}

func TestUpdateRejectsInvalidVersionBeforeNetwork(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		http.Error(w, "body-secret", http.StatusInternalServerError)
	}))
	defer server.Close()
	_, err := updateWithDeps(context.Background(), UpdateOptions{Version: "1.2.3", Repo: "owner/repo"}, InstallLayout{}, testUpdateDeps(server))
	if err == nil || requests != 0 {
		t.Fatalf("error=%v requests=%d", err, requests)
	}
}

func TestUpdateRejectsInvalidLatestTagAndTrailingJSON(t *testing.T) {
	for _, body := range []string{`{"tag_name":"latest"}`, `{"tag_name":"v1.2.3"} {"secret":"body-secret"}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			_, err := updateWithDeps(context.Background(), UpdateOptions{Repo: "owner/repo"}, InstallLayout{}, testUpdateDeps(server))
			if err == nil {
				t.Fatal("expected latest metadata rejection")
			}
			if strings.Contains(err.Error(), "body-secret") {
				t.Fatalf("error leaked response body: %v", err)
			}
		})
	}
}

func TestUpdateChecksumMismatchPreventsInstall(t *testing.T) {
	const asset = "sclaude_linux_amd64"
	installed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/SHA256SUMS") {
			_, _ = io.WriteString(w, strings.Repeat("0", 64)+"  "+asset+"\n")
			return
		}
		_, _ = io.WriteString(w, "different")
	}))
	defer server.Close()
	deps := testUpdateDeps(server)
	deps.install = func(string, string, InstallLayout) (InstallLedger, error) {
		installed = true
		return InstallLedger{}, nil
	}
	_, err := updateWithDeps(context.Background(), UpdateOptions{Version: "v1.0.0", Repo: "owner/repo"}, InstallLayout{}, deps)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") || installed {
		t.Fatalf("error=%v installed=%v", err, installed)
	}
}

func TestUpdateBoundsAllResponsesAndDoesNotLeakBodies(t *testing.T) {
	for _, test := range []struct {
		name, version string
		path          string
		size          int
	}{
		{name: "api", path: "/repos/owner/repo/releases/latest", size: maxLatestReleaseSize + 1},
		{name: "manifest", version: "v1.0.0", path: "/owner/repo/releases/download/v1.0.0/SHA256SUMS", size: maxChecksumFileSize + 1},
		{name: "binary", version: "v1.0.0", path: "/owner/repo/releases/download/v1.0.0/sclaude_linux_amd64", size: maxReleaseBinarySize + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == test.path:
					w.Header().Set("Content-Length", "0")
					w.Header().Del("Content-Length")
					_, _ = io.CopyN(w, strings.NewReader(strings.Repeat("body-secret", (test.size/11)+1)), int64(test.size))
				case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
					digest := sha256.Sum256([]byte(strings.Repeat("body-secret", (test.size/11)+1)[:test.size]))
					_, _ = io.WriteString(w, hex.EncodeToString(digest[:])+"  sclaude_linux_amd64\n")
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			deps := testUpdateDeps(server)
			_, err := updateWithDeps(context.Background(), UpdateOptions{Version: test.version, Repo: "owner/repo"}, InstallLayout{}, deps)
			if err == nil || !strings.Contains(err.Error(), "exceeds") {
				t.Fatalf("error = %v", err)
			}
			if strings.Contains(err.Error(), "body-secret") {
				t.Fatalf("error leaked response body: %v", err)
			}
		})
	}
}

func TestFetchBoundedDoesNotExposeHTTPBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "body-secret", http.StatusBadGateway)
	}))
	defer server.Close()
	_, err := fetchBounded(context.Background(), testUpdateDeps(server), server.URL, 100, "test response")
	if err == nil || strings.Contains(err.Error(), "body-secret") || !strings.Contains(err.Error(), "502") {
		t.Fatalf("error = %v", err)
	}
}

func TestProductionReleaseURLsRequireHTTPS(t *testing.T) {
	deps := defaultUpdateDeps()
	deps.apiBase = "http://api.github.com"
	_, err := fetchLatestReleaseTag(context.Background(), "owner/repo", deps)
	if err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("error = %v", err)
	}
}

func TestProductionClientRejectsUntrustedRedirect(t *testing.T) {
	client := defaultUpdateDeps().client
	req, err := http.NewRequest(http.MethodGet, "https://evil.example/release", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckRedirect(req, []*http.Request{{}}); err == nil {
		t.Fatal("untrusted redirect was accepted")
	}
}

func testUpdateDeps(server *httptest.Server) updateDeps {
	return updateDeps{
		client: server.Client(), apiBase: server.URL, downloadBase: server.URL,
		goos: "linux", goarch: "amd64", allowHTTP: true,
		install: func(string, string, InstallLayout) (InstallLedger, error) {
			return InstallLedger{}, nil
		},
	}
}
