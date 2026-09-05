package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	screenpkg "github.com/ctrl-alt-raccoon/sclaude/internal/screen"
	"github.com/ctrl-alt-raccoon/sclaude/internal/stateroot"
)

const (
	defaultStartTimeout = 2 * time.Second
	defaultStopTimeout  = 5 * time.Second
	startingGrace       = 2 * time.Second
	lastSeenWriteEvery  = 30 * time.Second
)

type Manager struct {
	Store          Store
	Screen         screenpkg.Controller
	BinaryPath     string
	Now            func() time.Time
	StartTimeout   time.Duration
	StopTimeout    time.Duration
	AdmitCreate    func(context.Context) error
	BeforeAttach   func(Record)
	AdmissionHooks stateroot.Hooks
}

// List returns every valid durable record and reconciles its lifecycle with
// the Screen socket list. A corrupt record or failed Screen probe is returned
// as a warning without hiding the other sessions.
func (m Manager) List(ctx context.Context) ([]Record, []error) {
	records, errs := m.Store.List()
	errs = append(errs, m.Store.CleanupLaunches()...)
	if m.Screen == nil {
		return records, append(errs, errors.New("screen controller is required"))
	}
	sockets, err := m.Screen.List(ctx)
	if err != nil {
		return records, append(errs, err)
	}
	records, reconcileErrs := m.reconcile(records, sockets)
	errs = append(errs, reconcileErrs...)
	sortRecords(records)
	return records, errs
}

func (m Manager) Create(ctx context.Context, topic, backend, cwd string, args []string, detach bool) (Record, error) {
	if m.Screen == nil {
		return Record{}, errors.New("screen controller is required")
	}
	if m.BinaryPath == "" {
		return Record{}, errors.New("manager binary path is required")
	}
	absoluteCWD, err := filepath.Abs(cwd)
	if err != nil {
		return Record{}, err
	}
	info, err := os.Stat(absoluteCWD)
	if err != nil || !info.IsDir() {
		return Record{}, fmt.Errorf("working directory is unavailable: %s", absoluteCWD)
	}
	record, err := NewRecord(topic, backend, absoluteCWD, m.now())
	if err != nil {
		return Record{}, err
	}
	if err := stateroot.Prepare(m.Store.Root, true); err != nil {
		return Record{}, err
	}
	admission, err := stateroot.Acquire(m.Store.Root, true, m.AdmissionHooks)
	if err != nil {
		return Record{}, err
	}
	if admission == nil {
		return Record{}, errors.New("session state-root admission lock is unavailable")
	}
	if m.AdmitCreate != nil {
		if err := m.AdmitCreate(ctx); err != nil {
			return Record{}, errors.Join(err, admission.Release())
		}
	}
	request := LaunchRequest{SchemaVersion: 1, SessionID: record.ID, Args: append([]string(nil), args...)}
	// Publish the record first so concurrent cleanup never sees an orphan launch
	// request in the gap between the two writes. Screen is started only afterward.
	if err := m.Store.Save(record); err != nil {
		return Record{}, errors.Join(err, admission.Release())
	}
	if err := m.Store.SaveLaunch(request); err != nil {
		_, _ = m.Store.FailLaunch(record.ID, "prepare launch request failed", m.now())
		return Record{}, errors.Join(err, admission.Release())
	}
	if err := m.Screen.Start(ctx, record.ScreenName, truncate(record.Topic, 40), record.CWD, m.BinaryPath, record.ID); err != nil {
		failed, updateErr := m.Store.FailLaunch(record.ID, err.Error(), m.now())
		if updateErr == nil {
			record = failed
		}
		m.bestEffortStop(ctx, record.ScreenName)
		return record, errors.Join(err, admission.Release())
	}

	var startErr error
	record, startErr = m.waitForStart(ctx, record)
	if startErr != nil {
		failed, updateErr := m.Store.FailLaunch(record.ID, startErr.Error(), m.now())
		if updateErr == nil {
			record = failed
		}
		if record.State == StateRunning {
			startErr = nil
		} else {
			m.bestEffortStop(ctx, record.ScreenName)
			return record, errors.Join(startErr, admission.Release())
		}
	}
	if err := admission.Release(); err != nil {
		return record, fmt.Errorf("release session admission lock: %w", err)
	}
	if !detach && record.State == StateRunning {
		if m.BeforeAttach != nil {
			m.BeforeAttach(record)
		}
		if err := m.Screen.Attach(ctx, record.ScreenName, "normal"); err != nil {
			return record, err
		}
		if updated, loadErr := m.Store.Load(record.ID); loadErr == nil {
			record = updated
		}
	}
	return record, nil
}

