package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

type Store struct {
	Root        string
	SessionsDir string
	LaunchDir   string
	LockPath    string
}

var ErrSessionNotRunnable = errors.New("session is no longer runnable")

func NewStore(stateRoot string) Store {
	return Store{
		Root:        stateRoot,
		SessionsDir: filepath.Join(stateRoot, "sessions"),
		LaunchDir:   filepath.Join(stateRoot, "launch"),
		LockPath:    filepath.Join(stateRoot, "lock"),
	}
}

func (s Store) Ensure() error {
	for _, dir := range []string{s.Root, s.SessionsDir, s.LaunchDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func (s Store) Save(record Record) error {
	if err := validateRecord(record); err != nil {
		return err
	}
	return s.withLock(func() error {
		return writeJSONAtomic(s.recordPath(record.ID), record)
	})
}

// Update applies a read-modify-write while holding the store lock. Callers use
// it for lifecycle transitions so a reconciliation pass cannot overwrite a
// newer runner update with a stale copy of the record.
func (s Store) Update(id string, update func(*Record) error) (Record, error) {
	if err := validateID(id); err != nil {
		return Record{}, err
	}
	var result Record
	err := s.withLock(func() error {
		record, err := s.loadUnlocked(id)
		if err != nil {
			return err
		}
		if err := update(&record); err != nil {
			return err
		}
		if err := validateRecord(record); err != nil {
			return err
		}
		if err := writeJSONAtomic(s.recordPath(id), record); err != nil {
			return err
		}
		result = record
		return nil
	})
	return result, err
}

// FailLaunch records a sanitized startup failure without overwriting a stop
// request or terminal result. The transient launch request is deleted while the
// same store lock is held.
func (s Store) FailLaunch(id, message string, now time.Time) (Record, error) {
	return s.lifecycleUpdate(id, true, func(record *Record) (bool, error) {
		if record.State != StateStarting {
			return false, nil
		}
		record.State = StateFailed
		record.LaunchError = sanitize(message)
		record.ScreenStatus = ""
		record.ScreenPID = 0
		record.UpdatedAt = now.UTC()
		if record.EndedAt == nil {
			record.EndedAt = timePointer(now)
		}
		return true, nil
	})
}

// MarkRunning transitions a starting session to running. A stop request or
// terminal state wins if it was recorded first.
func (s Store) MarkRunning(id string, runnerPID int, now time.Time) (Record, error) {
	return s.lifecycleUpdate(id, false, func(record *Record) (bool, error) {
		switch record.State {
		case StateStarting:
			record.State = StateRunning
			record.RunnerPID = runnerPID
			record.UpdatedAt = now.UTC()
			if record.StartedAt == nil {
				record.StartedAt = timePointer(now)
			}
			return true, nil
		case StateRunning:
			if record.RunnerPID == runnerPID {
				return false, nil
			}
			record.RunnerPID = runnerPID
			record.UpdatedAt = now.UTC()
			return true, nil
		default:
			return false, ErrSessionNotRunnable
		}
	})
}

// MarkStarted atomically records the runner and child PIDs after the backend
// process has started. A concurrent stop or terminal transition wins.
func (s Store) MarkStarted(id string, runnerPID, backendPID int, now time.Time) (Record, error) {
	if runnerPID <= 0 || backendPID <= 0 {
		return Record{}, errors.New("runner and backend PIDs must be positive")
	}
	return s.lifecycleUpdate(id, false, func(record *Record) (bool, error) {
		switch record.State {
		case StateStarting:
			record.State = StateRunning
			record.RunnerPID = runnerPID
			record.BackendPID = backendPID
			record.UpdatedAt = now.UTC()
			if record.StartedAt == nil {
				record.StartedAt = timePointer(now)
			}
			return true, nil
		case StateRunning:
			if record.RunnerPID == runnerPID && record.BackendPID == backendPID {
				return false, nil
			}
			return false, ErrSessionNotRunnable
		default:
			return false, ErrSessionNotRunnable
		}
	})
}

// SetBackendPID records the child PID only while the session is running.
func (s Store) SetBackendPID(id string, backendPID int, now time.Time) (Record, error) {
	return s.lifecycleUpdate(id, false, func(record *Record) (bool, error) {
		if record.State != StateRunning {
			return false, ErrSessionNotRunnable
		}
		if record.BackendPID == backendPID {
			return false, nil
		}
		record.BackendPID = backendPID
		record.UpdatedAt = now.UTC()
		return true, nil
	})
}

// Finish records safe exit information without overwriting a newer stop intent
// or an existing terminal result. It also removes any launch request.
func (s Store) Finish(id string, exitCode int, termSignal string, now time.Time) (Record, error) {
	return s.lifecycleUpdate(id, true, func(record *Record) (bool, error) {
		switch record.State {
		case StateStarting, StateRunning:
			MarkStopped(record, now, "process-exited")
			record.ExitCode = &exitCode
			record.TermSignal = termSignal
			return true, nil
		case StateStopping:
			reason := record.EndReason
			if reason == "" {
				reason = "stop-requested"
			}
			MarkStopped(record, now, reason)
			record.ExitCode = &exitCode
			record.TermSignal = termSignal
			return true, nil
		case StateStopped:
			changed := false
			if record.ExitCode == nil {
				record.ExitCode = &exitCode
				changed = true
			}
			if record.TermSignal == "" && termSignal != "" {
				record.TermSignal = termSignal
				changed = true
			}
			if changed {
				record.UpdatedAt = now.UTC()
			}
			return changed, nil
		default:
			return false, nil
		}
	})
}

func (s Store) requestStop(id string, now time.Time) (Record, error) {
	return s.lifecycleUpdate(id, true, func(record *Record) (bool, error) {
		if !record.Active() {
			return false, errors.New("session is already stopped")
		}
		if record.State == StateStopping {
			return false, nil
		}
		record.State = StateStopping
		record.EndReason = "stopped-by-manager"
		record.UpdatedAt = now.UTC()
		return true, nil
	})
}

func (s Store) stop(id string, now time.Time, reason string) (Record, error) {
	return s.lifecycleUpdate(id, true, func(record *Record) (bool, error) {
		if !record.Active() {
			return false, nil
		}
		MarkStopped(record, now, reason)
		return true, nil
	})
}

func (s Store) lifecycleUpdate(id string, deleteLaunch bool, update func(*Record) (bool, error)) (Record, error) {
	if err := validateID(id); err != nil {
		return Record{}, err
	}
	var result Record
	err := s.withLock(func() error {
		record, err := s.loadUnlocked(id)
		if err != nil {
			return err
		}
		changed, err := update(&record)
		if err != nil {
			return err
		}
		if changed {
			if err := validateRecord(record); err != nil {
				return err
			}
			if err := writeJSONAtomic(s.recordPath(id), record); err != nil {
				return err
			}
		}
		if deleteLaunch {
			if err := removeIfExists(s.launchPath(id)); err != nil {
				return err
			}
		}
		result = record
		return nil
	})
	return result, err
}

func (s Store) Load(id string) (Record, error) {
	if err := validateID(id); err != nil {
		return Record{}, err
	}
	return s.loadUnlocked(id)
}

func (s Store) loadUnlocked(id string) (Record, error) {
	data, err := os.ReadFile(s.recordPath(id))
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, err
	}
	if err := validateRecord(record); err != nil {
		return Record{}, err
	}
	if record.ID != id {
		return Record{}, fmt.Errorf("session record ID %q does not match filename", record.ID)
	}
	return record, nil
}

func (s Store) List() ([]Record, []error) {
	entries, err := os.ReadDir(s.SessionsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []error{err}
	}
	var records []Record
	var errs []error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if err := validateID(id); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", entry.Name(), err))
			continue
		}
		record, loadErr := s.loadUnlocked(id)
		if loadErr != nil {
			errs = append(errs, fmt.Errorf("%s: %w", entry.Name(), loadErr))
			continue
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].CreatedAt.After(records[j].CreatedAt) })
	return records, errs
}

