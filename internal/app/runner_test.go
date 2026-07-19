package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ctrl-alt-raccoon/sclaude/internal/config"
	"github.com/ctrl-alt-raccoon/sclaude/internal/session"
)

func TestRunSessionRecordsRapidBackendExit(t *testing.T) {
	originalCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalCWD); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	root := t.TempDir()
	cwd := t.TempDir()
	backendPath := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(backendPath, []byte("#!/bin/sh\nexit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	store := session.NewStore(root)
	record, err := session.NewRecord("Rapid exit", "claude", cwd, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLaunch(session.LaunchRequest{SchemaVersion: 1, SessionID: record.ID}); err != nil {
		t.Fatal(err)
	}

	var errOut bytes.Buffer
	code := runSession(context.Background(), record.ID, config.Paths{StateRoot: root}, config.Runtime{RealClaude: backendPath}, IO{Err: &errOut})
	if code != 7 || errOut.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, errOut.String())
	}
	loaded, err := store.Load(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != session.StateStopped || loaded.EndReason != "process-exited" || loaded.ExitCode == nil || *loaded.ExitCode != 7 || loaded.RunnerPID <= 0 || loaded.BackendPID <= 0 || loaded.StartedAt == nil || loaded.EndedAt == nil {
		t.Fatalf("unexpected terminal record: %+v", loaded)
	}
}

func TestRunSessionMissingLaunchFailsWithoutResurrectingStop(t *testing.T) {
	root := t.TempDir()
	store := session.NewStore(root)
	record, err := session.NewRecord("Stopped startup", "claude", t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	record.State = session.StateStopping
	record.EndReason = "stopped-by-manager"
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}

	var errOut bytes.Buffer
	code := runSession(context.Background(), record.ID, config.Paths{StateRoot: root}, config.Runtime{RealClaude: "/bin/sh"}, IO{Err: &errOut})
	if code != 1 || errOut.Len() == 0 {
		t.Fatalf("code=%d stderr=%q", code, errOut.String())
	}
	loaded, err := store.Load(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != session.StateStopping || loaded.EndReason != "stopped-by-manager" {
		t.Fatalf("missing launch overwrote stop intent: %+v", loaded)
	}
}