func (m Manager) Attach(ctx context.Context, selector, mode string) error {
	records, warnings := m.List(ctx)
	if len(warnings) > 0 && len(records) == 0 {
		return warnings[0]
	}
	record, err := m.Store.Select(selector, records)
	if err != nil {
		return err
	}
	if !record.Active() || record.ScreenStatus == "" {
		return errors.New("session is not attachable")
	}
	if record.ScreenStatus == ScreenAttached && (mode == "" || mode == "normal") {
		return errors.New("session is already attached; choose --multi or --takeover")
	}
	return m.Screen.Attach(ctx, record.ScreenName, mode)
}

func (m Manager) Stop(ctx context.Context, selector string) error {
	records, warnings := m.List(ctx)
	if len(warnings) > 0 && len(records) == 0 {
		return warnings[0]
	}
	record, err := m.Store.Select(selector, records)
	if err != nil {
		return err
	}
	if !record.Active() {
		// Older releases could record a terminal state before Screen exited.
		// An explicit stop may still shut down that exact surviving socket.
		sockets, probeErr := m.Screen.List(ctx)
		if probeErr != nil {
			return probeErr
		}
		found := false
		for _, socket := range sockets {
			found = found || socket.Name == record.ScreenName
		}
		if !found && !record.ExitUnconfirmed() {
			return errors.New("session is already stopped")
		}
	} else {
		record, err = m.Store.requestStop(record.ID, m.now())
		if err != nil {
			return err
		}
	}
	// New runners watch the durable stop intent. Leave Screen and its terminal
	// alive while the runner terminates and waits for the backend. Old runners
	// get the legacy Screen hangup, but absence still cannot substitute for exit.
	stopCtx, cancel := context.WithTimeout(ctx, m.stopTimeout())
	defer cancel()
	var stopErr error
	if record.ShutdownProtocol == 1 {
		if err := m.waitForBackendExit(stopCtx, record.ID); err != nil {
			return err
		}
	} else {
		stopErr = m.Screen.Stop(stopCtx, record.ScreenName)
		if err := m.waitForBackendExit(stopCtx, record.ID); err != nil {
			return errors.Join(stopErr, err)
		}
	}
	if record.ShutdownProtocol == 1 {
		sockets, err := m.Screen.List(stopCtx)
		if err != nil {
			return fmt.Errorf("cannot confirm Screen exit: %w", err)
		}
		for _, socket := range sockets {
			if socket.Name == record.ScreenName {
				stopErr = m.Screen.Stop(stopCtx, record.ScreenName)
				break
			}
		}
	}
	if err := m.confirmStopped(stopCtx, record.ScreenName, stopErr == nil); err != nil {
		// A control error is not proof of exit. Keep the stop intent active so
		// retries remain possible and prune/uninstall cannot orphan the session.
		return errors.Join(stopErr, err)
	}
	_, updateErr := m.Store.stop(record.ID, m.now(), "stopped-by-manager")
	return errors.Join(stopErr, updateErr)
}

func (m Manager) stopTimeout() time.Duration {
	if m.StopTimeout > 0 {
		return m.StopTimeout
	}
	return defaultStopTimeout
}

