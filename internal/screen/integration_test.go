package screen

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRealScreenLifecycle(t *testing.T) {
	if os.Getenv("SCLAUDE_SCREEN_INTEGRATION") != "1" {
		t.Skip("opt in with SCLAUDE_SCREEN_INTEGRATION=1; uses a disposable socket namespace")
	}
	path, err := exec.LookPath("screen")
	if err != nil {
		t.Fatal(err)
	}
	// Keep Unix-domain socket paths short even when the caller's TMPDIR is long.
	socketDir, err := os.MkdirTemp("/tmp", "sc-int-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(socketDir); err != nil {
			t.Error(err)
		}
	})
	t.Setenv("SCREENDIR", socketDir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("STY", "")
	client := Client{Path: path}
	name := "sclaude-integration"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	marker := filepath.Join(t.TempDir(), "probe-pid")
	if err := client.startDetached(ctx, name, `printf '%s\n' "$$" > "$1"; `+detachedProbeCommand, "probe", marker); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = client.Stop(cleanup, name)
	})
	wait := func(present bool) {
		t.Helper()
		for {
			sockets, err := client.List(ctx)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, socket := range sockets {
				if socket.Name == name && socket.PID > 0 {
					found = true
				}
			}
			if found == present {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
	wait(true)
	var pid int
	for {
		data, err := os.ReadFile(marker)
		if err == nil {
			pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil || pid <= 0 {
				t.Fatalf("probe pid=%q %v", data, err)
			}
			break
		}
		if ctx.Err() != nil {
			t.Fatal("probe did not report readiness")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := client.Stop(ctx, name); err != nil {
		t.Fatal(err)
	}
	wait(false)
	for !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		if ctx.Err() != nil {
			t.Fatal("health-check process survived socket removal")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
