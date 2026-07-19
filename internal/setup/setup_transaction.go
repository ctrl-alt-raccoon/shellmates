package setup

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"syscall"

	"github.com/ctrl-alt-raccoon/sclaude/internal/config"
	"github.com/ctrl-alt-raccoon/sclaude/internal/fssecure"
	"github.com/ctrl-alt-raccoon/sclaude/internal/stateroot"
)

const (
	setupJournalSchema       = 4
	markerSetupJournalSchema = 3
	legacySetupJournalSchema = 2
)

var setupTokenPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var setupDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var afterSetupParentOpen func(string) error
var beforeSetupAdmissionFlock func(string) error
var afterSetupAdmissionLock func(string) error
var afterSetupFileInitialRevalidation func(string) error
var beforeSetupFilePublication func(string) error
var afterSetupFilePublication func(string) error
var beforeSetupFileRollback func(string) error
var beforeSetupCreatedTargetRemoval func(string) error
var beforeSetupArtifactRemoval func(string) error
var beforeSetupJournalRemoval func(string) error
var removeSetupRegularOutcome = func(parent *fssecure.Directory, name string, expected os.FileInfo, beforeRemove func() error) (bool, error) {
	return parent.RemoveRegularOutcome(name, expected, beforeRemove)
}
var syncSetupDirectory = func(parent *fssecure.Directory) error {
	return parent.Sync()
}
var beforeCommitSetupTransaction func() error
var beforeFinalizeSetupTransaction func() error

type setupFileIdentity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

type setupJournalEntry struct {
	Path           string             `json:"path"`
	Backup         string             `json:"backup,omitempty"`
	Staged         string             `json:"staged,omitempty"`
	Replacement    string             `json:"replacement,omitempty"`
	Existed        bool               `json:"existed"`
	Mode           uint32             `json:"mode,omitempty"`
	AfterMode      uint32             `json:"after_mode"`
	BeforeDigest   string             `json:"before_digest,omitempty"`
	AfterDigest    string             `json:"after_digest"`
	FinalIdentity  *setupFileIdentity `json:"final_identity,omitempty"`
	BackupIdentity *setupFileIdentity `json:"backup_identity,omitempty"`
	StagedIdentity *setupFileIdentity `json:"staged_identity,omitempty"`
}

type setupJournal struct {
	SchemaVersion           int                 `json:"schema_version"`
	Token                   string              `json:"token"`
	Committed               bool                `json:"committed"`
	InstallLock             string              `json:"install_lock,omitempty"`
	ServiceManager          string              `json:"service_manager,omitempty"`
	ServiceExecutable       string              `json:"service_executable,omitempty"`
	ServiceExecutableDigest string              `json:"service_executable_digest,omitempty"`
	ServiceActive           bool                `json:"service_active,omitempty"`
	ServiceScheduled        bool                `json:"service_scheduled,omitempty"`
	ServiceActiveTouched    bool                `json:"service_active_touched,omitempty"`
	ServiceScheduledTouched bool                `json:"service_scheduled_touched,omitempty"`
	OAuthAttempted          bool                `json:"oauth_attempted,omitempty"`
	OAuthCompleted          bool                `json:"oauth_completed,omitempty"`
	Entries                 []setupJournalEntry `json:"entries"`
}

type setupPreparedFile struct {
	entry         setupJournalEntry
	before        []byte
	beforeInfo    os.FileInfo
	parentInfo    os.FileInfo
	parentExisted bool
	after         []byte
	afterInfo     os.FileInfo
}

type setupTransaction struct {
	stateRoot   string
	journalPath string
	journalDir  *fssecure.Directory
	journal     setupJournal
	files       []setupPreparedFile
	persisted   bool
}

type setupAdmissionLock struct {
	stateRoot string
	directory *fssecure.Directory
	lock      *stateroot.Lock
}

func acquireSetupAdmissionLock(stateRoot string, create bool) (*setupAdmissionLock, error) {
	lock, err := stateroot.Acquire(stateRoot, create, stateroot.Hooks{
		BeforeFlock: beforeSetupAdmissionFlock,
		AfterLock:   afterSetupAdmissionLock,
	})
	if err != nil || lock == nil {
		return nil, err
	}
	return &setupAdmissionLock{
		stateRoot: lock.Path(),
		directory: lock.Directory(),
		lock:      lock,
	}, nil
}

func (lock *setupAdmissionLock) release() error {
	if lock == nil || lock.lock == nil {
		return nil
	}
	err := lock.lock.Release()
	lock.directory = nil
	lock.lock = nil
	return err
}

func (lock *setupAdmissionLock) journalPath() string {
	if lock == nil {
		return ""
	}
	return setupJournalPath(lock.stateRoot)
}

func (lock *setupAdmissionLock) revalidate() error {
	if lock == nil || lock.lock == nil {
		return errors.New("setup admission lock is closed")
	}
	return lock.lock.Revalidate()
}

func (lock *setupAdmissionLock) loadJournal() (setupJournal, error) {
	if err := lock.revalidate(); err != nil {
		return setupJournal{}, err
	}
	return loadSetupJournalFromDirectory(lock.directory, filepath.Base(lock.journalPath()))
}

func (lock *setupAdmissionLock) writeJournal(journal setupJournal) error {
	if err := lock.revalidate(); err != nil {
		return err
	}
	return writeSetupJournalToDirectory(lock.directory, filepath.Base(lock.journalPath()), journal)
}

func (lock *setupAdmissionLock) removeJournal(journal setupJournal) error {
	return lock.removeJournalVerified(journal, nil)
}

func (lock *setupAdmissionLock) removeJournalVerified(journal setupJournal, beforeRemove func() error) error {
	if err := lock.revalidate(); err != nil {
		return err
	}
	return removeSetupJournalFromDirectoryVerified(
		lock.directory,
		filepath.Base(lock.journalPath()),
		journal,
		beforeRemove,
	)
}

func newSetupTransaction(stateRoot string) (*setupTransaction, error) {
	return newSetupTransactionWithDirectory(stateRoot, nil)
}

func newSetupTransactionLocked(lock *setupAdmissionLock) (*setupTransaction, error) {
	if err := lock.revalidate(); err != nil {
		return nil, err
	}
	return newSetupTransactionWithDirectory(lock.stateRoot, lock.directory)
}

