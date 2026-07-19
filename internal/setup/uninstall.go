package setup

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ctrl-alt-raccoon/sclaude/internal/config"
	"github.com/ctrl-alt-raccoon/sclaude/internal/fssecure"
)

const uninstallJournalSchema = 1

type uninstallJournalEntry struct {
	Path              string `json:"path"`
	Backup            string `json:"backup"`
	WriteReplacement  bool   `json:"write_replacement,omitempty"`
	ReplacementDigest string `json:"replacement_digest,omitempty"`
	Mode              uint32 `json:"mode,omitempty"`
}

type uninstallJournal struct {
	SchemaVersion int                     `json:"schema_version"`
	Layout        InstallLayout           `json:"layout"`
	Token         string                  `json:"token"`
	Purge         bool                    `json:"purge"`
	Committed     bool                    `json:"committed"`
	Entries       []uninstallJournalEntry `json:"entries"`
}

type preparedUninstallEntry struct {
	journalEntry uninstallJournalEntry
	replacement  []byte
}

type uninstallOps struct {
	rename       func(string, string) error
	writeFile    func(string, []byte, os.FileMode) error
	writeJournal func(string, any) error
	removeLedger func(string) error
}

var beforeUninstallStatePurge func(string) error

func defaultUninstallOps() uninstallOps {
	return uninstallOps{
		rename: os.Rename,
		writeFile: func(path string, data []byte, mode os.FileMode) error {
			return writePrivateFileSynced(path, data, mode)
		},
		writeJournal: writeJSONPrivate,
		removeLedger: removeIfPresent,
	}
}

func uninstallWithOps(
	paths config.Paths,
	layout InstallLayout,
	purge bool,
	inactive func() error,
	ops uninstallOps,
) (resultErr error) {
	if ops.rename == nil || ops.writeFile == nil || ops.writeJournal == nil || ops.removeLedger == nil {
		return errors.New("uninstall operations are incomplete")
	}
	var err error
	layout, err = normalizeInstallLayout(layout)
	if err != nil {
		return err
	}
	expectedStateRoot := filepath.Dir(layout.StateDir)
	if filepath.Base(layout.StateDir) != "install" {
		expectedStateRoot = layout.StateDir
	}
	if filepath.Clean(paths.StateRoot) != expectedStateRoot ||
		filepath.Clean(paths.InstallState) != layout.StateDir ||
		filepath.Clean(paths.DataRoot) != layout.DataDir {
		return errors.New("runtime paths do not match the installed layout")
	}
	setupLock, err := acquireSetupAdmissionLock(paths.StateRoot, false)
	if err != nil {
		return err
	}
	if setupLock == nil {
		return os.ErrNotExist
	}
	defer func() {
		if releaseErr := setupLock.release(); releaseErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("release setup admission lock: %w", releaseErr))
		}
	}()

	return withInstallLock(layout, func() error {
		journal, journalErr := loadUninstallJournal(uninstallJournalPath(layout))
		switch {
		case journalErr == nil:
			layout, err = uninstallJournalLayout(paths, layout, journal)
			if err != nil {
				return err
			}
			if journal.Committed {
				return finalizeUninstallJournal(paths, layout, journal, ops.removeLedger, setupLock)
			}
			if err := restoreUninstallJournal(layout, journal); err != nil {
				return err
			}
		case !errors.Is(journalErr, os.ErrNotExist):
			return journalErr
		}
		layout, err = installedUninstallLayout(layout)
		if err != nil {
			return err
		}
		if err := recoverInstallJournal(layout); err != nil {
			return err
		}
		layout, err = installedUninstallLayout(layout)
		if err != nil {
			return err
		}
		if err := setupLock.revalidate(); err != nil {
			return err
		}
		pending, err := setupTransactionPendingLocked(setupLock)
		if err != nil {
			return fmt.Errorf("inspect setup transaction before uninstall: %w", err)
		}
		if pending {
			return errors.New("an interrupted setup transaction requires recovery before uninstall")
		}
		if inactive != nil {
			if err := inactive(); err != nil {
				return err
			}
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

		journal, prepared, err := prepareUninstall(paths, layout, ledger, purge)
		if err != nil {
			return err
		}
		if err := ops.writeJournal(uninstallJournalPath(layout), journal); err != nil {
			return err
		}
		rollback := func(primary error) error {
			if recoveryErr := restoreUninstallJournal(layout, journal); recoveryErr != nil {
				return fmt.Errorf("%w; restore prior install state: %v", primary, recoveryErr)
			}
			return primary
		}
		for _, entry := range prepared {
			if err := applyPreparedUninstallEntry(entry, ops); err != nil {
				return rollback(err)
			}
		}

		journal.Committed = true
		if err := ops.writeJournal(uninstallJournalPath(layout), journal); err != nil {
			onDisk, loadErr := loadUninstallJournal(uninstallJournalPath(layout))
			if loadErr != nil {
				return fmt.Errorf("commit uninstall transaction: %w; inspect commit state: %v", err, loadErr)
			}
			if onDisk.Committed {
				return finalizeUninstallJournal(paths, layout, onDisk, ops.removeLedger, setupLock)
			}
			return rollback(err)
		}
		return finalizeUninstallJournal(paths, layout, journal, ops.removeLedger, setupLock)
	})
}