func (s Store) SaveLaunch(request LaunchRequest) error {
	if err := validateID(request.SessionID); err != nil {
		return fmt.Errorf("launch request: %w", err)
	}
	if request.SchemaVersion == 0 {
		request.SchemaVersion = 1
	}
	if request.SchemaVersion != 1 {
		return fmt.Errorf("unsupported launch request schema %d", request.SchemaVersion)
	}
	request.Args = append([]string(nil), request.Args...)
	return s.withLock(func() error {
		return writeJSONAtomic(s.launchPath(request.SessionID), request)
	})
}

// ConsumeLaunch makes a launch request unavailable before decoding it. Even a
// malformed request is removed, so arguments cannot be replayed or left behind
// after the one process allowed to consume them has claimed the request.
func (s Store) ConsumeLaunch(id string) (LaunchRequest, error) {
	if err := validateID(id); err != nil {
		return LaunchRequest{}, err
	}
	var request LaunchRequest
	err := s.withLock(func() error {
		path := s.launchPath(id)
		claimed, err := os.CreateTemp(s.LaunchDir, ".consume-*")
		if err != nil {
			return err
		}
		claimPath := claimed.Name()
		if err := claimed.Close(); err != nil {
			_ = os.Remove(claimPath)
			return err
		}
		if err := os.Remove(claimPath); err != nil {
			return err
		}
		if err := os.Rename(path, claimPath); err != nil {
			return err
		}
		defer os.Remove(claimPath)

		data, err := os.ReadFile(claimPath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &request); err != nil {
			return err
		}
		if request.SchemaVersion != 1 {
			return fmt.Errorf("unsupported launch request schema %d", request.SchemaVersion)
		}
		if request.SessionID != id {
			return errors.New("launch request session ID mismatch")
		}
		request.Args = append([]string(nil), request.Args...)
		return nil
	})
	return request, err
}

