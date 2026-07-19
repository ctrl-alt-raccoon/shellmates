package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	screenpkg "github.com/ctrl-alt-raccoon/sclaude/internal/screen"
	"github.com/ctrl-alt-raccoon/sclaude/internal/stateroot"
)

func TestValidateTopicAndSlug(t *testing.T) {
	for _, invalid := range []string{"  \n ", "secret\nsecond", strings.Repeat("x", 201)} {
		if _, err := ValidateTopic(invalid); err == nil {
			t.Fatalf("invalid topic accepted: %q", invalid)
		}
	}
	topic, err := ValidateTopic("  OAuth callback refactor  ")
	if err != nil || topic != "OAuth callback refactor" {
		t.Fatalf("topic=%q err=%v", topic, err)
	}
	if slug := Slugify("OAuth callback/refactor!"); slug != "oauth-callback-refactor" {
		t.Fatalf("slug=%q", slug)
	}
	if slug := Slugify("日本語"); slug != "topic" {
		t.Fatalf("unicode fallback=%q", slug)
	}
	if slug := Slugify(strings.Repeat("a", 40)); len(slug) != 32 {
		t.Fatalf("slug length=%d slug=%q", len(slug), slug)
	}
}

func TestStoreRoundTripPrivateAtomicAndTransientLaunch(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	now := time.Now().UTC()
	record, err := NewRecord("Test work", "claude", t.TempDir(), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLaunch(LaunchRequest{SchemaVersion: 1, SessionID: record.ID, Args: []string{"-p", "private prompt"}}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, store.SessionsDir, store.LaunchDir} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode=%o", path, info.Mode().Perm())
		}
	}
	for _, path := range []string{store.recordPath(record.ID), store.launchPath(record.ID), store.LockPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode=%o", path, info.Mode().Perm())
		}
	}
	loaded, err := store.Load(record.ID)
	if err != nil || loaded.Topic != record.Topic {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	data, err := os.ReadFile(store.recordPath(record.ID))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private prompt") || strings.Contains(string(data), `"args"`) {
		t.Fatalf("durable record leaked args: %s", data)
	}
	request, err := store.ConsumeLaunch(record.ID)
	if err != nil || len(request.Args) != 2 {
		t.Fatalf("request=%+v err=%v", request, err)
	}
	if _, err := store.ConsumeLaunch(record.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("launch request replayed: %v", err)
	}
	entries, err := os.ReadDir(store.LaunchDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("transient launch files remain: %v err=%v", entries, err)
	}
}

func TestConsumeMalformedLaunchDeletesIt(t *testing.T) {
	store := NewStore(t.TempDir())
	id := strings.Repeat("a", 32)
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.launchPath(id), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumeLaunch(id); err == nil {
		t.Fatal("malformed launch accepted")
	}
	if _, err := os.Stat(store.launchPath(id)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("malformed request remained: %v", err)
	}
}

func TestStoreRejectsTraversalAndMismatchedRecord(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.Load("../outside"); err == nil {
		t.Fatal("path traversal ID accepted")
	}
	if err := store.Save(Record{SchemaVersion: 1, ID: "../outside", ScreenName: "sc-test"}); err == nil {
		t.Fatal("invalid record ID accepted")
	}
}

func TestStoreUpdateSerializesConcurrentLifecycleChanges(t *testing.T) {
	store := NewStore(t.TempDir())
	record, _ := NewRecord("Concurrent", "claude", t.TempDir(), time.Now())
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Update(record.ID, func(current *Record) error {
				current.RunnerPID++
				return nil
			})
			if err != nil {
				t.Errorf("update: %v", err)
			}
		}()
	}
	wg.Wait()
	loaded, err := store.Load(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RunnerPID != 10 {
		t.Fatalf("runner PID counter=%d", loaded.RunnerPID)
	}
}