func uninstallJournalLayout(paths config.Paths, requested InstallLayout, journal uninstallJournal) (InstallLayout, error) {
	layout := journal.Layout
	if layout.DataDir != requested.DataDir || layout.StateDir != requested.StateDir ||
		filepath.Clean(paths.InstallState) != layout.StateDir ||
		filepath.Clean(paths.DataRoot) != layout.DataDir {
		return InstallLayout{}, errors.New("uninstall transaction journal data/state paths do not match the current user installation")
	}
	return layout, nil
}

func installedUninstallLayout(requested InstallLayout) (InstallLayout, error) {
	ledger, err := LoadInstallLedger(ledgerPath(requested))
	if errors.Is(err, os.ErrNotExist) {
		return requested, nil
	}
	if err != nil {
		return InstallLayout{}, err
	}
	layout := requested
	layout.BinDir = ledger.BinDir
	if ledger.SchemaVersion == installLedgerSchema {
		layout.DataDir = ledger.DataDir
		layout.StateDir = ledger.StateDir
	}
	layout, err = normalizeInstallLayout(layout)
	if err != nil {
		return InstallLayout{}, err
	}
	if layout.DataDir != requested.DataDir || layout.StateDir != requested.StateDir {
		return InstallLayout{}, errors.New("install ledger data/state paths do not match the requested layout")
	}
	return layout, nil
}