func newSetupTransactionWithDirectory(stateRoot string, journalDir *fssecure.Directory) (*setupTransaction, error) {
	absolute, err := absoluteCleanPath(stateRoot, "setup state root")
	if err != nil {
		return nil, err
	}
	journalPath := setupJournalPath(absolute)
	if journalDir != nil {
		info, inspectErr := journalDir.InspectRegular(filepath.Base(journalPath))
		if inspectErr != nil {
			return nil, fmt.Errorf("inspect setup transaction journal: %w", inspectErr)
		}
		if info != nil {
			return nil, errors.New("a setup transaction journal already exists")
		}
	} else if _, err := os.Lstat(journalPath); err == nil {
		return nil, errors.New("a setup transaction journal already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect setup transaction journal: %w", err)
	}
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("generate setup transaction token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)
	return &setupTransaction{
		stateRoot:   absolute,
		journalPath: journalPath,
		journalDir:  journalDir,
		journal: setupJournal{
			SchemaVersion: setupJournalSchema,
			Token:         token,
		},
	}, nil
}

func (transaction *setupTransaction) revalidateJournalDirectory() error {
	if transaction.journalDir == nil {
		return nil
	}
	if err := transaction.journalDir.Revalidate(transaction.stateRoot); err != nil {
		return fmt.Errorf("setup state root changed during the transaction: %w", err)
	}
	return nil
}

func (transaction *setupTransaction) writeJournal() error {
	validateTargets := func() error {
		if !transaction.persisted || !transaction.journal.Committed {
			return nil
		}
		return validateCommittedSetupTargets(transaction.journal)
	}
	writeNew := !transaction.persisted
	if transaction.journalDir != nil {
		if err := transaction.revalidateJournalDirectory(); err != nil {
			return err
		}
		return writeSetupJournalToDirectoryVerified(
			transaction.journalDir,
			filepath.Base(transaction.journalPath),
			transaction.journal,
			validateTargets,
			writeNew,
		)
	}
	return writeSetupJournalVerified(
		transaction.journalPath,
		transaction.journal,
		validateTargets,
		writeNew,
	)
}

func (transaction *setupTransaction) loadJournal() (setupJournal, error) {
	if transaction.journalDir != nil {
		if err := transaction.revalidateJournalDirectory(); err != nil {
			return setupJournal{}, err
		}
		return loadSetupJournalFromDirectory(transaction.journalDir, filepath.Base(transaction.journalPath))
	}
	return loadSetupJournal(transaction.journalPath)
}

func (transaction *setupTransaction) removeJournal(journal setupJournal) error {
	return transaction.removeJournalVerified(journal, nil)
}

func (transaction *setupTransaction) removeJournalVerified(journal setupJournal, beforeRemove func() error) error {
	if transaction.journalDir != nil {
		if err := transaction.revalidateJournalDirectory(); err != nil {
			return err
		}
		return removeSetupJournalFromDirectoryVerified(
			transaction.journalDir,
			filepath.Base(transaction.journalPath),
			journal,
			beforeRemove,
		)
	}
	return removeSetupJournalVerified(transaction.journalPath, journal, beforeRemove)
}

func (transaction *setupTransaction) finalizeJournal(journal setupJournal) error {
	return finalizeSetupJournalWithRemoval(journal, transaction.removeJournalVerified)
}

func (transaction *setupTransaction) setInstallLock(path string) error {
	if transaction.persisted {
		return errors.New("cannot set the setup install lock after the journal is persisted")
	}
	absolute, err := absoluteCleanPath(path, "setup install lock")
	if err != nil {
		return err
	}
	transaction.journal.InstallLock = absolute
	return nil
}

func (transaction *setupTransaction) setServiceState(service preparedServiceState) error {
	if transaction.persisted {
		return errors.New("cannot set setup service state after the journal is persisted")
	}
	transaction.journal.ServiceManager = service.manager
	transaction.journal.ServiceExecutable = service.executable.path()
	transaction.journal.ServiceExecutableDigest = service.executable.digest
	transaction.journal.ServiceActive = service.active
	transaction.journal.ServiceScheduled = service.scheduled
	return nil
}

func (transaction *setupTransaction) updateServiceState(service preparedServiceState, oauthAttempted, oauthCompleted bool) error {
	transaction.journal.ServiceManager = service.manager
	transaction.journal.ServiceExecutable = service.executable.path()
	transaction.journal.ServiceExecutableDigest = service.executable.digest
	transaction.journal.ServiceActive = service.active
	transaction.journal.ServiceScheduled = service.scheduled
	transaction.journal.ServiceActiveTouched = service.activeTouched
	transaction.journal.ServiceScheduledTouched = service.scheduledTouched
	transaction.journal.OAuthAttempted = oauthAttempted
	transaction.journal.OAuthCompleted = oauthCompleted
	if !transaction.persisted {
		return nil
	}
	if err := transaction.writeJournal(); err != nil {
		return fmt.Errorf("persist setup transaction service state: %w", err)
	}
	return nil
}

func (transaction *setupTransaction) addFile(path string, after []byte, mode os.FileMode) (int, error) {
	if transaction.persisted {
		return -1, errors.New("cannot add setup files after the journal is persisted")
	}
	absolute, err := absoluteCleanPath(path, "setup target")
	if err != nil {
		return -1, err
	}
	if !isExactPermissionMode(mode) {
		return -1, errors.New("setup target mode must contain only nonzero permission bits")
	}
	for _, prepared := range transaction.files {
		if prepared.entry.Path == absolute {
			return -1, fmt.Errorf("duplicate setup target %s", absolute)
		}
	}

	prepared := setupPreparedFile{
		after: append([]byte(nil), after...),
		entry: setupJournalEntry{
			Path:        absolute,
			Backup:      setupBackupPath(absolute, transaction.journal.Token),
			Staged:      setupStagedPath(absolute, transaction.journal.Token),
			Replacement: setupReplacementPath(absolute, transaction.journal.Token),
			AfterMode:   uint32(mode.Perm()),
			AfterDigest: hexDigest(after),
		},
	}
	if parentInfo, parentErr := os.Lstat(filepath.Dir(absolute)); parentErr == nil {
		if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
			return -1, errors.New("setup target parent must be a non-symlink directory")
		}
		prepared.parentInfo = parentInfo
		prepared.parentExisted = true
	} else if !errors.Is(parentErr, os.ErrNotExist) {
		return -1, fmt.Errorf("inspect setup target parent: %w", parentErr)
	}
	before, info, err := readStableRegularFile(absolute)
	switch {
	case err == nil:
		if !isExactPermissionMode(info.Mode()) {
			return -1, fmt.Errorf("setup target %s has unsupported mode %v", absolute, info.Mode())
		}
		prepared.before = before
		prepared.beforeInfo = info
		prepared.entry.Existed = true
		prepared.entry.Mode = uint32(info.Mode().Perm())
		prepared.entry.BeforeDigest = hexDigest(before)
		prepared.entry.BackupIdentity, err =
			setupIdentityFromInfo(info)
		if err != nil {
			return -1, fmt.Errorf(
				"capture prepared setup target identity: %w",
				err,
			)
		}
		if setupPreparedFileIsNoop(&prepared) {
			prepared.entry.FinalIdentity =
				prepared.entry.BackupIdentity
		}
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return -1, fmt.Errorf("snapshot setup target %s: %w", absolute, err)
	}
	for _, artifact := range setupEntryArtifacts(prepared.entry) {
		if _, err := os.Lstat(artifact); err == nil {
			return -1, fmt.Errorf("setup transaction artifact already exists: %s", artifact)
		} else if !errors.Is(err, os.ErrNotExist) {
			return -1, err
		}
	}
	transaction.files = append(transaction.files, prepared)
	transaction.journal.Entries = append(transaction.journal.Entries, prepared.entry)
	return len(transaction.files) - 1, nil
}

func (transaction *setupTransaction) persist() error {
	if transaction.persisted {
		return nil
	}
	if len(transaction.files) == 0 {
		return errors.New("setup transaction has no files")
	}
	if err := validateSetupJournalForPersistence(transaction.journal); err != nil {
		return err
	}
	if err := transaction.writeJournal(); err != nil {
		return fmt.Errorf("persist setup transaction journal: %w", err)
	}
	transaction.persisted = true
	return nil
}

const noExactSetupParentMode os.FileMode = 0

func (transaction *setupTransaction) applyFile(index int) error {
	if !transaction.persisted {
		return errors.New("setup transaction journal is not persisted")
	}
	if index < 0 || index >= len(transaction.files) {
		return errors.New("setup transaction file index is invalid")
	}
	prepared := &transaction.files[index]
	return transaction.applyPreparedFile(
		index,
		prepared,
		"setup target "+prepared.entry.Path,
		noExactSetupParentMode,
	)
}

func (transaction *setupTransaction) applyRuntime(index int, _ config.Runtime) error {
	if !transaction.persisted {
		return errors.New("setup transaction journal is not persisted")
	}
	if index < 0 || index >= len(transaction.files) {
		return errors.New("setup transaction file index is invalid")
	}
	return transaction.applyPreparedFile(
		index,
		&transaction.files[index],
		"runtime configuration",
		0o700,
	)
}

