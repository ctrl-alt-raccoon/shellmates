package setup

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCandidateOwnsInstallerMigration(t *testing.T) {
	if os.Getenv("SCLAUDE_RELEASE_INTEGRATION") != "1" {
		t.Skip("opt in with SCLAUDE_RELEASE_INTEGRATION=1; builds a local candidate, never downloads a release")
	}
	root := t.TempDir()
	source := filepath.Join(root, "downloaded-candidate")
	build := exec.Command("go", "build", "-o", source, "./cmd/sclaude")
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME"} {
		t.Setenv(key, filepath.Join(root, key))
	}
	layout, err := DefaultInstallLayout(filepath.Join(root, "bin"))
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := InstallBinary(testReleaseSource(t, root, "legacy"), "v0.1.0", layout)
	if err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(layout.BinDir, "scodex")
	if err := os.Remove(launcher); err != nil {
		t.Fatal(err)
	}
	delete(ledger.Files, launcher)
	ledger.SchemaVersion, ledger.CodexReleases = 2, nil
	if err := writeLedger(layout, ledger); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ledger, err = installDownloadedRelease(ctx, source, "v0.2.0", layout)
	if err != nil {
		t.Fatal(err)
	}
	if ledger.SchemaVersion != 3 || ledger.Current != "v0.2.0" || !ledger.CodexReleases[ledger.Current] || ledger.CodexReleases[ledger.Previous] {
		t.Fatalf("candidate ledger=%+v", ledger)
	}
	if _, err := os.Stat(launcher); err != nil {
		t.Fatal(err)
	}
	// A candidate that refuses the format must not be installed by its caller.
	priorLedger, err := os.ReadFile(ledgerPath(layout))
	if err != nil {
		t.Fatal(err)
	}
	refusing := filepath.Join(root, "refusing-candidate")
	if err := os.WriteFile(refusing, []byte("#!/bin/sh\nexit 42\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installDownloadedRelease(ctx, refusing, "v0.0.1", layout); err == nil || !strings.Contains(err.Error(), "exit status 42") {
		t.Fatalf("candidate refusal=%v", err)
	}
	afterLedger, err := os.ReadFile(ledgerPath(layout))
	if err != nil || !bytes.Equal(priorLedger, afterLedger) {
		t.Fatal("refusing candidate changed ledger")
	}
	if target, err := os.Readlink(currentPath(layout)); err != nil || target != releaseDir(layout, "v0.2.0") {
		t.Fatalf("current=%s %v", target, err)
	}
}