func prepareUninstall(paths config.Paths, layout InstallLayout, ledger InstallLedger, purge bool) (uninstallJournal, []preparedUninstallEntry, error) {
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return uninstallJournal{}, nil, err
	}
	journal := uninstallJournal{
		SchemaVersion: uninstallJournalSchema,
		Layout:        layout,
		Token:         hex.EncodeToString(tokenBytes),
		Purge:         purge,
	}
	prepared := []preparedUninstallEntry{}
	seen := map[string]bool{}
	appendEntry := func(path string, replacement []byte, writeReplacement bool, mode os.FileMode) error {
		path = filepath.Clean(path)
		if !filepath.IsAbs(path) || seen[path] {
			return fmt.Errorf("duplicate or nonabsolute uninstall path %s", path)
		}
		seen[path] = true
		if _, err := os.Lstat(path); err != nil {
			return fmt.Errorf("preflight uninstall path %s: %w", path, err)
		}
		entry := uninstallJournalEntry{
			Path:             path,
			Backup:           uninstallBackupPath(path, journal.Token, len(prepared)),
			WriteReplacement: writeReplacement,
			Mode:             uint32(mode.Perm()),
		}
		if writeReplacement {
			if mode.Perm() == 0 {
				return errors.New("uninstall replacement mode is empty")
			}
			entry.ReplacementDigest = hexDigest(replacement)
		}
		if _, err := os.Lstat(entry.Backup); err == nil {
			return fmt.Errorf("uninstall backup path already exists: %s", entry.Backup)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		prepared = append(prepared, preparedUninstallEntry{journalEntry: entry, replacement: replacement})
		return nil
	}

	shellPaths := make([]string, 0, len(ledger.ShellBlocks))
	for path := range ledger.ShellBlocks {
		shellPaths = append(shellPaths, path)
	}
	sort.Strings(shellPaths)
	for _, path := range shellPaths {
		plan, err := prepareOwnedShellRemoval(path, ledger.ShellBlocks[path])
		if err != nil {
			return uninstallJournal{}, nil, err
		}
		target, err := shellRemovalTarget(plan.path)
		if err != nil {
			return uninstallJournal{}, nil, err
		}
		if err := appendEntry(target, plan.data, !plan.remove, plan.mode); err != nil {
			return uninstallJournal{}, nil, err
		}
	}

	for _, path := range stableLauncherPaths(layout) {
		if err := appendEntry(path, nil, false, 0); err != nil {
			return uninstallJournal{}, nil, err
		}
	}
	if err := appendEntry(currentPath(layout), nil, false, 0); err != nil {
		return uninstallJournal{}, nil, err
	}

	tags := make([]string, 0, len(ledger.Releases))
	for tag := range ledger.Releases {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	for _, tag := range tags {
		if err := validateReleaseTag(tag); err != nil {
			return uninstallJournal{}, nil, fmt.Errorf("invalid owned release in ledger: %w", err)
		}
		releaseExists, err := verifyOwnedReleaseDirectory(
			layout,
			tag,
			ledger.Releases[tag],
			tag == ledger.Current || tag == ledger.Previous,
		)
		if err != nil {
			return uninstallJournal{}, nil, err
		}
		if !releaseExists {
			continue
		}
		if err := appendEntry(releaseDir(layout, tag), nil, false, 0); err != nil {
			return uninstallJournal{}, nil, err
		}
	}

	if purge {
		if filepath.Clean(paths.InstallState) != layout.StateDir || filepath.Clean(paths.DataRoot) != layout.DataDir {
			return uninstallJournal{}, nil, errors.New("runtime paths do not match the installed layout")
		}
		for _, path := range []string{paths.ConfigFile, paths.Credential, paths.ManagedSettings} {
			info, err := os.Lstat(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return uninstallJournal{}, nil, err
			}
			if info.IsDir() {
				return uninstallJournal{}, nil, fmt.Errorf("refusing to purge directory at file path %s", path)
			}
			if err := appendEntry(path, nil, false, 0); err != nil {
				return uninstallJournal{}, nil, err
			}
		}
	}

	journal.Entries = make([]uninstallJournalEntry, len(prepared))
	for index, entry := range prepared {
		journal.Entries[index] = entry.journalEntry
	}
	return journal, prepared, nil
}

func applyPreparedUninstallEntry(entry preparedUninstallEntry, ops uninstallOps) error {
	item := entry.journalEntry
	if err := ops.rename(item.Path, item.Backup); err != nil {
		return fmt.Errorf("stage uninstall path %s: %w", item.Path, err)
	}
	if err := syncDir(filepath.Dir(item.Path)); err != nil {
		return err
	}
	if !item.WriteReplacement {
		return nil
	}
	if err := ops.writeFile(item.Path, entry.replacement, os.FileMode(item.Mode)); err != nil {
		return fmt.Errorf("write uninstall replacement %s: %w", item.Path, err)
	}
	return nil
}

func recoverUninstallJournal(layout InstallLayout) (bool, error) {
	return recoverUninstallJournalLocked(layout, nil)
}

func recoverUninstallJournalLocked(layout InstallLayout, setupLock *setupAdmissionLock) (bool, error) {
	journal, err := loadUninstallJournal(uninstallJournalPath(layout))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if journal.Layout != layout {
		return false, errors.New("uninstall transaction journal layout does not match the requested layout")
	}
	if journal.Committed {
		paths, err := config.DefaultPaths()
		if err != nil {
			return false, err
		}
		return true, finalizeUninstallJournal(paths, layout, journal, removeIfPresent, setupLock)
	}
	return false, restoreUninstallJournal(layout, journal)
}

func restoreUninstallJournal(layout InstallLayout, journal uninstallJournal) error {
	for index := len(journal.Entries) - 1; index >= 0; index-- {
		entry := journal.Entries[index]
		_, backupErr := os.Lstat(entry.Backup)
		if errors.Is(backupErr, os.ErrNotExist) {
			if _, originalErr := os.Lstat(entry.Path); originalErr != nil {
				return fmt.Errorf("restore uninstall path %s: original and backup are both missing", entry.Path)
			}
			continue
		}
		if backupErr != nil {
			return backupErr
		}

		_, originalErr := os.Lstat(entry.Path)
		if originalErr == nil {
			if !entry.WriteReplacement {
				return fmt.Errorf("restore uninstall path %s: unexpected replacement exists", entry.Path)
			}
			digest, err := digestRegularFile(entry.Path)
			if err != nil || digest != entry.ReplacementDigest {
				return fmt.Errorf("restore uninstall path %s: replacement was modified", entry.Path)
			}
			if err := os.Remove(entry.Path); err != nil {
				return err
			}
		} else if !errors.Is(originalErr, os.ErrNotExist) {
			return originalErr
		}
		if err := os.Rename(entry.Backup, entry.Path); err != nil {
			return err
		}
		if err := syncDir(filepath.Dir(entry.Path)); err != nil {
			return err
		}
	}
	if err := removeIfPresent(uninstallJournalPath(layout)); err != nil {
		return err
	}
	return syncDir(layout.StateDir)
}

func finalizeUninstallJournal(paths config.Paths, layout InstallLayout, journal uninstallJournal, removeLedger func(string) error, setupLock *setupAdmissionLock) error {
	if !journal.Committed {
		return errors.New("refusing to finalize an uncommitted uninstall transaction")
	}
	if err := removeLedger(ledgerPath(layout)); err != nil {
		return fmt.Errorf("remove install ledger after committed uninstall: %w", err)
	}
	if err := syncDir(layout.StateDir); err != nil {
		return err
	}
	for _, entry := range journal.Entries {
		if err := os.RemoveAll(entry.Backup); err != nil {
			return fmt.Errorf("remove uninstall backup %s: %w", entry.Backup, err)
		}
		if err := syncDir(filepath.Dir(entry.Backup)); err != nil {
			return err
		}
	}

	if journal.Purge {
		if filepath.Clean(paths.StateRoot) != filepath.Dir(layout.StateDir) {
			return errors.New("runtime state root does not match the uninstall journal")
		}
		if setupLock == nil {
			return errors.New("purge requires the setup admission lock")
		}
		if err := setupLock.revalidate(); err != nil {
			return err
		}
		if beforeUninstallStatePurge != nil {
			if err := beforeUninstallStatePurge(paths.StateRoot); err != nil {
				return err
			}
		}
		if err := setupLock.revalidate(); err != nil {
			return err
		}
		for _, path := range []string{
			paths.SessionsDir,
			paths.LaunchDir,
			filepath.Join(paths.StateRoot, "lock"),
			paths.InstallState,
		} {
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("remove purged state %s: %w", path, err)
			}
		}
		if err := setupLock.directory.Sync(); err != nil {
			return fmt.Errorf("sync purged state root: %w", err)
		}
		if err := setupLock.revalidate(); err != nil {
			return err
		}
	} else {
		if err := removeIfPresent(uninstallJournalPath(layout)); err != nil {
			return err
		}
		if err := syncDir(layout.StateDir); err != nil {
			return err
		}
		_ = os.Remove(layout.StateDir)
	}
	_ = os.Remove(releasesDir(layout))
	_ = os.Remove(layout.DataDir)
	if journal.Purge {
		for _, path := range []string{paths.ConfigFile, paths.Credential, paths.ManagedSettings} {
			_ = os.Remove(filepath.Dir(path))
		}
	}
	return nil
}