func (m Manager) waitForBackendExit(ctx context.Context, id string) error {
	for {
		record, err := m.Store.Load(id)
		if err != nil {
			return fmt.Errorf("cannot confirm backend exit: %w", err)
		}
		if !record.ExitUnconfirmed() {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("backend exit is unconfirmed; stop remains pending (older runners may require manual shutdown): %w", ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (m Manager) confirmStopped(ctx context.Context, name string, wait bool) error {
	timeout := m.StopTimeout
	if timeout <= 0 {
		timeout = defaultStartTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		sockets, err := m.Screen.List(probeCtx)
		if err != nil {
			return fmt.Errorf("cannot confirm session exit: %w", err)
		}
		found := false
		for _, socket := range sockets {
			found = found || socket.Name == name
		}
		if !found {
			return nil
		}
		if !wait {
			return errors.New("session is still present in Screen; stop remains pending")
		}
		select {
		case <-probeCtx.Done():
			return fmt.Errorf("session stop remains unconfirmed: %w", probeCtx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (m Manager) Prune(ctx context.Context, selectors []string, olderThan time.Duration, allStopped bool) (int, error) {
	if olderThan < 0 {
		return 0, errors.New("prune age cannot be negative")
	}
	records, warnings := m.List(ctx)
	if len(warnings) > 0 {
		return 0, errors.Join(warnings...)
	}
	selected := map[string]bool{}
	for _, selector := range selectors {
		record, err := m.Store.Select(selector, records)
		if err != nil {
			return 0, err
		}
		selected[record.ID] = true
	}
	removed := 0
	now := m.now()
	for _, record := range records {
		if record.Active() {
			continue
		}
		match := allStopped || selected[record.ID]
		if !match && olderThan > 0 && record.EndedAt != nil {
			match = now.Sub(*record.EndedAt) >= olderThan
		}
		if !match {
			continue
		}
		if err := m.Store.Delete(record); err != nil && !errors.Is(err, os.ErrNotExist) {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

func (m Manager) reconcile(records []Record, sockets []screenpkg.Socket) ([]Record, []error) {
	byName := make(map[string]screenpkg.Socket, len(sockets))
	for _, socket := range sockets {
		byName[socket.Name] = socket
	}
	now := m.now()
	var errs []error
	for i := range records {
		record := &records[i]
		socket, exists := byName[record.ScreenName]
		changed := false
		if exists {
			if record.State == StateStopping {
				continue
			}
			if !record.Active() {
				errs = append(errs, fmt.Errorf("terminal session %s still has a Screen socket; use stop to retry shutdown before cleanup", record.ID))
				continue
			}
			changed = record.State != StateRunning || record.ScreenPID != socket.PID || record.ScreenStatus != ScreenStatus(socket.Status)
			staleSeen := record.LastSeenAt == nil || now.Sub(*record.LastSeenAt) >= lastSeenWriteEvery
			if changed || staleSeen {
				updated, err := m.Store.Update(record.ID, func(current *Record) error {
					if current.State == StateStopping || !current.Active() {
						return nil
					}
					current.State = StateRunning
					current.ScreenPID = socket.PID
					current.ScreenStatus = ScreenStatus(socket.Status)
					current.LastSeenAt = timePointer(now)
					current.UpdatedAt = now
					return nil
				})
				if err != nil {
					errs = append(errs, err)
				} else {
					*record = updated
				}
			}
			continue
		}
		if record.ExitUnconfirmed() {
			errs = append(errs, fmt.Errorf("session %s has no Screen socket but backend exit is unconfirmed; cleanup is blocked", record.ID))
			continue
		}
		if record.Active() && now.Sub(record.CreatedAt) > startingGrace {
			updated, err := m.Store.stop(record.ID, now, "screen-missing")
			if err != nil {
				errs = append(errs, err)
			} else {
				*record = updated
			}
		}
	}
	return records, errs
}

func (m Manager) waitForStart(ctx context.Context, record Record) (Record, error) {
	timeout := m.StartTimeout
	if timeout <= 0 {
		timeout = defaultStartTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		current, err := m.Store.Load(record.ID)
		if err != nil {
			return record, fmt.Errorf("load starting session: %w", err)
		}
		record = current
		switch current.State {
		case StateRunning:
			return record, nil
		case StateStopped, StateFailed:
			message := strings.TrimSpace(current.LaunchError)
			if message == "" {
				message = "session ended before startup completed"
			}
			return record, errors.New(message)
		case StateStopping:
			return record, errors.New("session startup was stopped")
		}

		select {
		case <-ctx.Done():
			return record, fmt.Errorf("session startup canceled: %w", ctx.Err())
		case <-timer.C:
			return record, fmt.Errorf("session startup timed out after %s", timeout)
		case <-ticker.C:
		}
	}
}

func sortRecords(records []Record) {
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].Active() != records[j].Active() {
			return records[i].Active()
		}
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})
}

func (m Manager) bestEffortStop(ctx context.Context, screenName string) {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStartTimeout)
	defer cancel()
	_ = m.Screen.Stop(stopCtx, screenName)
}

func (m Manager) now() time.Time {
	if m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}

func truncate(text string, n int) string {
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	return string(runes[:n])
}

func sanitize(text string) string {
	clean := make([]rune, 0, 300)
	space := false
	for _, r := range strings.TrimSpace(text) {
		if unicode.IsControl(r) {
			space = len(clean) > 0
			continue
		}
		if space && len(clean) < 300 {
			clean = append(clean, ' ')
		}
		space = false
		clean = append(clean, r)
		if len(clean) >= 300 {
			break
		}
	}
	return strings.TrimSpace(string(clean))
}

func timePointer(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}