func (transaction *setupTransaction) applyPreparedFile(
	index int,
	prepared *setupPreparedFile,
	description string,
	exactParentMode os.FileMode,
) error {
	parent, err := openSetupParent(prepared.entry.Path, true)
	if err != nil {
		return err
	}
	defer parent.Close()
	if err := revalidateSetupPreparedParent(parent, *prepared); err != nil {
		return err
	}
	if err := revalidateSetupPreparedFileInParent(parent, prepared); err != nil {
		return err
	}
	if exactParentMode != noExactSetupParentMode &&
		parent.Info().Mode() != os.ModeDir|exactParentMode {
		if err := parent.Chmod(exactParentMode); err != nil {
			return fmt.Errorf("secure %s parent: %w", description, err)
		}
		if err := parent.Revalidate(filepath.Dir(prepared.entry.Path)); err != nil {
			return fmt.Errorf("%s parent changed while securing it: %w", description, err)
		}
		prepared.parentInfo = parent.Info()
		prepared.parentExisted = true
	}
	if err := runAfterSetupFileInitialRevalidation(prepared.entry.Path); err != nil {
		return err
	}
	if setupPreparedFileIsNoop(prepared) {
		if err := revalidateSetupPreparedFileInParent(parent, prepared); err != nil {
			return err
		}
		prepared.afterInfo = prepared.beforeInfo
		return revalidateSetupPreparedParent(parent, *prepared)
	}
	entry := prepared.entry
	var stagedInfo os.FileInfo
	if err := parent.WriteNewVerified(
		filepath.Base(entry.Staged),
		prepared.after,
		os.FileMode(entry.AfterMode),
		func(info os.FileInfo) error {
			identity, err := setupIdentityFromInfo(info)
			if err != nil {
				return fmt.Errorf("capture staged %s identity: %w", description, err)
			}
			prepared.entry.StagedIdentity = identity
			transaction.journal.Entries[index] = prepared.entry
			if err := transaction.writeJournal(); err != nil {
				return fmt.Errorf(
					"persist staged %s identity: %w",
					description,
					err,
				)
			}
			return nil
		},
		func(info os.FileInfo) error {
			stagedInfo = info
			return nil
		},
	); err != nil {
		return fmt.Errorf("stage %s: %w", description, err)
	}
	stagedInfo, err = validateSetupArtifactIdentity(
		parent,
		entry.Staged,
		entry.AfterDigest,
		os.FileMode(entry.AfterMode),
		prepared.entry.StagedIdentity,
	)
	if err != nil {
		return fmt.Errorf("validate staged %s: %w", description, err)
	}
	entry = prepared.entry
	if err := revalidateSetupPreparedParent(parent, *prepared); err != nil {
		return err
	}
	if err := revalidateSetupPreparedFileInParent(parent, prepared); err != nil {
		return err
	}
	if entry.Existed {
		if err := parent.Link(filepath.Base(entry.Path), filepath.Base(entry.Backup)); err != nil {
			return fmt.Errorf("back up %s: %w", description, err)
		}
		backupInfo, err := validateSetupArtifact(parent, entry.Backup, entry.BeforeDigest, os.FileMode(entry.Mode))
		if err != nil || prepared.beforeInfo == nil || !os.SameFile(prepared.beforeInfo, backupInfo) {
			_ = parent.Unlink(filepath.Base(entry.Backup))
			if err == nil {
				err = errors.New("backup does not match the prepared file identity")
			}
			return fmt.Errorf("validate backup for %s: %w", description, err)
		}
		if !setupIdentityMatchesInfo(
			prepared.entry.BackupIdentity,
			backupInfo,
		) {
			return fmt.Errorf(
				"validate backup for %s: prepared identity changed",
				description,
			)
		}
	}
	if beforeSetupFilePublication != nil {
		if err := beforeSetupFilePublication(entry.Path); err != nil {
			return err
		}
	}
	if err := revalidateSetupPreparedParent(parent, *prepared); err != nil {
		return err
	}
	if entry.Existed {
		if err := parent.Link(filepath.Base(entry.Staged), filepath.Base(entry.Replacement)); err != nil {
			return fmt.Errorf("prepare %s publication marker: %w", description, err)
		}
		markerInfo, err := validateSetupArtifact(parent, entry.Replacement, entry.AfterDigest, os.FileMode(entry.AfterMode))
		if err != nil || !os.SameFile(stagedInfo, markerInfo) {
			_ = parent.Unlink(filepath.Base(entry.Replacement))
			if err == nil {
				err = errors.New("publication marker does not match the staged file")
			}
			return fmt.Errorf("validate %s publication marker: %w", description, err)
		}
		if err := parent.Exchange(filepath.Base(entry.Replacement), filepath.Base(entry.Path)); err != nil {
			return fmt.Errorf("publish %s: %w", description, err)
		}
		originalInfo, err := parent.InspectRegular(filepath.Base(entry.Replacement))
		if err != nil {
			return fmt.Errorf("inspect displaced %s: %w", description, err)
		}
		if originalInfo == nil || prepared.beforeInfo == nil || !os.SameFile(originalInfo, prepared.beforeInfo) {
			if exchangeErr := parent.Exchange(filepath.Base(entry.Replacement), filepath.Base(entry.Path)); exchangeErr != nil {
				return errors.Join(fmt.Errorf("%s changed during publication", description), exchangeErr)
			}
			return fmt.Errorf("%s changed during publication", description)
		}
		if _, err := validateSetupArtifact(parent, entry.Replacement, entry.BeforeDigest, os.FileMode(entry.Mode)); err != nil {
			if exchangeErr := parent.Exchange(filepath.Base(entry.Replacement), filepath.Base(entry.Path)); exchangeErr != nil {
				return errors.Join(fmt.Errorf("%s changed during publication", description), err, exchangeErr)
			}
			return fmt.Errorf("%s changed during publication: %w", description, err)
		}
		if !setupIdentityMatchesInfo(
			prepared.entry.BackupIdentity,
			originalInfo,
		) {
			return fmt.Errorf(
				"displaced %s identity changed",
				description,
			)
		}
	} else {
		if err := parent.Link(filepath.Base(entry.Staged), filepath.Base(entry.Path)); err != nil {
			return fmt.Errorf("publish new %s: %w", description, err)
		}
	}
	publishedInfo, err := parent.InspectRegular(filepath.Base(entry.Path))
	if err != nil {
		return fmt.Errorf("inspect published %s: %w", description, err)
	}
	if publishedInfo == nil || !os.SameFile(stagedInfo, publishedInfo) {
		return fmt.Errorf("published %s does not match the staged file", description)
	}
	prepared.entry.FinalIdentity, err =
		setupIdentityFromInfo(publishedInfo)
	if err != nil {
		return fmt.Errorf(
			"capture published %s identity: %w",
			description,
			err,
		)
	}
	transaction.journal.Entries[index] = prepared.entry
	if err := transaction.writeJournal(); err != nil {
		return fmt.Errorf(
			"persist applied %s identity: %w",
			description,
			err,
		)
	}
	if err := parent.Sync(); err != nil {
		return fmt.Errorf("sync %s directory: %w", description, err)
	}
	if afterSetupFilePublication != nil {
		if err := afterSetupFilePublication(entry.Path); err != nil {
			return err
		}
	}
	ownedInfo, err := validateSetupTargetOwnershipInfoInParent(
		parent,
		transaction.journal.SchemaVersion,
		entry,
	)
	if err != nil {
		return fmt.Errorf("published %s changed after publication: %w", description, err)
	}
	if !os.SameFile(publishedInfo, ownedInfo) {
		return fmt.Errorf("published %s changed after publication", description)
	}
	prepared.afterInfo = ownedInfo
	prepared.entry.FinalIdentity, err = setupIdentityFromInfo(ownedInfo)
	if err != nil {
		return fmt.Errorf("capture published %s identity: %w", description, err)
	}
	return revalidateSetupPreparedParent(parent, *prepared)
}

func runAfterSetupFileInitialRevalidation(path string) error {
	if afterSetupFileInitialRevalidation == nil {
		return nil
	}
	return afterSetupFileInitialRevalidation(path)
}

func validateSetupArtifact(parent *fssecure.Directory, path, digest string, mode os.FileMode) (os.FileInfo, error) {
	data, info, err := parent.ReadRegular(filepath.Base(path))
	if err != nil {
		return nil, err
	}
	if info.Mode() != mode || hexDigest(data) != digest {
		return nil, errors.New("artifact does not match the prepared state")
	}
	return info, nil
}

func validateSetupArtifactIdentity(
	parent *fssecure.Directory,
	path, digest string,
	mode os.FileMode,
	identity *setupFileIdentity,
) (os.FileInfo, error) {
	info, err := validateSetupArtifact(parent, path, digest, mode)
	if err != nil {
		return nil, err
	}
	if identity != nil && !setupIdentityMatchesInfo(identity, info) {
		return nil, errors.New("artifact does not match the recorded identity")
	}
	return info, nil
}

func validateSetupOriginalArtifact(parent *fssecure.Directory, schemaVersion int, path string, entry setupJournalEntry) (os.FileInfo, error) {
	identity := entry.BackupIdentity
	if schemaVersion != setupJournalSchema {
		identity = nil
	}
	info, err := validateSetupArtifactIdentity(
		parent,
		path,
		entry.BeforeDigest,
		os.FileMode(entry.Mode),
		identity,
	)
	if err != nil {
		return nil, err
	}
	if schemaVersion == legacySetupJournalSchema {
		return info, nil
	}
	backupInfo, err := validateSetupArtifactIdentity(
		parent,
		entry.Backup,
		entry.BeforeDigest,
		os.FileMode(entry.Mode),
		identity,
	)
	if err != nil || backupInfo == nil || !os.SameFile(info, backupInfo) {
		return nil, errors.New("original artifact has no ownership marker")
	}
	return info, nil
}

func setupPreparedFileIsNoop(prepared *setupPreparedFile) bool {
	return prepared.entry.Existed &&
		prepared.entry.BeforeDigest == prepared.entry.AfterDigest &&
		prepared.entry.Mode == prepared.entry.AfterMode
}

func setupJournalEntryIsNoop(entry setupJournalEntry) bool {
	return entry.Existed &&
		entry.BeforeDigest == entry.AfterDigest &&
		entry.Mode == entry.AfterMode
}

func setupIdentityFromInfo(info os.FileInfo) (*setupFileIdentity, error) {
	if info == nil {
		return nil, errors.New("file identity is missing")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, errors.New("file identity metadata is unavailable")
	}
	return &setupFileIdentity{
		Device: uint64(stat.Dev),
		Inode:  uint64(stat.Ino),
	}, nil
}

func setupIdentityMatchesInfo(identity *setupFileIdentity, info os.FileInfo) bool {
	if identity == nil || info == nil {
		return false
	}
	current, err := setupIdentityFromInfo(info)
	return err == nil && *current == *identity
}

func validateSetupTargetOwnershipInParent(parent *fssecure.Directory, schemaVersion int, entry setupJournalEntry) error {
	_, err := validateSetupTargetOwnershipInfoInParent(parent, schemaVersion, entry)
	return err
}

func validateSetupTargetOwnershipInfoInParent(parent *fssecure.Directory, schemaVersion int, entry setupJournalEntry) (os.FileInfo, error) {
	data, targetInfo, err := parent.ReadRegular(filepath.Base(entry.Path))
	if err != nil {
		return nil, err
	}
	if targetInfo.Mode() != os.FileMode(entry.AfterMode) || hexDigest(data) != entry.AfterDigest {
		return nil, errors.New("target does not match the prepared state")
	}
	if setupJournalEntryIsNoop(entry) {
		return targetInfo, nil
	}
	marker := entry.Staged
	if schemaVersion == legacySetupJournalSchema && entry.Existed {
		marker = entry.Replacement
	}
	markerInfo, err := validateSetupArtifact(
		parent,
		marker,
		entry.AfterDigest,
		os.FileMode(entry.AfterMode),
	)
	if err != nil || markerInfo == nil || !os.SameFile(targetInfo, markerInfo) {
		return nil, errors.New("target has no setup ownership marker")
	}
	return targetInfo, nil
}

