package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func discoverScreenExecutable() (string, error) {
	return discoverExecutable(
		"screen",
		homebrewExecutablePaths("screen")...,
	)
}

func discoverExecutable(command string, homebrewPaths ...string) (string, error) {
	if paths := validatedCommandPaths(command, validateExecutablePath); len(paths) > 0 {
		return paths[0], nil
	}
	for _, candidate := range homebrewPaths {
		prefix := filepath.Dir(filepath.Dir(filepath.Clean(candidate)))
		if path, err := validateContainedPath(
			prefix,
			candidate,
			validateExecutablePath,
		); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s: %w", command, exec.ErrNotFound)
}

func discoverClaudeExecutable() (string, error) {
	return discoverHarnessExecutable("claude")
}

func configuredCodexExecutable(explicit string) (string, error) {
	if explicit == "" {
		return discoverHarnessExecutable("codex")
	}
	if !filepath.IsAbs(explicit) {
		return "", errors.New("Codex executable must be an absolute path")
	}
	resolved, err := validateExecutablePath(explicit)
	if err != nil {
		return "", err
	}
	if matchesAnyIdentity(resolved, projectExecutableIdentities()) {
		return "", errors.New("Codex executable resolves to a project launcher")
	}
	return filepath.Clean(explicit), nil
}

func discoverHarnessExecutable(command string) (string, error) {
	owned := projectExecutableIdentities()
	for _, candidate := range validatedCommandCandidates(command, validateExecutablePath) {
		if !matchesAnyIdentity(candidate.Resolved, owned) {
			return candidate.Candidate, nil
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, ".local", "bin", command)
		if paths := validatedCandidate(candidate, validateExecutablePath); len(paths) > 0 && !matchesAnyIdentity(paths[0].Resolved, owned) {
			return paths[0].Candidate, nil
		}
	}
	return "", fmt.Errorf("%s: %w", command, exec.ErrNotFound)
}

func resolveCommand(command string, fallbackPaths ...string) (string, error) {
	if paths := validatedCommandPaths(command, validateExecutablePath); len(paths) > 0 {
		return paths[0], nil
	}
	for _, candidate := range fallbackPaths {
		if candidate == "" {
			continue
		}
		resolved, err := validateExecutablePath(candidate)
		if err == nil {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("%s: %w", command, exec.ErrNotFound)
}

type validatedCommandPath struct {
	Candidate string
	Resolved  string
}

type fileIdentity struct {
	path string
	info os.FileInfo
}

type trustedExecutable struct {
	identity fileIdentity
	digest   string
}

func resolveTrustedExecutable(command string, fallbackPaths ...string) (trustedExecutable, error) {
	path, err := resolveCommand(command, fallbackPaths...)
	if err != nil {
		return trustedExecutable{}, err
	}
	return trustExecutablePath(path)
}

func resolveTrustedExecutableAtPaths(command string, paths ...string) (trustedExecutable, error) {
	for _, path := range paths {
		if path == "" {
			continue
		}
		executable, err := trustExecutablePath(path)
		if err == nil {
			return executable, nil
		}
	}
	return trustedExecutable{}, fmt.Errorf("%s: %w", command, exec.ErrNotFound)
}

func resolveTrustedHomebrewExecutableAtPaths(
	command string,
	paths ...string,
) (trustedExecutable, error) {
	for _, path := range paths {
		if path == "" {
			continue
		}
		prefix := filepath.Dir(filepath.Dir(filepath.Clean(path)))
		resolved, err := validateContainedPath(
			prefix,
			path,
			validateExecutablePath,
		)
		if err != nil {
			continue
		}
		executable, err := trustExecutablePath(resolved)
		if err == nil {
			return executable, nil
		}
	}
	return trustedExecutable{}, fmt.Errorf("%s: %w", command, exec.ErrNotFound)
}

func homebrewExecutablePaths(command string) []string {
	if command == "" {
		return nil
	}
	return []string{
		filepath.Join("/opt/homebrew/bin", command),
		filepath.Join("/usr/local/bin", command),
	}
}

func validateContainedPath(
	prefix,
	path string,
	validate func(string) (string, error),
) (string, error) {
	canonicalPrefix, err := canonicalDirectory(prefix)
	if err != nil {
		return "", err
	}
	resolved, err := validate(path)
	if err != nil {
		return "", err
	}
	if !pathWithinDirectory(canonicalPrefix, resolved) {
		return "", errors.New("resolved path is outside the approved prefix")
	}
	return resolved, nil
}

func canonicalDirectory(path string) (string, error) {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return "", errors.New("prefix must be an absolute path")
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("prefix must resolve to a directory")
	}
	return filepath.Clean(resolved), nil
}

func pathWithinDirectory(directory, path string) bool {
	relative, err := filepath.Rel(directory, path)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func sameContainedRegularFile(
	prefix,
	left,
	right string,
	validate func(string) (string, error),
) bool {
	leftResolved, err := validateContainedPath(prefix, left, validate)
	if err != nil {
		return false
	}
	rightResolved, err := validateContainedPath(prefix, right, validate)
	return err == nil && sameRegularFileIdentity(leftResolved, rightResolved)
}

func trustExecutablePath(path string) (trustedExecutable, error) {
	resolved, err := validateExecutablePath(path)
	if err != nil {
		return trustedExecutable{}, err
	}
	identity, err := identifyRegularFile(resolved)
	if err != nil {
		return trustedExecutable{}, err
	}
	digest, err := executableDigest(identity.path)
	if err != nil {
		return trustedExecutable{}, err
	}
	return trustedExecutable{identity: identity, digest: digest}, nil
}

func recoverTrustedExecutable(path, digest string) (trustedExecutable, error) {
	if !setupDigestPattern.MatchString(digest) {
		return trustedExecutable{}, errors.New("trusted executable digest is invalid")
	}
	executable, err := trustExecutablePath(path)
	if err != nil {
		return trustedExecutable{}, err
	}
	if executable.digest != digest {
		return trustedExecutable{}, errors.New("trusted executable changed since setup preparation")
	}
	return executable, nil
}

func (executable trustedExecutable) path() string {
	return executable.identity.path
}

func (executable trustedExecutable) revalidate() (string, error) {
	if executable.identity.path == "" || executable.identity.info == nil || executable.digest == "" {
		return "", errors.New("trusted executable identity is missing")
	}
	resolved, err := validateExecutablePath(executable.identity.path)
	if err != nil {
		return "", fmt.Errorf("revalidate trusted executable: %w", err)
	}
	current, err := identifyRegularFile(resolved)
	if err != nil {
		return "", fmt.Errorf("revalidate trusted executable: %w", err)
	}
	if current.path != executable.identity.path || !os.SameFile(executable.identity.info, current.info) {
		return "", errors.New("trusted executable changed after preparation")
	}
	digest, err := executableDigest(current.path)
	if err != nil {
		return "", fmt.Errorf("revalidate trusted executable: %w", err)
	}
	if digest != executable.digest {
		return "", errors.New("trusted executable contents changed after preparation")
	}
	return current.path, nil
}

func executableDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func validatedCommandPaths(command string, validate func(string) (string, error)) []string {
	candidates := validatedCommandCandidates(command, validate)
	paths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		paths = append(paths, candidate.Resolved)
	}
	return paths
}

func validatedCommandCandidates(command string, validate func(string) (string, error)) []validatedCommandPath {
	if command == "" {
		return nil
	}
	if strings.ContainsRune(command, os.PathSeparator) {
		return validatedCandidate(command, validate)
	}

	var paths []validatedCommandPath
	for _, directory := range filepath.SplitList(os.Getenv("PATH")) {
		if directory == "" || !filepath.IsAbs(directory) {
			continue
		}
		paths = append(paths, validatedCandidate(filepath.Join(directory, command), validate)...)
	}
	return paths
}

func validatedCandidate(path string, validate func(string) (string, error)) []validatedCommandPath {
	if !filepath.IsAbs(path) {
		return nil
	}
	absolute := filepath.Clean(path)
	resolved, err := validate(absolute)
	if err != nil {
		return nil
	}
	return []validatedCommandPath{{Candidate: absolute, Resolved: resolved}}
}

func validateExecutablePath(path string) (string, error) {
	if path == "" {
		return "", errors.New("executable path is missing")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect executable: %w", err)
	}
	if !info.Mode().IsRegular() || !executableByCurrentUser(info) {
		return "", errors.New("executable must be a regular file executable by the current user")
	}
	return resolved, nil
}

func identifyRegularFile(path string) (fileIdentity, error) {
	if path == "" {
		return fileIdentity{}, errors.New("file path is missing")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fileIdentity{}, err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return fileIdentity{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return fileIdentity{}, err
	}
	if !info.Mode().IsRegular() {
		return fileIdentity{}, errors.New("path must be a regular file")
	}
	return fileIdentity{path: resolved, info: info}, nil
}

func sameFileIdentity(left, right fileIdentity) bool {
	return left.path == right.path || os.SameFile(left.info, right.info)
}

func sameRegularFileIdentity(left, right string) bool {
	leftIdentity, err := identifyRegularFile(left)
	if err != nil {
		return false
	}
	rightIdentity, err := identifyRegularFile(right)
	return err == nil && sameFileIdentity(leftIdentity, rightIdentity)
}

func matchesAnyIdentity(path string, identities []fileIdentity) bool {
	identity, err := identifyRegularFile(path)
	if err != nil {
		return false
	}
	for _, owned := range identities {
		if sameFileIdentity(identity, owned) {
			return true
		}
	}
	return false
}

func projectExecutableIdentities(extraPaths ...string) []fileIdentity {
	paths := append([]string{}, extraPaths...)
	if executable, err := os.Executable(); err == nil {
		paths = append(paths, executable)
	}
	if layout, err := DefaultInstallLayout(""); err == nil {
		if installed, installedErr := InstalledLayout(); installedErr == nil {
			layout = installed
		}
		paths = append(paths,
			filepath.Join(layout.BinDir, "sclaude"),
			filepath.Join(layout.BinDir, "sclaudex"),
			filepath.Join(layout.BinDir, "scodex"),
			filepath.Join(layout.DataDir, "current", "sclaude"),
		)
		if ledger, ledgerErr := LoadInstallLedger(filepath.Join(layout.StateDir, "ledger.json")); ledgerErr == nil {
			for path := range ledger.Files {
				paths = append(paths, path)
			}
			for tag := range ledger.Releases {
				paths = append(paths, filepath.Join(layout.DataDir, "releases", tag, "sclaude"))
			}
		}
	}
	identities := make([]fileIdentity, 0, len(paths))
	for _, path := range paths {
		identity, err := identifyRegularFile(path)
		if err != nil {
			continue
		}
		duplicate := false
		for _, existing := range identities {
			if sameFileIdentity(identity, existing) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			identities = append(identities, identity)
		}
	}
	return identities
}

func sameExecutable(left, right string) bool {
	if _, err := validateExecutablePath(left); err != nil {
		return false
	}
	if _, err := validateExecutablePath(right); err != nil {
		return false
	}
	return sameRegularFileIdentity(left, right)
}