func TestSelectPrecedenceAndAmbiguity(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now()
	one, _ := NewRecord("Duplicate", "claude", t.TempDir(), now)
	two, _ := NewRecord("Duplicate", "claudex", t.TempDir(), now)
	if _, err := store.Select("Duplicate", []Record{one, two}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguity, got %v", err)
	}
	selected, err := store.Select(one.ID[:8], []Record{one, two})
	if err != nil || selected.ID != one.ID {
		t.Fatalf("selected=%+v err=%v", selected, err)
	}
	selected, err = store.Select(one.ID, []Record{one, two})
	if err != nil || selected.ID != one.ID {
		t.Fatalf("exact selection=%+v err=%v", selected, err)
	}
}

func TestManagerReconcileRunningMissingAndStableWrites(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	running, _ := NewRecord("Running", "claude", t.TempDir(), now.Add(-time.Minute))
	missing, _ := NewRecord("Missing", "claude", t.TempDir(), now.Add(-time.Minute))
	if err := store.Save(running); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(missing); err != nil {
		t.Fatal(err)
	}
	fake := &fakeScreen{sockets: []screenpkg.Socket{{PID: 123, Name: running.ScreenName, Status: screenpkg.Detached}}}
	manager := Manager{Store: store, Screen: fake, Now: func() time.Time { return now }}
	records, errs := manager.List(context.Background())
	if len(errs) != 0 {
		t.Fatalf("warnings=%v", errs)
	}
	gotRunning := findRecord(t, records, running.ID)
	if gotRunning.State != StateRunning || gotRunning.ScreenPID != 123 || gotRunning.ScreenStatus != ScreenDetached {
		t.Fatalf("running=%+v", gotRunning)
	}
	gotMissing := findRecord(t, records, missing.ID)
	if gotMissing.State != StateStopped || gotMissing.EndReason != "screen-missing" || gotMissing.EndedAt == nil {
		t.Fatalf("missing=%+v", gotMissing)
	}
	firstInfo, err := os.Stat(store.recordPath(running.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, errs := manager.List(context.Background()); len(errs) != 0 {
		t.Fatal(errs)
	}
	secondInfo, err := os.Stat(store.recordPath(running.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !secondInfo.ModTime().Equal(firstInfo.ModTime()) {
		t.Fatalf("unchanged reconciliation rewrote record: %s -> %s", firstInfo.ModTime(), secondInfo.ModTime())
	}
}

func TestManagerCreateUsesTransientArgsAndCleansFailedLaunch(t *testing.T) {
	store := NewStore(t.TempDir())
	cwd := t.TempDir()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	fake := &fakeScreen{startErr: errors.New("screen refused")}
	manager := Manager{Store: store, Screen: fake, BinaryPath: "/bin/sclaude", Now: func() time.Time { return now }}
	record, err := manager.Create(context.Background(), "Private launch", "claude", cwd, []string{"-p", "secret prompt"}, true)
	if err == nil {
		t.Fatal("failed Screen start reported success")
	}
	if record.State != StateFailed || !strings.Contains(record.LaunchError, "screen refused") {
		t.Fatalf("record=%+v", record)
	}
	data, readErr := os.ReadFile(store.recordPath(record.ID))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(data), "secret prompt") {
		t.Fatalf("record leaked prompt: %s", data)
	}
	if _, statErr := os.Stat(store.launchPath(record.ID)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed launch request remains: %v", statErr)
	}
}

func TestManagerAttachStopAndPrune(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	record, _ := NewRecord("Managed", "claude", t.TempDir(), now.Add(-time.Hour))
	record.State = StateRunning
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	fake := &fakeScreen{sockets: []screenpkg.Socket{{PID: 22, Name: record.ScreenName, Status: screenpkg.Attached}}}
	manager := Manager{Store: store, Screen: fake, Now: func() time.Time { return now }}
	if err := manager.Attach(context.Background(), record.ID, "normal"); err == nil {
		t.Fatal("normal attach to attached session succeeded")
	}
	if err := manager.Attach(context.Background(), record.ID, "multi"); err != nil {
		t.Fatal(err)
	}
	if fake.attachMode != "multi" {
		t.Fatalf("attach mode=%q", fake.attachMode)
	}
	if err := manager.Stop(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
	stopped, err := store.Load(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.State != StateStopped || stopped.EndReason != "stopped-by-manager" || fake.stopped != record.ScreenName {
		t.Fatalf("stopped=%+v fake=%+v", stopped, fake)
	}
	fake.sockets = nil
	removed, err := manager.Prune(context.Background(), []string{record.ID}, 0, false)
	if err != nil || removed != 1 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	if _, err := store.Load(record.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("record still exists: %v", err)
	}
}

func TestListReturnsRecordsWhenScreenProbeFails(t *testing.T) {
	store := NewStore(t.TempDir())
	record, _ := NewRecord("Visible", "claude", t.TempDir(), time.Now())
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	manager := Manager{Store: store, Screen: &fakeScreen{listErr: errors.New("probe failed")}}
	records, errs := manager.List(context.Background())
	if len(records) != 1 || len(errs) != 1 || !strings.Contains(errs[0].Error(), "probe failed") {
		t.Fatalf("records=%+v errs=%v", records, errs)
	}
}

func TestRecordJSONDoesNotContainLaunchFields(t *testing.T) {
	type durable Record
	var _ = durable{}
	data := []byte(`{"schema_version":1,"id":"` + strings.Repeat("a", 32) + `","topic":"t","topic_slug":"t","backend":"claude","cwd":"/tmp","screen_name":"sc","created_at":"2026-07-17T00:00:00Z","updated_at":"2026-07-17T00:00:00Z","state":"starting"}`)
	if strings.Contains(string(data), "args") || strings.Contains(string(data), "prompt") {
		t.Fatal("fixture contains transient data")
	}
}

type fakeScreen struct {
	sockets    []screenpkg.Socket
	listErr    error
	startErr   error
	attachErr  error
	stopErr    error
	start      func(string)
	started    string
	attachMode string
	stopped    string
}

func (f *fakeScreen) List(context.Context) ([]screenpkg.Socket, error) {
	return append([]screenpkg.Socket(nil), f.sockets...), f.listErr
}

func (f *fakeScreen) Start(_ context.Context, name, _, _, _, id string) error {
	f.started = name
	if f.start != nil {
		f.start(id)
	}
	return f.startErr
}

func (f *fakeScreen) Attach(_ context.Context, _ string, mode string) error {
	f.attachMode = mode
	return f.attachErr
}

func (f *fakeScreen) Stop(_ context.Context, name string) error {
	f.stopped = name
	if f.stopErr == nil {
		f.sockets = nil
	}
	return f.stopErr
}

func findRecord(t *testing.T, records []Record, id string) Record {
	t.Helper()
	for _, record := range records {
		if record.ID == id {
			return record
		}
	}
	t.Fatalf("record %s not found", id)
	return Record{}
}

func TestManagerCreateTimeoutFailsAndStopsScreen(t *testing.T) {
	store := NewStore(t.TempDir())
	fake := &fakeScreen{}
	manager := Manager{
		Store:        store,
		Screen:       fake,
		BinaryPath:   "/bin/sclaude",
		StartTimeout: 25 * time.Millisecond,
	}
	record, err := manager.Create(context.Background(), "Timeout", "claude", t.TempDir(), []string{"secret"}, true)
	if err == nil || !strings.Contains(err.Error(), "timed out after 25ms") {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	if record.State != StateFailed || record.EndedAt == nil || !strings.Contains(record.LaunchError, "timed out") {
		t.Fatalf("record=%+v", record)
	}
	if fake.stopped != record.ScreenName {
		t.Fatalf("screen stop=%q want=%q", fake.stopped, record.ScreenName)
	}
	if _, err := os.Stat(store.launchPath(record.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("launch request remains: %v", err)
	}
}

func TestManagerCreateRapidTerminalReturnsErrorWithoutResurrection(t *testing.T) {
	store := NewStore(t.TempDir())
	fake := &fakeScreen{}
	fake.start = func(id string) {
		now := time.Now().UTC()
		if _, err := store.MarkRunning(id, 42, now); err != nil {
			t.Errorf("mark running: %v", err)
			return
		}
		if _, err := store.Finish(id, 0, "", now.Add(time.Millisecond)); err != nil {
			t.Errorf("finish: %v", err)
		}
	}
	manager := Manager{Store: store, Screen: fake, BinaryPath: "/bin/sclaude", StartTimeout: time.Second}
	record, err := manager.Create(context.Background(), "Rapid", "claude", t.TempDir(), nil, true)
	if err == nil || !strings.Contains(err.Error(), "session ended before startup completed") {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	loaded, loadErr := store.Load(record.ID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if loaded.State != StateStopped || loaded.ExitCode == nil || *loaded.ExitCode != 0 {
		t.Fatalf("terminal record was overwritten: %+v", loaded)
	}
}

func TestReconcileDoesNotResurrectStoppingOrTerminalSessions(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now().UTC()
	stopping, _ := NewRecord("Stopping", "claude", t.TempDir(), now.Add(-time.Minute))
	stopping.State = StateStopping
	failed, _ := NewRecord("Failed", "claude", t.TempDir(), now.Add(-time.Minute))
	failed.State = StateFailed
	failed.LaunchError = "failed"
	for _, record := range []Record{stopping, failed} {
		if err := store.Save(record); err != nil {
			t.Fatal(err)
		}
	}
	fake := &fakeScreen{sockets: []screenpkg.Socket{
		{PID: 10, Name: stopping.ScreenName, Status: screenpkg.Detached},
		{PID: 11, Name: failed.ScreenName, Status: screenpkg.Attached},
	}}
	manager := Manager{Store: store, Screen: fake, Now: func() time.Time { return now }}
	records, errs := manager.List(context.Background())
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if got := findRecord(t, records, stopping.ID); got.State != StateStopping || got.ScreenPID != 0 {
		t.Fatalf("stopping resurrected: %+v", got)
	}
	if got := findRecord(t, records, failed.ID); got.State != StateFailed || got.ScreenPID != 0 {
		t.Fatalf("failed resurrected: %+v", got)
	}
}

func TestReconcileMissingSocketDeletesLaunch(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now().UTC()
	record, _ := NewRecord("Missing", "claude", t.TempDir(), now.Add(-time.Minute))
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLaunch(LaunchRequest{SessionID: record.ID, Args: []string{"secret"}}); err != nil {
		t.Fatal(err)
	}
	manager := Manager{Store: store, Screen: &fakeScreen{}, Now: func() time.Time { return now }}
	records, errs := manager.List(context.Background())
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if got := findRecord(t, records, record.ID); got.State != StateStopped || got.EndReason != "screen-missing" {
		t.Fatalf("record=%+v", got)
	}
	if _, err := os.Stat(store.launchPath(record.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("launch request remains: %v", err)
	}
}

func TestCleanupLaunchesRemovesTerminalAndMissingRequests(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now().UTC()
	active, _ := NewRecord("Active", "claude", t.TempDir(), now)
	terminal, _ := NewRecord("Terminal", "claude", t.TempDir(), now)
	terminal.State = StateStopped
	missingID := strings.Repeat("c", 32)
	for _, record := range []Record{active, terminal} {
		if err := store.Save(record); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{active.ID, terminal.ID, missingID} {
		if err := store.SaveLaunch(LaunchRequest{SessionID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if errs := store.CleanupLaunches(); len(errs) != 0 {
		t.Fatal(errs)
	}
	if _, err := os.Stat(store.launchPath(active.ID)); err != nil {
		t.Fatalf("active launch removed: %v", err)
	}
	for _, id := range []string{terminal.ID, missingID} {
		if _, err := os.Stat(store.launchPath(id)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("launch %s remains: %v", id, err)
		}
	}
}

func TestStoreLifecycleTransitionsAreMonotonicUnderConcurrency(t *testing.T) {
	store := NewStore(t.TempDir())
	record, _ := NewRecord("Race", "claude", t.TempDir(), time.Now())
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLaunch(LaunchRequest{SessionID: record.ID}); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, _ = store.MarkRunning(record.ID, 10, time.Now())
	}()
	go func() {
		defer wg.Done()
		<-start
		_, _ = store.FailLaunch(record.ID, "failed\nwith control", time.Now())
	}()
	close(start)
	wg.Wait()
	loaded, err := store.Load(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != StateRunning && loaded.State != StateFailed {
		t.Fatalf("unexpected state: %+v", loaded)
	}
	if loaded.State == StateFailed && strings.Contains(loaded.LaunchError, "\n") {
		t.Fatalf("unsanitized launch error: %q", loaded.LaunchError)
	}
	if loaded.State == StateRunning {
		if _, err := store.FailLaunch(record.ID, "late failure", time.Now()); err != nil {
			t.Fatal(err)
		}
		loaded, err = store.Load(record.ID)
		if err != nil || loaded.State != StateRunning {
			t.Fatalf("running resurrected/overwritten: %+v err=%v", loaded, err)
		}
	}
}

func TestStoreDeleteDefensivelyRemovesLaunch(t *testing.T) {
	store := NewStore(t.TempDir())
	record, _ := NewRecord("Delete", "claude", t.TempDir(), time.Now())
	record.State = StateStopped
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLaunch(LaunchRequest{SessionID: record.ID}); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(record); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{store.recordPath(record.ID), store.launchPath(record.ID)} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("path remains %s: %v", path, err)
		}
	}
}

func TestManagerStopBestEffortFinalizesOnScreenError(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now().UTC()
	record, _ := NewRecord("Stop", "claude", t.TempDir(), now.Add(-time.Minute))
	record.State = StateRunning
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	fake := &fakeScreen{
		sockets: []screenpkg.Socket{{PID: 12, Name: record.ScreenName, Status: screenpkg.Detached}},
		stopErr: errors.New("socket disappeared"),
	}
	manager := Manager{Store: store, Screen: fake, Now: func() time.Time { return now }}
	if err := manager.Stop(context.Background(), record.ID); err == nil || !strings.Contains(err.Error(), "socket disappeared") {
		t.Fatalf("err=%v", err)
	}
	loaded, err := store.Load(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != StateStopped || loaded.EndReason != "stopped-by-manager" {
		t.Fatalf("record=%+v", loaded)
	}
}

func TestManagerCreateReleasesAdmissionBeforeAttach(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	store := NewStore(stateRoot)
	fake := &fakeScreen{}
	fake.start = func(id string) {
		if _, err := store.MarkRunning(id, 42, time.Now()); err != nil {
			t.Errorf("mark running: %v", err)
		}
	}
	manager := Manager{
		Store:      store,
		Screen:     fake,
		BinaryPath: "/bin/sclaude",
		BeforeAttach: func(Record) {
			acquired := make(chan error, 1)
			go func() {
				lock, err := stateroot.Acquire(stateRoot, false, stateroot.Hooks{})
				if lock != nil {
					err = errors.Join(err, lock.Release())
				}
				acquired <- err
			}()
			select {
			case err := <-acquired:
				if err != nil {
					t.Errorf("acquire admission before attach: %v", err)
				}
			case <-time.After(time.Second):
				t.Error("interactive attach retained the lifecycle admission lock")
			}
		},
	}
	if _, err := manager.Create(context.Background(), "Attach", "claude", t.TempDir(), nil, false); err != nil {
		t.Fatal(err)
	}
	if fake.attachMode != "normal" {
		t.Fatalf("attach mode = %q", fake.attachMode)
	}
}

func TestManagerCreateRevalidatesAfterAdmission(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	manager := Manager{
		Store:      NewStore(stateRoot),
		Screen:     &fakeScreen{},
		BinaryPath: "/bin/sclaude",
		AdmitCreate: func(context.Context) error {
			return errors.New("runtime removed")
		},
	}
	if _, err := manager.Create(context.Background(), "Rejected", "claude", t.TempDir(), nil, true); err == nil || !strings.Contains(err.Error(), "runtime removed") {
		t.Fatalf("create error = %v", err)
	}
	for _, path := range []string{manager.Store.SessionsDir, manager.Store.LaunchDir, manager.Store.LockPath} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("rejected create published state at %s: %v", path, err)
		}
	}
}

func TestStorePathsRemainUnderRoot(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	id := strings.Repeat("b", 32)
	for _, path := range []string{store.recordPath(id), store.launchPath(id)} {
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Fatalf("path escaped root: %s", path)
		}
	}
}