func validateCommittedSetupTargetInfoInParent(
	parent *fssecure.Directory,
	schemaVersion int,
	entry setupJournalEntry,
) (os.FileInfo, error) {
	if schemaVersion != setupJournalSchema {
		return validateSetupTargetOwnershipInfoInParent(parent, schemaVersion, entry)
	}
	data, targetInfo, err := parent.ReadRegular(filepath.Base(entry.Path))
	if err != nil {
		return nil, err
	}
	if targetInfo.Mode() != os.FileMode(entry.AfterMode) ||
		hexDigest(data) != entry.AfterDigest ||
		!setupIdentityMatchesInfo(entry.FinalIdentity, targetInfo) {
		return nil, errors.New("target does not match the committed identity")
	}
	return targetInfo, nil
}

func validatePreparedCommittedTargetInParent(parent *fssecure.Directory, prepared setupPreparedFile) error {
	if prepared.afterInfo == nil {
		return errors.New("prepared setup target identity is missing")
	}
	data, currentInfo, err := parent.ReadRegular(filepath.Base(prepared.entry.Path))
	if err != nil {
		return err
	}
	if !os.SameFile(prepared.afterInfo, currentInfo) ||
		currentInfo.Mode() != os.FileMode(prepared.entry.AfterMode) ||
		hexDigest(data) != prepared.entry.AfterDigest {
		return errors.New("target does not match the committed prepared identity")
	}
	return nil
}

func validateSetupTargetsForCommit(journal setupJournal) ([]os.FileInfo, error) {
	targets := make([]os.FileInfo, len(journal.Entries))
	for index, entry := range journal.Entries {
		parent, err := openSetupParent(entry.Path, false)
		if err != nil {
			return nil, fmt.Errorf("open setup target %s: %w", entry.Path, err)
		}
		targetInfo, validateErr := validateSetupTargetOwnershipInfoInParent(
			parent,
			journal.SchemaVersion,
			entry,
		)
		revalidateErr := parent.Revalidate(filepath.Dir(entry.Path))
		closeErr := parent.Close()
		if validateErr != nil {
			return nil, fmt.Errorf("setup target %s is not in the prepared final state: %w", entry.Path, validateErr)
		}
		if revalidateErr != nil {
			return nil, fmt.Errorf("setup target parent %s changed during validation: %w", filepath.Dir(entry.Path), revalidateErr)
		}
		if closeErr != nil {
			return nil, closeErr
		}
		targets[index] = targetInfo
	}
	return targets, nil
}

func validateCommittedSetupTargets(journal setupJournal) error {
	for _, entry := range journal.Entries {
		parent, err := openSetupParent(entry.Path, false)
		if err != nil {
			return fmt.Errorf("open committed setup target %s: %w", entry.Path, err)
		}
		_, validateErr := validateCommittedSetupTargetInfoInParent(
			parent,
			journal.SchemaVersion,
			entry,
		)
		revalidateErr := parent.Revalidate(filepath.Dir(entry.Path))
		closeErr := parent.Close()
		if validateErr != nil {
			return fmt.Errorf("setup target %s is not in the committed final state: %w", entry.Path, validateErr)
		}
		if revalidateErr != nil {
			return fmt.Errorf("setup target parent %s changed during validation: %w", filepath.Dir(entry.Path), revalidateErr)
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func (transaction *setupTransaction) prepareCommittedJournal() error {
	targets, err := validateSetupTargetsForCommit(transaction.journal)
	if err != nil {
		return err
	}
	for index, targetInfo := range targets {
		identity, err := setupIdentityFromInfo(targetInfo)
		if err != nil {
			return fmt.Errorf("capture committed setup target identity: %w", err)
		}
		entry := transaction.files[index].entry
		entry.FinalIdentity = identity
		transaction.files[index].entry.FinalIdentity = identity
		transaction.journal.Entries[index] = entry
	}
	transaction.journal.Committed = true
	return nil
}

func (transaction *setupTransaction) commit() error {
	if !transaction.persisted {
		return errors.New("setup transaction journal is not persisted")
	}
	if _, err := validateSetupTargetsForCommit(transaction.journal); err != nil {
		return err
	}
	if beforeCommitSetupTransaction != nil {
		if err := beforeCommitSetupTransaction(); err != nil {
			return err
		}
	}
	if err := transaction.prepareCommittedJournal(); err != nil {
		return err
	}
	if err := transaction.writeJournal(); err != nil {
		reloaded, loadErr := transaction.loadJournal()
		if loadErr != nil || !reloaded.Committed {
			transaction.journal.Committed = false
			return fmt.Errorf("commit setup transaction: %w", err)
		}
		transaction.journal = reloaded
	}
	if err := transaction.finalizeJournal(transaction.journal); err != nil {
		return fmt.Errorf("finalize committed setup transaction: %w", err)
	}
	transaction.persisted = false
	return nil
}

func (transaction *setupTransaction) reconcileCommitError(primary error) error {
	if !transaction.persisted {
		return primary
	}
	journal, err := transaction.loadJournal()
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := transaction.reconcileMissingJournal(); err != nil {
			return errors.Join(primary, fmt.Errorf("reconcile missing setup transaction journal: %w", err))
		}
		return nil
	case err != nil:
		return errors.Join(primary, fmt.Errorf("reconcile setup transaction commit: %w", err))
	case journal.Committed:
		transaction.journal = journal
		if err := transaction.finalizeJournal(journal); err != nil {
			return errors.Join(primary, fmt.Errorf("finalize committed setup transaction: %w", err))
		}
		transaction.persisted = false
		return nil
	default:
		transaction.journal = journal
		return transaction.abort(primary)
	}
}

func (transaction *setupTransaction) reconcileMissingJournal() error {
	if !transaction.persisted {
		return errors.New("setup transaction is not persisted")
	}
	if len(transaction.files) != len(transaction.journal.Entries) {
		return errors.New("prepared setup transaction state is incomplete")
	}
	validate := func() error {
		for _, prepared := range transaction.files {
			parent, err := openSetupParent(prepared.entry.Path, false)
			if err != nil {
				return fmt.Errorf("open setup target during missing-journal reconciliation: %w", err)
			}
			if err := func() error {
				defer parent.Close()
				if err := revalidateSetupPreparedParent(parent, prepared); err != nil {
					return err
				}
				entry := prepared.entry
				if err := validatePreparedCommittedTargetInParent(parent, prepared); err != nil {
					return fmt.Errorf("setup target %s is not in the committed final state: %w", entry.Path, err)
				}
				for _, artifact := range setupEntryArtifacts(entry) {
					info, err := parent.InspectRegular(filepath.Base(artifact))
					if err != nil {
						return fmt.Errorf("inspect setup transaction artifact %s: %w", artifact, err)
					}
					if info != nil {
						return fmt.Errorf("setup transaction artifact %s remains", artifact)
					}
				}
				if err := parent.Sync(); err != nil {
					return fmt.Errorf("sync committed setup target directory %s: %w", filepath.Dir(entry.Path), err)
				}
				if err := revalidateSetupPreparedParent(parent, prepared); err != nil {
					return err
				}
				if err := validatePreparedCommittedTargetInParent(parent, prepared); err != nil {
					return fmt.Errorf("setup target %s changed during missing-journal reconciliation: %w", entry.Path, err)
				}
				return nil
			}(); err != nil {
				return err
			}
		}
		if err := transaction.syncMissingJournalDirectory(); err != nil {
			return err
		}
		if _, err := transaction.loadJournal(); !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				return errors.New("setup transaction journal reappeared during reconciliation")
			}
			return fmt.Errorf("recheck missing setup transaction journal: %w", err)
		}
		return nil
	}
	if transaction.journal.InstallLock != "" {
		if err := withInstallLockPath(transaction.journal.InstallLock, validate); err != nil {
			return err
		}
	} else if err := validate(); err != nil {
		return err
	}
	transaction.persisted = false
	return nil
}

func (transaction *setupTransaction) syncMissingJournalDirectory() error {
	if transaction.journalDir != nil {
		if err := transaction.revalidateJournalDirectory(); err != nil {
			return err
		}
		if err := transaction.journalDir.Sync(); err != nil {
			return fmt.Errorf("sync missing setup transaction journal directory: %w", err)
		}
		return nil
	}
	parent, err := openSetupParent(transaction.journalPath, false)
	if err != nil {
		return fmt.Errorf("open missing setup transaction journal directory: %w", err)
	}
	defer parent.Close()
	if err := parent.Sync(); err != nil {
		return fmt.Errorf("sync missing setup transaction journal directory: %w", err)
	}
	if err := parent.Revalidate(filepath.Dir(transaction.journalPath)); err != nil {
		return fmt.Errorf("setup transaction journal directory changed during reconciliation: %w", err)
	}
	return nil
}

func (transaction *setupTransaction) rollbackFiles() error {
	if !transaction.persisted {
		return nil
	}
	return rollbackSetupJournalFiles(transaction.journal)
}

func (transaction *setupTransaction) finishAbort() error {
	if !transaction.persisted {
		return nil
	}
	if err := validateRolledBackSetupTargets(transaction.journal); err != nil {
		return err
	}
	if err := transaction.removeJournal(transaction.journal); err != nil {
		return err
	}
	transaction.persisted = false
	return nil
}

