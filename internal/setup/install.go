package setup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/ctrl-alt-raccoon/shellmates/internal/config"
	"github.com/ctrl-alt-raccoon/shellmates/internal/fssecure"
)

const (
	installLedgerSchema  = 3
	installJournalSchema = 3
)

var releaseTagPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)
var afterInstallAdmissionLock func(string) error
var beforeInstallReleasePublication func(stagedRelease) error
var syncInstallReleaseDirectory = func(directory *fssecure.Directory) error {
	return directory.Sync()
}

type InstallLayout struct {
	DataDir  string
	StateDir string
	BinDir   string
}

type ShellBlockOwnership struct {
	Digest  string `json:"digest"`
	Created bool   `json:"created,omitempty"`
}

type InstallLedger struct {
	SchemaVersion int                            `json:"schema_version"`
	Current       string                         `json:"current"`
	Previous      string                         `json:"previous,omitempty"`
	BinDir        string                         `json:"bin_dir"`
	DataDir       string                         `json:"data_dir,omitempty"`
	StateDir      string                         `json:"state_dir,omitempty"`
	Releases      map[string]string              `json:"releases,omitempty"`
	CodexReleases map[string]bool                `json:"codex_releases,omitempty"`
	Files         map[string]string              `json:"files"`
	ShellBlocks   map[string]ShellBlockOwnership `json:"shell_blocks,omitempty"`
	InstalledAt   string                         `json:"installed_at"`
	Warnings      []string                       `json:"-"`
}

type linkSnapshot struct {
	Exists bool   `json:"exists"`
	Target string `json:"target,omitempty"`
}

type installJournal struct {
	SchemaVersion      int                      `json:"schema_version"`
	PriorLedger        []byte                   `json:"prior_ledger,omitempty"`
	PriorLedgerExist   bool                     `json:"prior_ledger_exists"`
	PriorCurrent       linkSnapshot             `json:"prior_current"`
	PriorLaunchers     map[string]linkSnapshot  `json:"prior_launchers"`
	NewLedgerDigest    string                   `json:"new_ledger_digest"`
	NewCurrent         string                   `json:"new_current"`
	NewReleaseCreated  bool                     `json:"new_release_created,omitempty"`
	NewReleaseStage    string                   `json:"new_release_stage,omitempty"`
	NewReleaseDigest   string                   `json:"new_release_digest,omitempty"`
	NewReleaseIdentity *setupFileIdentity       `json:"new_release_identity,omitempty"`
	PrunedReleases     []installReleaseArtifact `json:"pruned_releases,omitempty"`
}

type installReleaseArtifact struct {
	Path     string             `json:"path"`
	Digest   string             `json:"digest"`
	Identity *setupFileIdentity `json:"identity"`
}

type stagedRelease struct {
	Digest      string
	StagePath   string
	Destination string
	Created     bool
	Identity    *setupFileIdentity
}

type activationOps struct {
	switchCurrent func(string, string) error
	writeLedger   func(string, []byte) error
	clearJournal  func(string) error
	removeRelease func(installReleaseArtifact) error
}

func defaultActivationOps() activationOps {
	return activationOps{
		switchCurrent: atomicSymlink,
		writeLedger: func(path string, data []byte) error {
			return writePrivateFileSynced(path, data, 0o600)
		},
		clearJournal: removeIfPresent,
		removeRelease: func(artifact installReleaseArtifact) error {
			return removeOwnedReleaseDirectory(artifact.Path, artifact.Digest, artifact.Identity)
		},
	}
}

func DefaultInstallLayout(binDir string) (InstallLayout, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return InstallLayout{}, err
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(home, ".local", "state")
	}
	if binDir == "" {
		binDir = filepath.Join(home, ".local", "bin")
	}
	return normalizeInstallLayout(InstallLayout{
		DataDir:  filepath.Join(data, "sclaude"),
		StateDir: filepath.Join(state, "sclaude", "install"),
		BinDir:   binDir,
	})
}

// InstalledLayout loads the existing ledger from the default install-state
// directory and recovers a custom bin directory recorded by the installer.
func InstalledLayout() (InstallLayout, error) {
	base, err := DefaultInstallLayout("")
	if err != nil {
		return InstallLayout{}, err
	}
	ledger, err := LoadInstallLedger(ledgerPath(base))
	if err != nil {
		return InstallLayout{}, err
	}
	layout := base
	layout.BinDir = ledger.BinDir
	if ledger.SchemaVersion >= 2 {
		layout.DataDir = ledger.DataDir
		layout.StateDir = ledger.StateDir
	}
	layout, err = normalizeInstallLayout(layout)
	if err != nil {
		return InstallLayout{}, err
	}
	if layout.DataDir != base.DataDir || layout.StateDir != base.StateDir {
		return InstallLayout{}, errors.New("install ledger data/state paths do not match the current user installation")
	}
	return layout, nil
}

func InstallBinary(source, version string, layout InstallLayout) (InstallLedger, error) {
	return installBinaryWithOps(source, version, layout, defaultActivationOps())
}

func installBinaryWithOps(source, version string, layout InstallLayout, ops activationOps) (InstallLedger, error) {
	if err := validateReleaseTag(version); err != nil {
		return InstallLedger{}, err
	}
	var err error
	layout, err = normalizeInstallLayout(layout)
	if err != nil {
		return InstallLedger{}, err
	}
	if err := inspectRegularSource(source); err != nil {
		return InstallLedger{}, err
	}
	var result InstallLedger
	err = withInstallLock(layout, func() error {
		if err := recoverInstallJournal(layout); err != nil {
			return err
		}
		if _, err := recoverUninstallJournal(layout); err != nil {
			return err
		}
		if err := ensureInstallDirectories(layout); err != nil {
			return err
		}
		old, exists, _, err := loadLedgerForLayout(layout)
		if err != nil {
			return err
		}
		if exists {
			if err := preflightLedger(layout, old); err != nil {
				return err
			}
			if old.Current == version {
				sourceDigest, err := digestRegularFile(source)
				if err != nil {
					return err
				}
				if sourceDigest != old.Releases[version] {
					return errors.New("same release tag has different contents")
				}
				if old.SchemaVersion == installLedgerSchema {
					result = old
					return nil
				}
			}
			if err := preflightLauncherUpgrade(layout, old); err != nil {
				return err
			}
		} else if err := preflightFreshInstall(layout); err != nil {
			return err
		}

		ownedDigest := ""
		if exists {
			ownedDigest = old.Releases[version]
		}
		release, err := stageRelease(source, version, ownedDigest, layout)
		if err != nil {
			return err
		}
		result, err = activateRelease(layout, old, exists, version, release, ops)
		return err
	})
	return result, err
}