func (s Store) DeleteLaunch(id string) error {
	if err := validateID(id); err != nil {
		return err
	}
	return s.withLock(func() error {
		return removeIfExists(s.launchPath(id))
	})
}

// CleanupLaunches removes requests whose record is missing or terminal. A
// starting, running, or stopping record may still have a runner racing to
// consume its one-use request, so those requests are preserved.
func (s Store) CleanupLaunches() []error {
	var errs []error
	err := s.withLock(func() error {
		entries, err := os.ReadDir(s.LaunchDir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			id := strings.TrimSuffix(entry.Name(), ".json")
			if err := validateID(id); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", entry.Name(), err))
				continue
			}
			record, loadErr := s.loadUnlocked(id)
			if loadErr == nil && record.Active() {
				continue
			}
			if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
				errs = append(errs, fmt.Errorf("%s: %w", entry.Name(), loadErr))
				continue
			}
			if err := removeIfExists(s.launchPath(id)); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", entry.Name(), err))
			}
		}
		return nil
	})
	if err != nil {
		errs = append(errs, err)
	}
	return errs
}

func (s Store) Delete(record Record) error {
	if record.Active() {
		return errors.New("cannot prune an active session")
	}
	if err := validateID(record.ID); err != nil {
		return err
	}
	return s.withLock(func() error {
		if err := removeIfExists(s.launchPath(record.ID)); err != nil {
			return err
		}
		return os.Remove(s.recordPath(record.ID))
	})
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s Store) Select(selector string, records []Record) (Record, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return Record{}, errors.New("session selector is required")
	}

	var exact []Record
	for _, record := range records {
		if record.ID == selector || record.ScreenName == selector {
			exact = append(exact, record)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) > 1 {
		return Record{}, fmt.Errorf("ambiguous session %q (%d matches)", selector, len(exact))
	}

	var matches []Record
	for _, record := range records {
		if record.Topic == selector || validIDPrefix(selector) && strings.HasPrefix(record.ID, strings.ToLower(selector)) {
			matches = append(matches, record)
		}
	}
	if len(matches) == 0 {
		return Record{}, fmt.Errorf("unknown session %q", selector)
	}
	if len(matches) > 1 {
		return Record{}, fmt.Errorf("ambiguous session %q (%d matches)", selector, len(matches))
	}
	return matches[0], nil
}

func (s Store) recordPath(id string) string {
	return filepath.Join(s.SessionsDir, id+".json")
}

func (s Store) launchPath(id string) string {
	return filepath.Join(s.LaunchDir, id+".json")
}

func (s Store) withLock(fn func() error) error {
	if err := s.Ensure(); err != nil {
		return err
	}
	lock, err := os.OpenFile(s.LockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := lock.Chmod(0o600); err != nil {
		return err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return fn()
}

func writeJSONAtomic(path string, value any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".sclaude-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return syncDir(dir)
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func validateRecord(record Record) error {
	if record.SchemaVersion != 1 {
		return fmt.Errorf("unsupported session schema %d", record.SchemaVersion)
	}
	if err := validateID(record.ID); err != nil {
		return err
	}
	if record.ScreenName == "" {
		return errors.New("session record requires screen name")
	}
	return nil
}

func validateID(id string) error {
	if len(id) != 32 {
		return errors.New("invalid session ID")
	}
	for _, r := range id {
		if r < '0' || r > '9' && r < 'a' || r > 'f' {
			return errors.New("invalid session ID")
		}
	}
	return nil
}

func validIDPrefix(prefix string) bool {
	if len(prefix) < 4 || len(prefix) > 32 {
		return false
	}
	for _, r := range strings.ToLower(prefix) {
		if r < '0' || r > '9' && r < 'a' || r > 'f' {
			return false
		}
	}
	return true
}

func MarkStopped(record *Record, now time.Time, reason string) {
	record.State = StateStopped
	record.ScreenStatus = ""
	record.ScreenPID = 0
	record.EndReason = reason
	record.UpdatedAt = now.UTC()
	if record.EndedAt == nil {
		ended := now.UTC()
		record.EndedAt = &ended
	}
}