func (transaction *setupTransaction) abort(primary error) error {
	if !transaction.persisted {
		return primary
	}
	rollbackErr := transaction.rollbackFiles()
	if rollbackErr == nil {
		rollbackErr = transaction.finishAbort()
	}
	if rollbackErr != nil {
		return errors.Join(primary, fmt.Errorf("roll back setup transaction: %w", rollbackErr))
	}
	return primary
}

func setupTransactionPending(stateRoot string) (bool, error) {
	journalPath := setupJournalPath(stateRoot)
	_, err := loadSetupJournal(journalPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func setupTransactionPendingLocked(lock *setupAdmissionLock) (bool, error) {
	_, err := lock.loadJournal()
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func LoadRuntime(paths config.Paths) (runtimeConfig config.Runtime, resultErr error) {
	setupLock, err := acquireSetupAdmissionLock(paths.StateRoot, false)
	if err != nil {
		return config.Runtime{}, err
	}
	if setupLock != nil {
		defer func() {
			if releaseErr := setupLock.release(); releaseErr != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("release setup admission lock: %w", releaseErr))
			}
		}()
	}
	if setupLock != nil {
		journal, err := setupLock.loadJournal()
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			return config.Runtime{}, fmt.Errorf("inspect interrupted setup transaction: %w", err)
		case journal.Committed:
			if err := finalizeSetupJournalLocked(setupLock, journal); err != nil {
				return config.Runtime{}, fmt.Errorf("finalize committed setup transaction: %w", err)
			}
		default:
			return config.Runtime{}, errors.New("an interrupted setup transaction requires recovery; rerun `sclaude setup`")
		}
	}
	return config.Load(paths.ConfigFile)
}