func LoadInstallLedger(path string) (InstallLedger, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return InstallLedger{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var ledger InstallLedger
	if err := decoder.Decode(&ledger); err != nil {
		return InstallLedger{}, fmt.Errorf("parse install ledger: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return InstallLedger{}, errors.New("parse install ledger: trailing data")
	}
	if ledger.SchemaVersion != 1 && ledger.SchemaVersion != 2 && ledger.SchemaVersion != installLedgerSchema {
		return InstallLedger{}, fmt.Errorf("unsupported install ledger schema %d", ledger.SchemaVersion)
	}
	if strings.TrimSpace(ledger.Current) == "" || strings.TrimSpace(ledger.BinDir) == "" || ledger.Files == nil {
		return InstallLedger{}, errors.New("install ledger is incomplete")
	}
	if ledger.SchemaVersion >= 2 {
		if ledger.DataDir == "" || ledger.StateDir == "" || ledger.Releases == nil {
			return InstallLedger{}, errors.New("install ledger is missing canonical paths or releases")
		}
	}
	if ledger.SchemaVersion == installLedgerSchema && !ledger.CodexReleases[ledger.Current] {
		return InstallLedger{}, errors.New("install ledger is missing native Codex compatibility information")
	}
	return ledger, nil
}

func projectOwnedExecutables() []string {
	layout, err := InstalledLayout()
	if err != nil {
		return nil
	}
	ledger, err := LoadInstallLedger(ledgerPath(layout))
	if err != nil {
		return nil
	}
	paths := make([]string, 0, len(ledger.Files))
	for path, recordedDigest := range ledger.Files {
		if path == "" || recordedDigest == "" {
			continue
		}
		digest, err := digestPath(path)
		if err == nil && digest == recordedDigest {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths
}

func Rollback(layout InstallLayout) (InstallLedger, error) {
	return rollbackWithOps(layout, defaultActivationOps())
}

func rollbackWithOps(layout InstallLayout, ops activationOps) (InstallLedger, error) {
	var err error
	layout, err = normalizeInstallLayout(layout)
	if err != nil {
		return InstallLedger{}, err
	}
	var result InstallLedger
	err = withInstallLock(layout, func() error {
		if err := recoverInstallJournal(layout); err != nil {
			return err
		}
		if _, err := recoverUninstallJournal(layout); err != nil {
			return err
		}
		ledger, exists, migrated, err := loadLedgerForLayout(layout)
		if err != nil {
			return err
		}
		if !exists {
			return os.ErrNotExist
		}
		if err := preflightLedger(layout, ledger); err != nil {
			return err
		}
		if migrated {
			if err := persistMigratedLedger(layout, ledger); err != nil {
				return err
			}
		}
		if ledger.Previous == "" {
			return errors.New("no previous release is available")
		}
		if !ledger.CodexReleases[ledger.Previous] {
			return errors.New("previous release predates native Codex support; automatic rollback across this configuration/launcher boundary is refused")
		}
		digest, ok := ledger.Releases[ledger.Previous]
		if !ok || digest == "" {
			return errors.New("previous release is not recorded as project-owned")
		}
		if err := verifyRelease(layout, ledger.Previous, digest, true); err != nil {
			return fmt.Errorf("previous release unavailable: %w", err)
		}
		result, err = activateRelease(layout, ledger, true, ledger.Previous, stagedRelease{
			Digest:      digest,
			Destination: releaseDir(layout, ledger.Previous),
		}, ops)
		return err
	})
	return result, err
}

func Uninstall(layout InstallLayout, purge bool) error {
	paths := installPathsForLayout(layout)
	if purge {
		var err error
		paths, err = config.DefaultPaths()
		if err != nil {
			return err
		}
	}
	return uninstallWithOps(paths, layout, purge, nil, defaultUninstallOps())
}

func installPathsForLayout(layout InstallLayout) config.Paths {
	stateRoot := filepath.Dir(layout.StateDir)
	if filepath.Base(layout.StateDir) != "install" {
		stateRoot = layout.StateDir
	}
	return config.Paths{
		StateRoot:    stateRoot,
		SessionsDir:  filepath.Join(stateRoot, "sessions"),
		LaunchDir:    filepath.Join(stateRoot, "launch"),
		InstallState: layout.StateDir,
		DataRoot:     layout.DataDir,
	}
}

func UninstallInstalled(
	paths config.Paths,
	purge bool,
	inactive func() error,
) error {
	base, err := DefaultInstallLayout("")
	if err != nil {
		return err
	}
	if filepath.Clean(paths.StateRoot) != filepath.Dir(base.StateDir) ||
		filepath.Clean(paths.InstallState) != base.StateDir ||
		filepath.Clean(paths.DataRoot) != base.DataDir {
		return errors.New("runtime paths do not match the current user installation")
	}
	return uninstallWithOps(paths, base, purge, inactive, defaultUninstallOps())
}

// RecordShellOwnership commits managed PATH-block ownership to an existing
// install ledger. Development setup without an installed ledger is a no-op.
func RecordShellOwnership(edits []ShellEdit) error {
	if len(edits) == 0 {
		return nil
	}
	layout, err := InstalledLayout()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return withInstallLock(layout, func() error {
		if err := recoverInstallJournal(layout); err != nil {
			return err
		}
		if _, err := recoverUninstallJournal(layout); err != nil {
			return err
		}
		ledger, exists, _, err := loadLedgerForLayout(layout)
		if err != nil || !exists {
			return err
		}
		if err := preflightLedger(layout, ledger); err != nil {
			return err
		}
		ledger, err = updateShellOwnership(ledger, edits)
		if err != nil {
			return err
		}
		return writeLedger(layout, ledger)
	})
}

func updateShellOwnership(ledger InstallLedger, edits []ShellEdit) (InstallLedger, error) {
	ledger.ShellBlocks = cloneShellOwnership(ledger.ShellBlocks)
	if ledger.ShellBlocks == nil {
		ledger.ShellBlocks = map[string]ShellBlockOwnership{}
	}
	for _, edit := range edits {
		if edit.Path == "" || edit.Digest == "" {
			return InstallLedger{}, errors.New("shell edit is missing ownership metadata")
		}
		ownership := ledger.ShellBlocks[edit.Path]
		ownership.Digest = edit.Digest
		ownership.Created = ownership.Created || edit.Created
		ledger.ShellBlocks[edit.Path] = ownership
	}
	return ledger, nil
}

func normalizeInstallLayout(layout InstallLayout) (InstallLayout, error) {
	values := []*string{&layout.DataDir, &layout.StateDir, &layout.BinDir}
	for _, value := range values {
		if strings.TrimSpace(*value) == "" {
			return InstallLayout{}, errors.New("install layout paths are required")
		}
		absolute, err := filepath.Abs(*value)
		if err != nil {
			return InstallLayout{}, err
		}
		*value = filepath.Clean(absolute)
		if *value == string(filepath.Separator) {
			return InstallLayout{}, errors.New("install layout cannot use the filesystem root")
		}
	}
	paths := []string{layout.DataDir, layout.StateDir, layout.BinDir}
	for i := range paths {
		for j := i + 1; j < len(paths); j++ {
			if pathsOverlap(paths[i], paths[j]) {
				return InstallLayout{}, errors.New("install layout paths must not overlap")
			}
		}
	}
	return layout, nil
}

func pathsOverlap(left, right string) bool {
	for _, pair := range [][2]string{{left, right}, {right, left}} {
		relative, err := filepath.Rel(pair[0], pair[1])
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func validateReleaseTag(tag string) error {
	if tag == "" || !releaseTagPattern.MatchString(tag) {
		return fmt.Errorf("invalid release tag %q; expected canonical vMAJOR.MINOR.PATCH SemVer", tag)
	}
	matches := releaseTagPattern.FindStringSubmatch(tag)
	if len(matches) > 4 && matches[4] != "" {
		for _, identifier := range strings.Split(matches[4], ".") {
			if len(identifier) > 1 && identifier[0] == '0' && allDigits(identifier) {
				return fmt.Errorf("invalid release tag %q: numeric prerelease identifiers cannot have leading zeroes", tag)
			}
		}
	}
	return nil
}

func allDigits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

func ensureInstallDirectories(layout InstallLayout) error {
	for _, item := range []struct {
		path string
		mode os.FileMode
	}{{releasesDir(layout), 0o755}, {layout.StateDir, 0o700}, {layout.BinDir, 0o755}} {
		info, err := os.Lstat(item.path)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.MkdirAll(item.path, item.mode); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("install path %s must be a directory, not a symlink", item.path)
		}
	}
	return nil
}

func withInstallLock(layout InstallLayout, fn func() error) error {
	return withInstallLockPath(lockPath(layout), fn)
}

func withInstallLockPath(lockDir string, fn func() error) error {
	info, err := os.Lstat(lockDir)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(lockDir, 0o700); err != nil {
			return err
		}
		info, err = os.Lstat(lockDir)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("install lock path must be a directory, not a symlink")
	}
	lock, err := fssecure.Open(lockDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.File().Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.File().Fd()), syscall.LOCK_UN)
	if afterInstallAdmissionLock != nil {
		if err := afterInstallAdmissionLock(lockDir); err != nil {
			return err
		}
	}
	if err := lock.Revalidate(lockDir); err != nil {
		return errors.New("install lock path changed while acquiring the lock")
	}
	return fn()
}

func loadLedgerForLayout(layout InstallLayout) (InstallLedger, bool, bool, error) {
	ledger, err := LoadInstallLedger(ledgerPath(layout))
	if errors.Is(err, os.ErrNotExist) {
		return InstallLedger{}, false, false, nil
	}
	if err != nil {
		return InstallLedger{}, false, false, err
	}
	if ledger.SchemaVersion == 1 {
		migrated, err := migrateLedger(layout, ledger)
		return migrated, true, true, err
	}
	ledgerLayout, err := normalizeInstallLayout(InstallLayout{DataDir: ledger.DataDir, StateDir: ledger.StateDir, BinDir: ledger.BinDir})
	if err != nil {
		return InstallLedger{}, false, false, err
	}
	if ledgerLayout != layout {
		return InstallLedger{}, false, false, errors.New("install ledger paths do not match the requested layout")
	}
	return ledger, true, false, nil
}

func migrateLedger(layout InstallLayout, old InstallLedger) (InstallLedger, error) {
	binDir, err := filepath.Abs(old.BinDir)
	if err != nil || filepath.Clean(binDir) != layout.BinDir {
		return InstallLedger{}, errors.New("schema-1 install ledger bin directory is inconsistent")
	}
	if err := validateReleaseTag(old.Current); err != nil {
		return InstallLedger{}, fmt.Errorf("cannot migrate schema-1 ledger: %w", err)
	}
	if old.Previous != "" {
		if err := validateReleaseTag(old.Previous); err != nil {
			return InstallLedger{}, fmt.Errorf("cannot migrate schema-1 ledger: %w", err)
		}
	}
	currentTarget, err := readSymlink(currentPath(layout))
	if err != nil || resolveLinkTarget(currentPath(layout), currentTarget) != releaseDir(layout, old.Current) {
		return InstallLedger{}, errors.New("cannot migrate schema-1 ledger: current link is inconsistent")
	}
	expectedTarget := filepath.Join(currentPath(layout), "sclaude")
	files := map[string]string{}
	for _, path := range launcherPathsForSchema(layout, 2) {
		recorded, ok := old.Files[path]
		if !ok || recorded == "" {
			return InstallLedger{}, fmt.Errorf("cannot migrate schema-1 ledger: launcher %s is unrecorded", path)
		}
		digest, err := digestPath(path)
		if err != nil || digest != recorded {
			return InstallLedger{}, fmt.Errorf("cannot migrate schema-1 ledger: launcher %s was modified", path)
		}
		target, err := readSymlink(path)
		if err != nil || resolveLinkTarget(path, target) != expectedTarget {
			return InstallLedger{}, fmt.Errorf("cannot migrate schema-1 ledger: launcher %s has an unexpected target", path)
		}
		files[path] = recorded
	}
	releases := map[string]string{}
	for _, tag := range []string{old.Current, old.Previous} {
		if tag == "" {
			continue
		}
		digest, err := digestRegularFile(releaseBinary(layout, tag))
		if err != nil {
			return InstallLedger{}, fmt.Errorf("cannot migrate schema-1 ledger release %s: %w", tag, err)
		}
		releases[tag] = digest
	}
	return InstallLedger{
		SchemaVersion: 2,
		Current:       old.Current,
		Previous:      old.Previous,
		BinDir:        layout.BinDir,
		DataDir:       layout.DataDir,
		StateDir:      layout.StateDir,
		Releases:      releases,
		Files:         files,
		ShellBlocks:   map[string]ShellBlockOwnership{},
		InstalledAt:   old.InstalledAt,
	}, nil
}

func preflightFreshInstall(layout InstallLayout) error {
	for _, path := range append(stableLauncherPaths(layout), currentPath(layout)) {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("refusing to overwrite unmanaged path %s", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func preflightLedger(layout InstallLayout, ledger InstallLedger) error {
	if ledger.SchemaVersion != 2 && ledger.SchemaVersion != installLedgerSchema {
		return fmt.Errorf("install ledger was not migrated to schema %d", installLedgerSchema)
	}
	if err := validateReleaseTag(ledger.Current); err != nil {
		return err
	}
	if ledger.Previous != "" {
		if err := validateReleaseTag(ledger.Previous); err != nil {
			return err
		}
	}
	for _, tag := range []string{ledger.Current, ledger.Previous} {
		if tag == "" {
			continue
		}
		digest, ok := ledger.Releases[tag]
		if !ok || digest == "" {
			return fmt.Errorf("release %s is not recorded as project-owned", tag)
		}
		if err := verifyRelease(layout, tag, digest, true); err != nil {
			return err
		}
	}
	currentTarget, err := readSymlink(currentPath(layout))
	if err != nil {
		return fmt.Errorf("inspect current release: %w", err)
	}
	if resolveLinkTarget(currentPath(layout), currentTarget) != releaseDir(layout, ledger.Current) {
		return errors.New("current release link does not match the install ledger")
	}

	expectedTarget := filepath.Join(currentPath(layout), "sclaude")
	for _, path := range launcherPathsForSchema(layout, ledger.SchemaVersion) {
		recorded, ok := ledger.Files[path]
		if !ok || recorded == "" {
			return fmt.Errorf("stable launcher %s is missing from the install ledger", path)
		}
		digest, err := digestPath(path)
		if err != nil {
			return err
		}
		if digest != recorded {
			return fmt.Errorf("stable launcher %s was modified", path)
		}
		target, err := readSymlink(path)
		if err != nil {
			return err
		}
		if resolveLinkTarget(path, target) != expectedTarget {
			return fmt.Errorf("stable launcher %s has an unexpected target", path)
		}
	}
	return nil
}

func preflightLauncherUpgrade(layout InstallLayout, ledger InstallLedger) error {
	if ledger.SchemaVersion >= installLedgerSchema {
		return nil
	}
	path := filepath.Join(layout.BinDir, "scodex")
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("refusing to overwrite unmanaged path %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func inspectRegularSource(source string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("source binary must be a regular non-symlink file")
	}
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return errors.New("source binary changed while being opened")
	}
	return nil
}

func stageRelease(source, tag, ownedDigest string, layout InstallLayout) (result stagedRelease, resultErr error) {
	info, err := os.Lstat(source)
	if err != nil {
		return stagedRelease{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return stagedRelease{}, errors.New("source binary must be a regular non-symlink file")
	}
	in, err := os.Open(source)
	if err != nil {
		return stagedRelease{}, err
	}
	defer in.Close()
	openedInfo, err := in.Stat()
	if err != nil {
		return stagedRelease{}, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		return stagedRelease{}, errors.New("source binary changed while being opened")
	}

	stage, err := os.MkdirTemp(releasesDir(layout), ".staging-")
	if err != nil {
		return stagedRelease{}, err
	}
	keepStage := false
	defer func() {
		if !keepStage {
			resultErr = errors.Join(resultErr, os.RemoveAll(stage))
		}
	}()
	if err := os.Chmod(stage, 0o700); err != nil {
		return stagedRelease{}, err
	}
	staged := filepath.Join(stage, "sclaude")
	out, err := os.OpenFile(staged, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return stagedRelease{}, err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(out, hash), in)
	if copyErr == nil {
		copyErr = out.Sync()
	}
	closeErr := out.Close()
	if copyErr != nil {
		return stagedRelease{}, copyErr
	}
	if closeErr != nil {
		return stagedRelease{}, closeErr
	}
	if err := syncDir(stage); err != nil {
		return stagedRelease{}, err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	destination := releaseDir(layout, tag)
	if existing, err := os.Lstat(destination); err == nil {
		if !existing.IsDir() || existing.Mode()&os.ModeSymlink != 0 {
			return stagedRelease{}, errors.New("release path is not a project release directory")
		}
		if ownedDigest == "" {
			return stagedRelease{}, errors.New("release directory exists but is not recorded as project-owned")
		}
		existingDigest, err := digestRegularFile(filepath.Join(destination, "sclaude"))
		if err != nil {
			return stagedRelease{}, err
		}
		if existingDigest != ownedDigest {
			return stagedRelease{}, errors.New("recorded release directory was modified")
		}
		if existingDigest != digest {
			return stagedRelease{}, errors.New("same release tag already exists with different contents")
		}
		return stagedRelease{Digest: digest, Destination: destination}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return stagedRelease{}, err
	}
	stageInfo, err := os.Lstat(stage)
	if err != nil {
		return stagedRelease{}, err
	}
	identity, err := setupIdentityFromInfo(stageInfo)
	if err != nil {
		return stagedRelease{}, err
	}
	keepStage = true
	return stagedRelease{
		Digest:      digest,
		StagePath:   stage,
		Destination: destination,
		Created:     true,
		Identity:    identity,
	}, nil
}

func activateRelease(layout InstallLayout, old InstallLedger, oldExists bool, tag string, release stagedRelease, ops activationOps) (InstallLedger, error) {
	if ops.switchCurrent == nil || ops.writeLedger == nil || ops.clearJournal == nil || ops.removeRelease == nil {
		return InstallLedger{}, errors.New("activation operations are incomplete")
	}
	ledger := InstallLedger{
		SchemaVersion: installLedgerSchema,
		Current:       tag,
		BinDir:        layout.BinDir,
		DataDir:       layout.DataDir,
		StateDir:      layout.StateDir,
		Releases:      map[string]string{},
		CodexReleases: map[string]bool{},
		Files:         map[string]string{},
		ShellBlocks:   map[string]ShellBlockOwnership{},
		InstalledAt:   time.Now().UTC().Format(time.RFC3339Nano),
	}
	if oldExists {
		ledger.Releases = cloneStringMap(old.Releases)
		for version, supported := range old.CodexReleases {
			ledger.CodexReleases[version] = supported
		}
		ledger.Files = cloneStringMap(old.Files)
		ledger.ShellBlocks = cloneShellOwnership(old.ShellBlocks)
		if old.Current != tag {
			ledger.Previous = old.Current
		} else {
			ledger.Previous = old.Previous
		}
	}
	ledger.Releases[tag] = release.Digest
	ledger.CodexReleases[tag] = true

	launcherTarget := filepath.Join(currentPath(layout), "sclaude")
	newLaunchers := []string{}
	for _, path := range stableLauncherPaths(layout) {
		if !oldExists || old.Files[path] == "" {
			newLaunchers = append(newLaunchers, path)
			ledger.Files[path] = symlinkDigest(launcherTarget)
		}
	}
	prunedReleases, pruneWarnings := planOwnedReleasePrune(layout, &ledger)
	ledger.Warnings = append(ledger.Warnings, pruneWarnings...)
	ledgerData, err := marshalLedger(ledger)
	if err != nil {
		return InstallLedger{}, err
	}
	journal, err := captureJournal(layout, ledgerData, release, prunedReleases)
	if err != nil {
		return InstallLedger{}, err
	}
	if err := writeJSONPrivate(journalPath(layout), journal); err != nil {
		return InstallLedger{}, errors.Join(err, removeStagedRelease(release))
	}
	if err := syncDir(layout.StateDir); err != nil {
		return InstallLedger{}, errors.Join(err, restoreInstallJournal(layout, journal))
	}

	rollback := func(primary error) (InstallLedger, error) {
		recoveryErr := restoreInstallJournal(layout, journal)
		if recoveryErr != nil {
			return InstallLedger{}, fmt.Errorf("%w; restore prior install state: %v", primary, recoveryErr)
		}
		return InstallLedger{}, primary
	}
	if release.Created {
		if err := publishStagedRelease(layout, release); err != nil {
			return rollback(err)
		}
	}
	if err := ops.switchCurrent(release.Destination, currentPath(layout)); err != nil {
		return rollback(err)
	}
	for _, path := range newLaunchers {
		// New launchers have no prior owner. Never replace an entry that
		// appeared after preflight (including an unrelated scodex command).
		if err := os.Symlink(launcherTarget, path); err != nil {
			return rollback(err)
		}
		if err := syncDir(filepath.Dir(path)); err != nil {
			return rollback(err)
		}
	}
	if err := ops.writeLedger(ledgerPath(layout), ledgerData); err != nil {
		return rollback(err)
	}
	if err := syncDir(layout.StateDir); err != nil {
		return rollback(err)
	}
	cleanupWarnings := removePrunedReleaseArtifactsWith(journal.PrunedReleases, ops.removeRelease)
	ledger.Warnings = append(ledger.Warnings, cleanupWarnings...)
	if len(cleanupWarnings) > 0 {
		return ledger, nil
	}
	if err := ops.clearJournal(journalPath(layout)); err != nil {
		ledger.Warnings = append(ledger.Warnings, fmt.Sprintf("finalize install transaction: %v", err))
		return ledger, nil
	}
	if err := syncDir(layout.StateDir); err != nil {
		ledger.Warnings = append(ledger.Warnings, fmt.Sprintf("sync finalized install transaction: %v", err))
	}
	return ledger, nil
}

func captureJournal(layout InstallLayout, newLedger []byte, release stagedRelease, prunedReleases []installReleaseArtifact) (installJournal, error) {
	if release.Destination == "" || release.Digest == "" {
		return installJournal{}, errors.New("release activation state is incomplete")
	}
	journal := installJournal{
		SchemaVersion:      installJournalSchema,
		PriorLaunchers:     map[string]linkSnapshot{},
		NewLedgerDigest:    hexDigest(newLedger),
		NewCurrent:         release.Destination,
		NewReleaseCreated:  release.Created,
		NewReleaseStage:    release.StagePath,
		NewReleaseDigest:   release.Digest,
		NewReleaseIdentity: release.Identity,
		PrunedReleases:     append([]installReleaseArtifact(nil), prunedReleases...),
	}
	prior, err := os.ReadFile(ledgerPath(layout))
	if err == nil {
		journal.PriorLedgerExist = true
		journal.PriorLedger = prior
	} else if !errors.Is(err, os.ErrNotExist) {
		return installJournal{}, err
	}
	journal.PriorCurrent, err = snapshotLink(currentPath(layout))
	if err != nil {
		return installJournal{}, err
	}
	for _, path := range stableLauncherPaths(layout) {
		snapshot, err := snapshotLink(path)
		if err != nil {
			return installJournal{}, err
		}
		journal.PriorLaunchers[path] = snapshot
	}
	return journal, nil
}

func recoverInstallJournal(layout InstallLayout) error {
	data, err := os.ReadFile(journalPath(layout))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var journal installJournal
	if err := decoder.Decode(&journal); err != nil {
		return fmt.Errorf("parse install transaction journal: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("parse install transaction journal: trailing data")
	}
	if err := validateInstallJournal(layout, journal); err != nil {
		return err
	}
	if installJournalComplete(layout, journal) {
		if warnings := removePrunedReleaseArtifacts(journal.PrunedReleases); len(warnings) > 0 {
			return errors.New(strings.Join(warnings, "; "))
		}
		if err := removeIfPresent(journalPath(layout)); err != nil {
			return err
		}
		return syncDir(layout.StateDir)
	}
	return restoreInstallJournal(layout, journal)
}

func restoreInstallJournal(layout InstallLayout, journal installJournal) error {
	if err := restoreLink(currentPath(layout), journal.PriorCurrent); err != nil {
		return err
	}
	for _, path := range launcherPathsForSchema(layout, journal.SchemaVersion) {
		if err := restoreLauncher(path, journal.PriorLaunchers[path], filepath.Join(currentPath(layout), "sclaude")); err != nil {
			return err
		}
	}
	if journal.PriorLedgerExist {
		if err := writePrivateFileSynced(ledgerPath(layout), journal.PriorLedger, 0o600); err != nil {
			return err
		}
	} else if err := removeIfPresent(ledgerPath(layout)); err != nil {
		return err
	}
	if err := removeJournalReleaseArtifacts(journal); err != nil {
		return err
	}
	if err := removeIfPresent(journalPath(layout)); err != nil {
		return err
	}
	return syncDir(layout.StateDir)
}

func validateInstallJournal(layout InstallLayout, journal installJournal) error {
	if (journal.SchemaVersion != 2 && journal.SchemaVersion != installJournalSchema) || journal.NewLedgerDigest == "" || journal.NewCurrent == "" {
		return errors.New("install transaction journal is incomplete")
	}
	if _, err := hex.DecodeString(journal.NewLedgerDigest); err != nil || len(journal.NewLedgerDigest) != 64 {
		return errors.New("install transaction journal has an invalid ledger digest")
	}
	if _, err := hex.DecodeString(journal.NewReleaseDigest); err != nil || len(journal.NewReleaseDigest) != 64 {
		return errors.New("install transaction journal has an invalid release digest")
	}
	if !validLinkSnapshot(journal.PriorCurrent) {
		return errors.New("install transaction journal has an invalid current snapshot")
	}
	launcherPaths := launcherPathsForSchema(layout, journal.SchemaVersion)
	if len(journal.PriorLaunchers) != len(launcherPaths) {
		return errors.New("install transaction journal has invalid launcher snapshots")
	}
	for _, path := range launcherPaths {
		snapshot, exists := journal.PriorLaunchers[path]
		if !exists || !validLinkSnapshot(snapshot) {
			return errors.New("install transaction journal has invalid launcher snapshots")
		}
	}
	if filepath.Clean(journal.NewCurrent) != journal.NewCurrent || filepath.Dir(journal.NewCurrent) != releasesDir(layout) {
		return errors.New("install transaction journal has an invalid release destination")
	}
	if err := validateReleaseTag(filepath.Base(journal.NewCurrent)); err != nil {
		return errors.New("install transaction journal has an invalid release destination")
	}
	if journal.NewReleaseCreated {
		if journal.NewReleaseIdentity == nil || journal.NewReleaseIdentity.Inode == 0 {
			return errors.New("install transaction journal has incomplete release ownership")
		}
		if filepath.Clean(journal.NewReleaseStage) != journal.NewReleaseStage ||
			filepath.Dir(journal.NewReleaseStage) != releasesDir(layout) ||
			!strings.HasPrefix(filepath.Base(journal.NewReleaseStage), ".staging-") {
			return errors.New("install transaction journal has an invalid release stage")
		}
	} else if journal.NewReleaseStage != "" || journal.NewReleaseIdentity != nil {
		return errors.New("install transaction journal has unexpected release ownership")
	}
	seenPruned := map[string]bool{}
	for _, artifact := range journal.PrunedReleases {
		if filepath.Clean(artifact.Path) != artifact.Path ||
			filepath.Dir(artifact.Path) != releasesDir(layout) ||
			artifact.Path == journal.NewCurrent || seenPruned[artifact.Path] ||
			artifact.Identity == nil || artifact.Identity.Inode == 0 {
			return errors.New("install transaction journal has an invalid pruned release")
		}
		if err := validateReleaseTag(filepath.Base(artifact.Path)); err != nil {
			return errors.New("install transaction journal has an invalid pruned release")
		}
		if _, err := hex.DecodeString(artifact.Digest); err != nil || len(artifact.Digest) != 64 {
			return errors.New("install transaction journal has an invalid pruned release digest")
		}
		seenPruned[artifact.Path] = true
	}
	return nil
}

func validLinkSnapshot(snapshot linkSnapshot) bool {
	return snapshot.Exists == (snapshot.Target != "")
}

func publishStagedRelease(layout InstallLayout, release stagedRelease) error {
	if !release.Created || release.StagePath == "" || release.Identity == nil {
		return errors.New("staged release ownership is incomplete")
	}
	stageInfo, err := os.Lstat(release.StagePath)
	if err != nil {
		return err
	}
	if !stageInfo.IsDir() || stageInfo.Mode()&os.ModeSymlink != 0 || !setupIdentityMatchesInfo(release.Identity, stageInfo) {
		return errors.New("staged release changed before publication")
	}
	if digest, err := digestRegularFile(filepath.Join(release.StagePath, "sclaude")); err != nil || digest != release.Digest {
		return errors.Join(errors.New("staged release contents changed before publication"), err)
	}
	parent, err := fssecure.Open(releasesDir(layout))
	if err != nil {
		return err
	}
	defer parent.Close()
	if err := parent.Revalidate(releasesDir(layout)); err != nil {
		return err
	}
	if beforeInstallReleasePublication != nil {
		if err := beforeInstallReleasePublication(release); err != nil {
			return err
		}
	}
	if err := parent.RenameNoReplace(filepath.Base(release.StagePath), filepath.Base(release.Destination)); err != nil {
		return err
	}
	publishedInfo, err := os.Lstat(release.Destination)
	if err != nil || !publishedInfo.IsDir() || !setupIdentityMatchesInfo(release.Identity, publishedInfo) {
		return errors.Join(errors.New("published release changed during publication"), err)
	}
	if err := syncInstallReleaseDirectory(parent); err != nil {
		return err
	}
	if digest, err := digestRegularFile(filepath.Join(release.Destination, "sclaude")); err != nil || digest != release.Digest {
		return errors.Join(errors.New("published release contents changed after publication"), err)
	}
	return nil
}

func removeStagedRelease(release stagedRelease) error {
	if !release.Created || release.StagePath == "" {
		return nil
	}
	return removeOwnedReleaseDirectory(release.StagePath, release.Digest, release.Identity)
}

func removeJournalReleaseArtifacts(journal installJournal) error {
	if !journal.NewReleaseCreated {
		return nil
	}
	for _, path := range []string{journal.NewReleaseStage, journal.NewCurrent} {
		if err := removeOwnedReleaseDirectory(path, journal.NewReleaseDigest, journal.NewReleaseIdentity); err != nil {
			return err
		}
	}
	return nil
}

func removeOwnedReleaseDirectory(path, expectedDigest string, identity *setupFileIdentity) error {
	parentPath := filepath.Dir(path)
	quarantine := ".rollback-" + filepath.Base(path)
	quarantinePath := filepath.Join(parentPath, quarantine)
	info, pathErr := os.Lstat(path)
	quarantineInfo, quarantineErr := os.Lstat(quarantinePath)
	pathExists := pathErr == nil
	quarantineExists := quarantineErr == nil
	if pathErr != nil && !errors.Is(pathErr, os.ErrNotExist) {
		return pathErr
	}
	if quarantineErr != nil && !errors.Is(quarantineErr, os.ErrNotExist) {
		return quarantineErr
	}
	if pathExists && quarantineExists {
		return errors.New("release artifact and rollback quarantine both exist")
	}
	if !pathExists && !quarantineExists {
		return nil
	}
	if quarantineExists {
		if !quarantineInfo.IsDir() || quarantineInfo.Mode()&os.ModeSymlink != 0 ||
			!setupIdentityMatchesInfo(identity, quarantineInfo) {
			return errors.New("release rollback quarantine does not match the recorded identity")
		}
		if err := os.RemoveAll(quarantinePath); err != nil {
			return err
		}
		return syncDir(parentPath)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !setupIdentityMatchesInfo(identity, info) {
		return errors.New("release artifact does not match the recorded identity")
	}
	if digest, err := digestRegularFile(filepath.Join(path, "sclaude")); err != nil || digest != expectedDigest {
		return errors.Join(errors.New("release artifact does not match the recorded digest"), err)
	}
	parent, err := fssecure.Open(parentPath)
	if err != nil {
		return err
	}
	defer parent.Close()
	if err := parent.Revalidate(parentPath); err != nil {
		return err
	}
	if err := parent.RenameNoReplace(filepath.Base(path), quarantine); err != nil {
		return err
	}
	movedInfo, err := os.Lstat(quarantinePath)
	if err != nil || !movedInfo.IsDir() || movedInfo.Mode()&os.ModeSymlink != 0 ||
		!setupIdentityMatchesInfo(identity, movedInfo) {
		return errors.Join(errors.New("release artifact changed during rollback; ambiguous entry was retained"), err)
	}
	if err := syncInstallReleaseDirectory(parent); err != nil {
		return err
	}
	if err := os.RemoveAll(quarantinePath); err != nil {
		return err
	}
	return syncInstallReleaseDirectory(parent)
}

func installJournalComplete(layout InstallLayout, journal installJournal) bool {
	ledgerData, err := os.ReadFile(ledgerPath(layout))
	if err != nil || hexDigest(ledgerData) != journal.NewLedgerDigest {
		return false
	}
	target, err := readSymlink(currentPath(layout))
	if err != nil || resolveLinkTarget(currentPath(layout), target) != journal.NewCurrent {
		return false
	}
	expectedLauncher := filepath.Join(currentPath(layout), "sclaude")
	for _, path := range launcherPathsForSchema(layout, journal.SchemaVersion) {
		target, err := readSymlink(path)
		if err != nil || resolveLinkTarget(path, target) != expectedLauncher {
			return false
		}
	}
	tag := filepath.Base(journal.NewCurrent)
	return verifyRelease(layout, tag, journal.NewReleaseDigest, true) == nil
}

func restoreLink(path string, snapshot linkSnapshot) error {
	if !snapshot.Exists {
		return removeIfPresent(path)
	}
	return atomicSymlink(snapshot.Target, path)
}

func restoreLauncher(path string, snapshot linkSnapshot, installedTarget string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if !snapshot.Exists {
			return nil
		}
		return os.Symlink(snapshot.Target, path)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("launcher %s changed during activation; unmanaged entry was preserved", path)
	}
	target, err := os.Readlink(path)
	if err != nil {
		return err
	}
	if snapshot.Exists && target == snapshot.Target {
		return nil
	}
	if resolveLinkTarget(path, target) != installedTarget {
		return fmt.Errorf("launcher %s changed during activation; unmanaged entry was preserved", path)
	}
	return restoreLink(path, snapshot)
}

func snapshotLink(path string) (linkSnapshot, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return linkSnapshot{}, nil
	}
	if err != nil {
		return linkSnapshot{}, err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return linkSnapshot{}, fmt.Errorf("transaction target %s is not a symlink", path)
	}
	target, err := os.Readlink(path)
	return linkSnapshot{Exists: true, Target: target}, err
}

func planOwnedReleasePrune(layout InstallLayout, ledger *InstallLedger) ([]installReleaseArtifact, []string) {
	keep := map[string]bool{ledger.Current: true}
	if ledger.Previous != "" {
		keep[ledger.Previous] = true
	}
	tags := make([]string, 0, len(ledger.Releases))
	for tag := range ledger.Releases {
		if !keep[tag] {
			tags = append(tags, tag)
		}
	}
	sort.Strings(tags)
	artifacts := make([]installReleaseArtifact, 0, len(tags))
	var warnings []string
	for _, tag := range tags {
		digest := ledger.Releases[tag]
		path := releaseDir(layout, tag)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			delete(ledger.Releases, tag)
			delete(ledger.CodexReleases, tag)
			continue
		}
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("not pruning release %s: %v", tag, err))
			continue
		}
		if err := verifyRelease(layout, tag, digest, true); err != nil {
			warnings = append(warnings, fmt.Sprintf("not pruning release %s: %v", tag, err))
			continue
		}
		identity, err := setupIdentityFromInfo(info)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("not pruning release %s: %v", tag, err))
			continue
		}
		artifacts = append(artifacts, installReleaseArtifact{Path: path, Digest: digest, Identity: identity})
		delete(ledger.Releases, tag)
		delete(ledger.CodexReleases, tag)
	}
	return artifacts, warnings
}

func removePrunedReleaseArtifacts(artifacts []installReleaseArtifact) []string {
	return removePrunedReleaseArtifactsWith(artifacts, func(artifact installReleaseArtifact) error {
		return removeOwnedReleaseDirectory(artifact.Path, artifact.Digest, artifact.Identity)
	})
}

func removePrunedReleaseArtifactsWith(artifacts []installReleaseArtifact, remove func(installReleaseArtifact) error) []string {
	var warnings []string
	for _, artifact := range artifacts {
		if err := remove(artifact); err != nil {
			warnings = append(warnings, fmt.Sprintf("not pruning release %s: %v", filepath.Base(artifact.Path), err))
		}
	}
	return warnings
}

func verifyRelease(layout InstallLayout, tag, expected string, required bool) error {
	path := releaseDir(layout, tag)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && !required {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("owned release %s is not a regular directory", tag)
	}
	digest, err := digestRegularFile(filepath.Join(path, "sclaude"))
	if err != nil {
		return err
	}
	if digest != expected {
		return fmt.Errorf("owned release %s was modified", tag)
	}
	return nil
}

func digestRegularFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("path is not a regular non-symlink file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func digestPath(path string) (string, error) {
	if info, err := os.Lstat(path); err != nil {
		return "", err
	} else if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		return symlinkDigest(target), nil
	} else if !info.Mode().IsRegular() {
		return "", errors.New("owned path is not a regular file or symlink")
	}
	return digestRegularFile(path)
}

func symlinkDigest(target string) string {
	sum := sha256.Sum256([]byte("symlink:" + target))
	return hex.EncodeToString(sum[:])
}

func atomicSymlink(target, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".sclaude-link-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Remove(tmpPath); err != nil {
		return err
	}
	defer os.Remove(tmpPath)
	if err := os.Symlink(target, tmpPath); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func readSymlink(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return "", fmt.Errorf("%s is not a symlink", path)
	}
	return os.Readlink(path)
}