func loadUninstallJournal(path string) (uninstallJournal, error) {
	parent, err := fssecure.Open(filepath.Dir(path))
	if err != nil {
		return uninstallJournal{}, err
	}
	defer parent.Close()
	data, info, err := parent.ReadRegular(filepath.Base(path))
	if err != nil {
		return uninstallJournal{}, err
	}
	if info.Mode() != 0o600 {
		return uninstallJournal{}, errors.New("uninstall transaction journal must have mode 0600")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var journal uninstallJournal
	if err := decoder.Decode(&journal); err != nil {
		return uninstallJournal{}, fmt.Errorf("parse uninstall transaction journal: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return uninstallJournal{}, errors.New("parse uninstall transaction journal: trailing data")
	}
	if err := validateUninstallJournal(journal); err != nil {
		return uninstallJournal{}, err
	}
	return journal, nil
}

func validateUninstallJournal(journal uninstallJournal) error {
	if journal.SchemaVersion != uninstallJournalSchema {
		return fmt.Errorf("unsupported uninstall transaction journal schema %d", journal.SchemaVersion)
	}
	layout, err := normalizeInstallLayout(journal.Layout)
	if err != nil || layout != journal.Layout {
		return errors.New("uninstall transaction journal has an invalid layout")
	}
	token, err := hex.DecodeString(journal.Token)
	if err != nil || len(token) != 16 || strings.ToLower(journal.Token) != journal.Token {
		return errors.New("uninstall transaction journal has an invalid token")
	}
	seen := map[string]bool{}
	for index, entry := range journal.Entries {
		if !filepath.IsAbs(entry.Path) || filepath.Clean(entry.Path) != entry.Path || seen[entry.Path] {
			return errors.New("uninstall transaction journal has an invalid path")
		}
		seen[entry.Path] = true
		if entry.Backup != uninstallBackupPath(entry.Path, journal.Token, index) {
			return errors.New("uninstall transaction journal has an invalid backup path")
		}
		if entry.WriteReplacement {
			digest, err := hex.DecodeString(entry.ReplacementDigest)
			if err != nil || len(digest) != 32 || entry.Mode == 0 || entry.Mode > 0o777 {
				return errors.New("uninstall transaction journal has an invalid replacement")
			}
		} else if entry.ReplacementDigest != "" || entry.Mode != 0 {
			return errors.New("uninstall transaction journal has unexpected replacement metadata")
		}
	}
	return nil
}

func shellRemovalTarget(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return filepath.Clean(path), nil
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", err
	}
	return filepath.Clean(target), nil
}

func verifyOwnedReleaseDirectory(
	layout InstallLayout,
	tag,
	expected string,
	required bool,
) (bool, error) {
	path := releaseDir(layout, tag)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if required {
			return false, err
		}
		return false, nil
	} else if err != nil {
		return false, err
	}
	if err := verifyRelease(layout, tag, expected, true); err != nil {
		return false, err
	}
	return true, nil
}

func uninstallBackupPath(path, token string, index int) string {
	return filepath.Join(filepath.Dir(path), fmt.Sprintf(".%s.sclaude-uninstall-%s-%d", filepath.Base(path), token, index))
}

func uninstallJournalPath(layout InstallLayout) string {
	return filepath.Join(layout.StateDir, "uninstall.json")
}