func recoverSetupTransaction(stateRoot string) error {
	journalPath := setupJournalPath(stateRoot)
	journal, err := loadSetupJournal(journalPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if journal.Committed {
		return finalizeSetupJournal(journalPath, journal)
	}
	return rollbackRecoveredSetupJournal(context.Background(), journalPath, journal, ExecRunner{})
}

func recoverSetupTransactionLocked(lock *setupAdmissionLock) error {
	journal, err := lock.loadJournal()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if journal.Committed {
		return finalizeSetupJournalLocked(lock, journal)
	}
	return rollbackRecoveredSetupJournalLocked(context.Background(), lock, journal, ExecRunner{})
}

func writeSetupJournal(path string, journal setupJournal) error {
	return writeSetupJournalVerified(path, journal, nil, false)
}

func writeSetupJournalVerified(
	path string,
	journal setupJournal,
	beforePublish func() error,
	writeNew bool,
) error {
	parent, err := openSetupParent(path, true)
	if err != nil {
		return err
	}
	defer parent.Close()
	return writeSetupJournalToDirectoryVerified(
		parent,
		filepath.Base(path),
		journal,
		beforePublish,
		writeNew,
	)
}

func writeSetupJournalToDirectory(parent *fssecure.Directory, name string, journal setupJournal) error {
	return writeSetupJournalToDirectoryVerified(parent, name, journal, nil, false)
}

func writeSetupJournalToDirectoryVerified(
	parent *fssecure.Directory,
	name string,
	journal setupJournal,
	beforePublish func() error,
	writeNew bool,
) error {
	if err := validateSetupJournalForPersistence(journal); err != nil {
		return err
	}
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	afterPublish := func(publishedInfo os.FileInfo) error {
		current, currentInfo, err := loadSetupJournalFromDirectoryWithInfo(
			parent,
			name,
		)
		if err != nil || currentInfo == nil || !os.SameFile(publishedInfo, currentInfo) || !reflect.DeepEqual(current, journal) {
			return errors.New("setup transaction journal changed during publication")
		}
		return nil
	}
	if writeNew {
		return parent.WriteNewVerified(
			name,
			append(data, '\n'),
			0o600,
			func(os.FileInfo) error {
				if beforePublish == nil {
					return nil
				}
				return beforePublish()
			},
			afterPublish,
		)
	}
	return parent.WriteAtomicVerified(
		name,
		append(data, '\n'),
		0o600,
		beforePublish,
		afterPublish,
	)
}

func loadSetupJournal(path string) (setupJournal, error) {
	parent, err := openSetupParent(path, false)
	if err != nil {
		return setupJournal{}, err
	}
	defer parent.Close()
	return loadSetupJournalFromDirectory(parent, filepath.Base(path))
}

func loadSetupJournalFromDirectory(parent *fssecure.Directory, name string) (setupJournal, error) {
	journal, _, err := loadSetupJournalFromDirectoryWithInfo(parent, name)
	return journal, err
}

func loadSetupJournalFromDirectoryWithInfo(parent *fssecure.Directory, name string) (setupJournal, os.FileInfo, error) {
	data, info, err := parent.ReadRegular(name)
	if err != nil {
		return setupJournal{}, nil, err
	}
	if info.Mode() != 0o600 {
		return setupJournal{}, nil, errors.New("setup transaction journal must have mode 0600")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var journal setupJournal
	if err := decoder.Decode(&journal); err != nil {
		return setupJournal{}, nil, fmt.Errorf("parse setup transaction journal: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return setupJournal{}, nil, errors.New("parse setup transaction journal: trailing data")
	}
	if err := validateSetupJournal(journal); err != nil {
		return setupJournal{}, nil, err
	}
	return journal, info, nil
}

func validateSetupJournal(journal setupJournal) error {
	return validateSetupJournalState(journal, true)
}

func validateSetupJournalForPersistence(journal setupJournal) error {
	return validateSetupJournalState(journal, journal.Committed)
}

func validateSetupJournalState(journal setupJournal, requireCommittedIdentity bool) error {
	if journal.SchemaVersion != legacySetupJournalSchema &&
		journal.SchemaVersion != markerSetupJournalSchema &&
		journal.SchemaVersion != setupJournalSchema {
		return fmt.Errorf("unsupported setup transaction journal schema %d", journal.SchemaVersion)
	}
	if !setupTokenPattern.MatchString(journal.Token) {
		return errors.New("setup transaction journal token is invalid")
	}
	if journal.InstallLock != "" && (!filepath.IsAbs(journal.InstallLock) || filepath.Clean(journal.InstallLock) != journal.InstallLock || journal.InstallLock == string(filepath.Separator)) {
		return errors.New("setup transaction journal install lock path is invalid")
	}
	if journal.ServiceManager == "" {
		if journal.ServiceExecutable != "" || journal.ServiceExecutableDigest != "" || journal.ServiceActive || journal.ServiceScheduled || journal.ServiceActiveTouched || journal.ServiceScheduledTouched || journal.OAuthAttempted || journal.OAuthCompleted {
			return errors.New("setup transaction journal has service state without a manager")
		}
	} else {
		switch journal.ServiceManager {
		case "brew", "systemd":
			if !filepath.IsAbs(journal.ServiceExecutable) || filepath.Clean(journal.ServiceExecutable) != journal.ServiceExecutable || journal.ServiceExecutable == string(filepath.Separator) || !setupDigestPattern.MatchString(journal.ServiceExecutableDigest) {
				return errors.New("setup transaction journal service executable identity is invalid")
			}
		case "docker", "none":
			if journal.ServiceExecutable != "" || journal.ServiceExecutableDigest != "" || journal.ServiceActive || journal.ServiceScheduled || journal.ServiceActiveTouched || journal.ServiceScheduledTouched {
				return errors.New("setup transaction journal external service state is invalid")
			}
		default:
			return errors.New("setup transaction journal service manager is invalid")
		}
	}
	if journal.OAuthCompleted && !journal.OAuthAttempted {
		return errors.New("setup transaction journal completed OAuth without an attempt")
	}
	if len(journal.Entries) == 0 {
		return errors.New("setup transaction journal has no entries")
	}
	seen := map[string]bool{}
	if journal.InstallLock != "" {
		seen[journal.InstallLock] = true
	}
	for _, entry := range journal.Entries {
		if err := validateSetupJournalEntry(
			entry,
			journal.Token,
			journal.SchemaVersion,
			journal.Committed && requireCommittedIdentity,
		); err != nil {
			return err
		}
		for _, path := range append([]string{entry.Path}, setupEntryArtifacts(entry)...) {
			if path == "" {
				continue
			}
			if seen[path] {
				return fmt.Errorf("setup transaction journal repeats path %s", path)
			}
			seen[path] = true
		}
	}
	return nil
}

func validateSetupJournalEntry(
	entry setupJournalEntry,
	token string,
	schemaVersion int,
	requireCommittedIdentity bool,
) error {
	if !filepath.IsAbs(entry.Path) ||
		filepath.Clean(entry.Path) != entry.Path ||
		entry.Path == string(filepath.Separator) {
		return errors.New("setup transaction journal target path is invalid")
	}
	if entry.Backup != setupBackupPath(entry.Path, token) ||
		entry.Staged != setupStagedPath(entry.Path, token) ||
		entry.Replacement != setupReplacementPath(entry.Path, token) {
		return errors.New("setup transaction journal artifact path is invalid")
	}
	if !isExactPermissionMode(os.FileMode(entry.AfterMode)) {
		return errors.New("setup transaction journal after mode is invalid")
	}
	if !setupDigestPattern.MatchString(entry.AfterDigest) {
		return errors.New("setup transaction journal after digest is invalid")
	}
	if entry.Existed {
		if !isExactPermissionMode(os.FileMode(entry.Mode)) || !setupDigestPattern.MatchString(entry.BeforeDigest) {
			return errors.New("setup transaction journal prior state is invalid")
		}
	} else if entry.Mode != 0 || entry.BeforeDigest != "" {
		return errors.New("setup transaction journal absent target has prior state")
	}
	identities := []*setupFileIdentity{
		entry.FinalIdentity,
		entry.BackupIdentity,
		entry.StagedIdentity,
	}
	if schemaVersion != setupJournalSchema {
		for _, identity := range identities {
			if identity != nil {
				return errors.New("legacy setup transaction journal has file identity state")
			}
		}
		return nil
	}
	for _, identity := range identities {
		if identity != nil && identity.Inode == 0 {
			return errors.New("setup transaction journal file identity is invalid")
		}
	}
	if entry.Existed && entry.BackupIdentity == nil {
		return errors.New(
			"setup transaction journal prior target identity is missing",
		)
	}
	if !entry.Existed && entry.BackupIdentity != nil {
		return errors.New(
			"setup transaction journal absent target has prior identity",
		)
	}
	if entry.FinalIdentity != nil &&
		entry.StagedIdentity == nil &&
		!setupJournalEntryIsNoop(entry) {
		return errors.New(
			"setup transaction journal target has no staged identity",
		)
	}
	if setupJournalEntryIsNoop(entry) {
		if entry.FinalIdentity == nil ||
			!reflect.DeepEqual(
				entry.FinalIdentity,
				entry.BackupIdentity,
			) {
			return errors.New(
				"setup transaction journal no-op identity is invalid",
			)
		}
	}
	if requireCommittedIdentity && entry.FinalIdentity == nil {
		return errors.New("committed setup transaction journal target identity is missing")
	}
	return nil
}

func revalidateSetupPreparedFile(prepared setupPreparedFile) error {
	parent, err := openSetupParent(prepared.entry.Path, false)
	if err != nil {
		return fmt.Errorf("revalidate setup target %s: %w", prepared.entry.Path, err)
	}
	defer parent.Close()
	if err := revalidateSetupPreparedParent(parent, prepared); err != nil {
		return err
	}
	return revalidateSetupPreparedFileInParent(parent, &prepared)
}

func revalidateSetupPreparedParent(parent *fssecure.Directory, prepared setupPreparedFile) error {
	if prepared.parentExisted && (prepared.parentInfo == nil || !os.SameFile(prepared.parentInfo, parent.Info())) {
		return fmt.Errorf("setup target parent %s changed after preparation", filepath.Dir(prepared.entry.Path))
	}
	if err := parent.Revalidate(filepath.Dir(prepared.entry.Path)); err != nil {
		return fmt.Errorf("setup target parent %s changed after preparation: %w", filepath.Dir(prepared.entry.Path), err)
	}
	return nil
}

func revalidateSetupPreparedFileInParent(parent *fssecure.Directory, prepared *setupPreparedFile) error {
	entry := prepared.entry
	data, info, err := parent.ReadOptionalRegular(filepath.Base(entry.Path))
	if err != nil {
		return fmt.Errorf("revalidate setup target %s: %w", entry.Path, err)
	}
	if entry.FinalIdentity != nil {
		if info == nil ||
			!setupIdentityMatchesInfo(entry.FinalIdentity, info) ||
			info.Mode() != os.FileMode(entry.AfterMode) ||
			hexDigest(data) != entry.AfterDigest {
			return fmt.Errorf(
				"setup target %s changed after application",
				entry.Path,
			)
		}
		prepared.afterInfo = info
		return nil
	}
	if !entry.Existed {
		if info == nil {
			return nil
		}
		return fmt.Errorf("setup target %s changed after preparation", entry.Path)
	}
	if info == nil || prepared.beforeInfo == nil || !os.SameFile(prepared.beforeInfo, info) || info.Mode() != prepared.beforeInfo.Mode() || hexDigest(data) != entry.BeforeDigest {
		return fmt.Errorf("setup target %s changed after preparation", entry.Path)
	}
	return nil
}

func rollbackSetupJournalFiles(journal setupJournal) error {
	rollback := func() error {
		var rollbackErr error
		for index := len(journal.Entries) - 1; index >= 0; index-- {
			if err := rollbackSetupEntry(journal.SchemaVersion, journal.Entries[index]); err != nil {
				rollbackErr = errors.Join(rollbackErr, err)
			}
		}
		return rollbackErr
	}
	if journal.InstallLock != "" {
		return withInstallLockPath(journal.InstallLock, rollback)
	}
	return rollback()
}

func rollbackSetupJournal(journalPath string, journal setupJournal) error {
	if err := rollbackSetupJournalFiles(journal); err != nil {
		return err
	}
	if err := validateRolledBackSetupTargets(journal); err != nil {
		return err
	}
	return removeSetupJournal(journalPath, journal)
}

func rollbackSetupJournalLocked(lock *setupAdmissionLock, journal setupJournal) error {
	if err := rollbackSetupJournalFiles(journal); err != nil {
		return err
	}
	if err := validateRolledBackSetupTargets(journal); err != nil {
		return err
	}
	return lock.removeJournal(journal)
}

func rollbackSetupEntry(schemaVersion int, entry setupJournalEntry) error {
	parent, err := openSetupParent(entry.Path, false)
	if errors.Is(err, os.ErrNotExist) && !entry.Existed {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open setup target parent during rollback: %w", err)
	}
	defer parent.Close()
	current, currentInfo, err := parent.ReadOptionalRegular(filepath.Base(entry.Path))
	if err != nil {
		return fmt.Errorf("inspect setup target %s during rollback: %w", entry.Path, err)
	}
	currentExists := currentInfo != nil
	beforeMatches := entry.Existed && currentExists && hexDigest(current) == entry.BeforeDigest && currentInfo.Mode() == os.FileMode(entry.Mode)
	afterMatches := currentExists && hexDigest(current) == entry.AfterDigest && currentInfo.Mode() == os.FileMode(entry.AfterMode)

	switch {
	case beforeMatches:
	case afterMatches && entry.Existed:
		afterMarker := entry.Staged
		originalArtifact := entry.Replacement
		if schemaVersion == legacySetupJournalSchema {
			afterMarker = entry.Replacement
			originalArtifact = entry.Staged
		}
		_, publishedInfo, publishedErr := parent.ReadOptionalRegular(filepath.Base(afterMarker))
		if publishedErr != nil || publishedInfo == nil || !os.SameFile(currentInfo, publishedInfo) {
			return fmt.Errorf("setup-replaced target %s has no ownership marker", entry.Path)
		}
		beforeInfo, err := validateSetupOriginalArtifact(parent, schemaVersion, originalArtifact, entry)
		if err != nil {
			return fmt.Errorf("validate setup original for %s: %w", entry.Path, err)
		}
		if beforeSetupFileRollback != nil {
			if err := beforeSetupFileRollback(entry.Path); err != nil {
				return err
			}
		}
		if err := parent.Exchange(filepath.Base(originalArtifact), filepath.Base(entry.Path)); err != nil {
			return fmt.Errorf("restore setup target %s: %w", entry.Path, err)
		}
		displacedInfo, err := parent.InspectRegular(filepath.Base(originalArtifact))
		if err != nil || displacedInfo == nil || !os.SameFile(displacedInfo, publishedInfo) {
			if exchangeErr := parent.Exchange(filepath.Base(originalArtifact), filepath.Base(entry.Path)); exchangeErr != nil {
				return errors.Join(fmt.Errorf("setup target %s changed during rollback", entry.Path), exchangeErr)
			}
			return fmt.Errorf("setup target %s changed during rollback", entry.Path)
		}
		if _, err := validateSetupArtifact(parent, originalArtifact, entry.AfterDigest, os.FileMode(entry.AfterMode)); err != nil {
			if exchangeErr := parent.Exchange(filepath.Base(originalArtifact), filepath.Base(entry.Path)); exchangeErr != nil {
				return errors.Join(fmt.Errorf("setup target %s changed during rollback", entry.Path), err, exchangeErr)
			}
			return fmt.Errorf("setup target %s changed during rollback: %w", entry.Path, err)
		}
		restoredInfo, err := parent.InspectRegular(filepath.Base(entry.Path))
		if err != nil || restoredInfo == nil || !os.SameFile(restoredInfo, beforeInfo) {
			return fmt.Errorf("restored setup target %s does not match the original", entry.Path)
		}
	case afterMatches && !entry.Existed:
		_, stagedInfo, stagedErr := parent.ReadOptionalRegular(filepath.Base(entry.Staged))
		if stagedErr != nil || stagedInfo == nil || !os.SameFile(currentInfo, stagedInfo) {
			return fmt.Errorf("setup-created target %s has no ownership marker", entry.Path)
		}
		if beforeSetupFileRollback != nil {
			if err := beforeSetupFileRollback(entry.Path); err != nil {
				return err
			}
		}
		quarantine := filepath.Base(entry.Replacement)
		if err := parent.RenameNoReplace(filepath.Base(entry.Path), quarantine); err != nil {
			return fmt.Errorf("remove setup-created target %s: %w", entry.Path, err)
		}
		movedInfo, err := parent.InspectRegular(quarantine)
		if err != nil || movedInfo == nil || !os.SameFile(movedInfo, stagedInfo) {
			restoreErr := parent.RenameNoReplace(quarantine, filepath.Base(entry.Path))
			if restoreErr != nil {
				return errors.Join(fmt.Errorf("setup target %s changed during rollback", entry.Path), restoreErr)
			}
			return fmt.Errorf("setup target %s changed during rollback", entry.Path)
		}
		if _, err := validateSetupArtifact(parent, entry.Replacement, entry.AfterDigest, os.FileMode(entry.AfterMode)); err != nil {
			restoreErr := parent.RenameNoReplace(quarantine, filepath.Base(entry.Path))
			if restoreErr != nil {
				return errors.Join(fmt.Errorf("setup target %s changed during rollback", entry.Path), err, restoreErr)
			}
			return fmt.Errorf("setup target %s changed during rollback: %w", entry.Path, err)
		}
		if beforeSetupCreatedTargetRemoval != nil {
			if err := beforeSetupCreatedTargetRemoval(entry.Path); err != nil {
				return err
			}
		}
		visibleInfo, err := parent.InspectRegular(filepath.Base(entry.Path))
		if err != nil {
			return fmt.Errorf("inspect setup-created target %s before removal: %w", entry.Path, err)
		}
		if visibleInfo != nil {
			return fmt.Errorf("setup-created target %s was replaced before removal", entry.Path)
		}
	case !currentExists && entry.Existed:
		originalArtifact := entry.Replacement
		if schemaVersion == legacySetupJournalSchema {
			originalArtifact = entry.Staged
		}
		beforeInfo, err := validateSetupOriginalArtifact(parent, schemaVersion, originalArtifact, entry)
		if err != nil {
			return fmt.Errorf("setup target %s is missing and cannot be restored", entry.Path)
		}
		if beforeSetupFileRollback != nil {
			if err := beforeSetupFileRollback(entry.Path); err != nil {
				return err
			}
		}
		if err := parent.Link(filepath.Base(originalArtifact), filepath.Base(entry.Path)); err != nil {
			return fmt.Errorf("restore missing setup target %s: %w", entry.Path, err)
		}
		restoredInfo, err := parent.InspectRegular(filepath.Base(entry.Path))
		if err != nil || restoredInfo == nil || !os.SameFile(restoredInfo, beforeInfo) {
			return fmt.Errorf("restored setup target %s does not match the original", entry.Path)
		}
	case !currentExists && !entry.Existed:
	default:
		return fmt.Errorf("setup target %s changed outside the interrupted transaction", entry.Path)
	}
	if err := validateSetupRollbackArtifactsInParent(
		parent,
		schemaVersion,
		entry,
	); err != nil {
		return err
	}
	if err := removeSetupArtifactsInParent(
		parent,
		schemaVersion,
		entry,
		true,
	); err != nil {
		return err
	}
	if err := parent.Sync(); err != nil {
		return err
	}
	if err := parent.Revalidate(filepath.Dir(entry.Path)); err != nil {
		return fmt.Errorf("setup target parent %s changed during rollback: %w", filepath.Dir(entry.Path), err)
	}
	if err := validateRolledBackSetupTargetInParent(parent, entry); err != nil {
		return err
	}
	return nil
}

func finalizeSetupJournal(journalPath string, journal setupJournal) error {
	return finalizeSetupJournalWithRemoval(journal, func(expected setupJournal, beforeRemove func() error) error {
		return removeSetupJournalVerified(journalPath, expected, beforeRemove)
	})
}

func finalizeSetupJournalLocked(lock *setupAdmissionLock, journal setupJournal) error {
	return finalizeSetupJournalWithRemoval(journal, lock.removeJournalVerified)
}

func finalizeSetupJournalWithRemoval(journal setupJournal, removeJournal func(setupJournal, func() error) error) error {
	finalize := func() error {
		parents := make([]*fssecure.Directory, len(journal.Entries))
		targets := make([]os.FileInfo, len(journal.Entries))
		defer func() {
			for _, parent := range parents {
				if parent != nil {
					_ = parent.Close()
				}
			}
		}()
		for index, entry := range journal.Entries {
			parent, err := openSetupParent(entry.Path, false)
			if err != nil {
				return fmt.Errorf("open committed setup target parent %s: %w", entry.Path, err)
			}
			parents[index] = parent
			targetInfo, err := validateCommittedSetupTargetInfoInParent(
				parent,
				journal.SchemaVersion,
				entry,
			)
			if err != nil {
				return fmt.Errorf("committed setup target %s does not match the journal: %w", entry.Path, err)
			}
			targets[index] = targetInfo
			if err := validateSetupCommittedArtifactsInParent(
				parent,
				journal.SchemaVersion,
				entry,
			); err != nil {
				return err
			}
		}
		if beforeFinalizeSetupTransaction != nil {
			if err := beforeFinalizeSetupTransaction(); err != nil {
				return err
			}
		}
		for index, entry := range journal.Entries {
			parent := parents[index]
			parentPath := filepath.Dir(entry.Path)
			if err := parent.Revalidate(parentPath); err != nil {
				return fmt.Errorf("committed setup target parent %s changed during finalization: %w", parentPath, err)
			}
			targetInfo, err := validateCommittedSetupTargetInfoInParent(
				parent,
				journal.SchemaVersion,
				entry,
			)
			if err != nil {
				return fmt.Errorf("committed setup target %s changed during finalization: %w", entry.Path, err)
			}
			if targets[index] == nil || !os.SameFile(targets[index], targetInfo) {
				return fmt.Errorf("committed setup target %s changed during finalization", entry.Path)
			}
			if err := validateSetupCommittedArtifactsInParent(
				parent,
				journal.SchemaVersion,
				entry,
			); err != nil {
				return err
			}
		}
		for index, entry := range journal.Entries {
			parent := parents[index]
			if err := removeSetupArtifactsInParent(
				parent,
				journal.SchemaVersion,
				entry,
				false,
			); err != nil {
				return err
			}
			if err := parent.Sync(); err != nil {
				return err
			}
		}
		for index, entry := range journal.Entries {
			parent := parents[index]
			parentPath := filepath.Dir(entry.Path)
			if err := parent.Revalidate(parentPath); err != nil {
				return fmt.Errorf("committed setup target parent %s changed before journal removal: %w", parentPath, err)
			}
			data, targetInfo, err := parent.ReadRegular(filepath.Base(entry.Path))
			if err != nil ||
				targets[index] == nil ||
				!os.SameFile(targets[index], targetInfo) ||
				targetInfo.Mode() != os.FileMode(entry.AfterMode) ||
				hexDigest(data) != entry.AfterDigest {
				return fmt.Errorf("committed setup target %s changed before journal removal", entry.Path)
			}
		}
		return removeJournal(journal, func() error {
			for index, entry := range journal.Entries {
				parent := parents[index]
				parentPath := filepath.Dir(entry.Path)
				if err := parent.Revalidate(parentPath); err != nil {
					return fmt.Errorf("committed setup target parent %s changed during journal removal: %w", parentPath, err)
				}
				data, targetInfo, err := parent.ReadRegular(filepath.Base(entry.Path))
				if err != nil ||
					targets[index] == nil ||
					!os.SameFile(targets[index], targetInfo) ||
					targetInfo.Mode() != os.FileMode(entry.AfterMode) ||
					hexDigest(data) != entry.AfterDigest {
					return fmt.Errorf("committed setup target %s changed during journal removal", entry.Path)
				}
			}
			return nil
		})
	}
	if journal.InstallLock != "" {
		return withInstallLockPath(journal.InstallLock, finalize)
	}
	return finalize()
}

func readOptionalStableRegularFile(path string) ([]byte, os.FileInfo, error) {
	data, info, err := readStableRegularFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	return data, info, err
}

func stateMatchesEntry(path, digest string, mode os.FileMode, requireExists bool) bool {
	parent, err := openSetupParent(path, false)
	if err != nil {
		return false
	}
	defer parent.Close()
	return stateMatchesEntryInParent(parent, path, digest, mode, requireExists)
}

func stateMatchesEntryInParent(parent *fssecure.Directory, path, digest string, mode os.FileMode, requireExists bool) bool {
	data, info, err := parent.ReadOptionalRegular(filepath.Base(path))
	if err != nil || info == nil {
		return !requireExists && err == nil
	}
	return info.Mode() == mode && hexDigest(data) == digest
}

func openSetupParent(path string, create bool) (*fssecure.Directory, error) {
	parentPath := filepath.Dir(path)
	if create {
		if err := os.MkdirAll(parentPath, 0o700); err != nil {
			return nil, err
		}
	}
	parent, err := fssecure.Open(parentPath)
	if err != nil {
		return nil, fmt.Errorf("open setup target parent %s: %w", parentPath, err)
	}
	if afterSetupParentOpen != nil {
		if err := afterSetupParentOpen(parentPath); err != nil {
			_ = parent.Close()
			return nil, err
		}
	}
	return parent, nil
}

func writeSetupFile(path string, data []byte, mode os.FileMode) error {
	parent, err := openSetupParent(path, true)
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.WriteAtomic(filepath.Base(path), data, mode)
}

func isExactPermissionMode(mode os.FileMode) bool {
	return mode.Perm() != 0 && mode == mode.Perm()
}

func validateSetupRollbackArtifactsInParent(
	parent *fssecure.Directory,
	schemaVersion int,
	entry setupJournalEntry,
) error {
	return validateSetupArtifactsInParent(parent, schemaVersion, entry, true)
}

func setupArtifactIdentity(
	schemaVersion int,
	entry setupJournalEntry,
	artifact string,
	rolledBack bool,
) *setupFileIdentity {
	if schemaVersion != setupJournalSchema {
		return nil
	}
	switch artifact {
	case entry.Backup:
		return entry.BackupIdentity
	case entry.Staged:
		return entry.StagedIdentity
	case entry.Replacement:
		if entry.Existed && !rolledBack {
			return entry.BackupIdentity
		}
		return entry.StagedIdentity
	default:
		return nil
	}
}

func validateSetupArtifactsInParent(
	parent *fssecure.Directory,
	schemaVersion int,
	entry setupJournalEntry,
	rolledBack bool,
) error {
	for _, artifact := range setupEntryArtifacts(entry) {
		info, err := parent.InspectRegular(filepath.Base(artifact))
		if err != nil {
			return err
		}
		if info == nil {
			continue
		}
		var digest string
		var mode os.FileMode
		switch artifact {
		case entry.Staged:
			digest = entry.AfterDigest
			mode = os.FileMode(entry.AfterMode)
			if schemaVersion == legacySetupJournalSchema && entry.Existed && !rolledBack {
				digest = entry.BeforeDigest
				mode = os.FileMode(entry.Mode)
			}
		case entry.Backup:
			if !entry.Existed {
				return fmt.Errorf("setup transaction artifact %s was never owned by the transaction", artifact)
			}
			digest = entry.BeforeDigest
			mode = os.FileMode(entry.Mode)
		case entry.Replacement:
			digest = entry.AfterDigest
			mode = os.FileMode(entry.AfterMode)
			if !rolledBack && schemaVersion != legacySetupJournalSchema && entry.Existed {
				digest = entry.BeforeDigest
				mode = os.FileMode(entry.Mode)
			}
		default:
			return fmt.Errorf("unknown setup transaction artifact %s", artifact)
		}
		identity := setupArtifactIdentity(
			schemaVersion,
			entry,
			artifact,
			rolledBack,
		)
		if _, err := validateSetupArtifactIdentity(
			parent,
			artifact,
			digest,
			mode,
			identity,
		); err != nil {
			return fmt.Errorf("setup transaction artifact %s is not transaction-owned: %w", artifact, err)
		}
	}
	return nil
}

func validateSetupCommittedArtifactsInParent(
	parent *fssecure.Directory,
	schemaVersion int,
	entry setupJournalEntry,
) error {
	if setupJournalEntryIsNoop(entry) {
		return nil
	}
	marker := entry.Staged
	markerIdentity := entry.StagedIdentity
	if schemaVersion == legacySetupJournalSchema && entry.Existed {
		marker = entry.Replacement
		markerIdentity = nil
	}
	markerInfo, err := parent.InspectRegular(filepath.Base(marker))
	if err != nil {
		return fmt.Errorf("inspect setup publication marker %s: %w", marker, err)
	}
	if markerInfo != nil {
		if _, err := validateSetupArtifactIdentity(
			parent,
			marker,
			entry.AfterDigest,
			os.FileMode(entry.AfterMode),
			markerIdentity,
		); err != nil {
			return fmt.Errorf("validate setup publication marker %s: %w", marker, err)
		}
	} else if schemaVersion != setupJournalSchema {
		return fmt.Errorf("validate setup publication marker %s: artifact is missing", marker)
	}
	return validateSetupArtifactsInParent(parent, schemaVersion, entry, false)
}

func removeSetupArtifactsInParent(
	parent *fssecure.Directory,
	schemaVersion int,
	entry setupJournalEntry,
	rolledBack bool,
) error {
	for _, artifact := range setupEntryArtifacts(entry) {
		info, err := parent.InspectRegular(filepath.Base(artifact))
		if err != nil {
			return err
		}
		if info == nil {
			continue
		}
		identity := setupArtifactIdentity(
			schemaVersion,
			entry,
			artifact,
			rolledBack,
		)
		if identity != nil && !setupIdentityMatchesInfo(identity, info) {
			return fmt.Errorf("setup transaction artifact %s changed before removal", artifact)
		}
		removed, err := removeSetupRegularOutcome(parent,
			filepath.Base(artifact),
			info,
			func() error {
				if beforeSetupArtifactRemoval == nil {
					return nil
				}
				return beforeSetupArtifactRemoval(artifact)
			},
		)
		if err != nil {
			if removed {
				if current, inspectErr := parent.InspectRegular(filepath.Base(artifact)); inspectErr != nil {
					return fmt.Errorf("reconcile removed setup transaction artifact %s: %w", artifact, inspectErr)
				} else if current == nil {
					continue
				}
			}
			return fmt.Errorf("remove setup transaction artifact %s: %w", artifact, err)
		}
	}
	return nil
}

func validateRolledBackSetupTargetInParent(parent *fssecure.Directory, entry setupJournalEntry) error {
	data, info, err := parent.ReadOptionalRegular(filepath.Base(entry.Path))
	if err != nil {
		return fmt.Errorf("inspect rolled-back setup target %s: %w", entry.Path, err)
	}
	if !entry.Existed {
		if info != nil {
			return fmt.Errorf("setup-created target %s remains after rollback", entry.Path)
		}
		return nil
	}
	if info == nil || info.Mode() != os.FileMode(entry.Mode) || hexDigest(data) != entry.BeforeDigest {
		return fmt.Errorf("rolled-back setup target %s does not match the original state", entry.Path)
	}
	return nil
}

func validateRolledBackSetupTargets(journal setupJournal) error {
	for _, entry := range journal.Entries {
		parent, err := openSetupParent(entry.Path, false)
		if errors.Is(err, os.ErrNotExist) && !entry.Existed {
			continue
		}
		if err != nil {
			return err
		}
		validateErr := validateRolledBackSetupTargetInParent(parent, entry)
		revalidateErr := parent.Revalidate(filepath.Dir(entry.Path))
		closeErr := parent.Close()
		if validateErr != nil {
			return validateErr
		}
		if revalidateErr != nil {
			return revalidateErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func removeSetupJournal(path string, journal setupJournal) error {
	return removeSetupJournalVerified(path, journal, nil)
}

func removeSetupJournalVerified(path string, journal setupJournal, beforeRemove func() error) error {
	if path == "" {
		return nil
	}
	parent, err := openSetupParent(path, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer parent.Close()
	return removeSetupJournalFromDirectoryVerified(parent, filepath.Base(path), journal, beforeRemove)
}

func removeSetupJournalFromDirectory(
	parent *fssecure.Directory,
	name string,
	journal setupJournal,
) error {
	return removeSetupJournalFromDirectoryVerified(parent, name, journal, nil)
}

func removeSetupJournalFromDirectoryVerified(
	parent *fssecure.Directory,
	name string,
	journal setupJournal,
	beforeRemove func() error,
) error {
	current, info, err := loadSetupJournalFromDirectoryWithInfo(parent, name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, journal) {
		return errors.New("setup transaction journal changed before removal")
	}
	if beforeSetupJournalRemoval != nil {
		if err := beforeSetupJournalRemoval(name); err != nil {
			return err
		}
	}
	if beforeRemove != nil {
		if err := beforeRemove(); err != nil {
			return err
		}
	}
	current, currentInfo, err := loadSetupJournalFromDirectoryWithInfo(parent, name)
	if err != nil || currentInfo == nil || !os.SameFile(info, currentInfo) || !reflect.DeepEqual(current, journal) {
		return errors.New("setup transaction journal changed before removal")
	}
	removed, err := removeSetupRegularOutcome(parent, name, currentInfo, nil)
	if err != nil {
		if removed {
			if currentInfo, inspectErr := parent.InspectRegular(name); inspectErr != nil {
				return fmt.Errorf("reconcile removed setup transaction journal: %w", inspectErr)
			} else if currentInfo == nil {
				if syncErr := syncSetupDirectory(parent); syncErr != nil {
					return fmt.Errorf("sync removed setup transaction journal: %w", syncErr)
				}
				return nil
			}
		}
		return fmt.Errorf("remove setup transaction journal: %w", err)
	}
	return nil
}

func absoluteCleanPath(path, description string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%s path is missing", description)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	if !filepath.IsAbs(absolute) || absolute == string(filepath.Separator) {
		return "", fmt.Errorf("%s path is invalid", description)
	}
	return absolute, nil
}

func setupJournalPath(stateRoot string) string {
	return filepath.Join(stateRoot, "setup-transaction.json")
}

func setupBackupPath(path, token string) string {
	return path + ".sclaude-setup-" + token + ".backup"
}

func setupStagedPath(path, token string) string {
	return path + ".sclaude-setup-" + token + ".stage"
}

func setupReplacementPath(path, token string) string {
	return path + ".sclaude-setup-" + token + ".replacement"
}

func setupEntryArtifacts(entry setupJournalEntry) []string {
	return []string{entry.Backup, entry.Staged, entry.Replacement}
}