func resolveLinkTarget(path, target string) string {
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	return filepath.Clean(target)
}

func marshalLedger(ledger InstallLedger) ([]byte, error) {
	ledger.Warnings = nil
	data, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func writeLedger(layout InstallLayout, ledger InstallLedger) error {
	data, err := marshalLedger(ledger)
	if err != nil {
		return err
	}
	if err := writePrivateFileSynced(ledgerPath(layout), data, 0o600); err != nil {
		return err
	}
	return syncDir(layout.StateDir)
}

func persistMigratedLedger(layout InstallLayout, ledger InstallLedger) error {
	if ledger.SchemaVersion != 2 && ledger.SchemaVersion != installLedgerSchema {
		return errors.New("refusing to persist an unmigrated install ledger")
	}
	return writeLedger(layout, ledger)
}

func writeJSONPrivate(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFileSynced(path, append(data, '\n'), 0o600)
}

func writePrivateFileSynced(path string, data []byte, mode os.FileMode) error {
	if err := writePrivateFile(path, data, mode); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func removeIfPresent(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func hexDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func cloneStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneShellOwnership(source map[string]ShellBlockOwnership) map[string]ShellBlockOwnership {
	result := make(map[string]ShellBlockOwnership, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func syncDir(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func releasesDir(layout InstallLayout) string { return filepath.Join(layout.DataDir, "releases") }
func releaseDir(layout InstallLayout, tag string) string {
	return filepath.Join(releasesDir(layout), tag)
}
func releaseBinary(layout InstallLayout, tag string) string {
	return filepath.Join(releaseDir(layout, tag), "sclaude")
}
func currentPath(layout InstallLayout) string { return filepath.Join(layout.DataDir, "current") }
func ledgerPath(layout InstallLayout) string  { return filepath.Join(layout.StateDir, "ledger.json") }
func journalPath(layout InstallLayout) string {
	return filepath.Join(layout.StateDir, "transaction.json")
}
func lockPath(layout InstallLayout) string {
	return filepath.Dir(filepath.Dir(layout.StateDir))
}
func stableLauncherPaths(layout InstallLayout) []string {
	return launcherPathsForSchema(layout, installLedgerSchema)
}

func launcherPathsForSchema(layout InstallLayout, schema int) []string {
	paths := []string{filepath.Join(layout.BinDir, "sclaude"), filepath.Join(layout.BinDir, "sclaudex")}
	if schema >= 3 {
		paths = append(paths, filepath.Join(layout.BinDir, "scodex"))
	}
	return paths
}
