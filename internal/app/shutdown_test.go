package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestShutdownBackendProcess(t *testing.T) {
	mode := os.Getenv("SCLAUDE_SHUTDOWN_FIXTURE")
	if mode == "" {
		return
	}
	if mode == "ignore" {
		signal.Ignore(syscall.SIGTERM, syscall.SIGHUP)
	}
	if err := os.WriteFile(os.Getenv("SCLAUDE_SHUTDOWN_READY"), []byte("ready"), 0o600); err != nil {
		os.Exit(90)
	}
	for {
		time.Sleep(time.Second)
	}
}

func TestManagedBackendShutdownTriggersAndEscalation(t *testing.T) {
	for _, trigger := range []string{"intent", "context", "hangup", "term", "storage-error", "ignore"} {
		t.Run(trigger, func(t *testing.T) {
			ready := filepath.Join(t.TempDir(), "ready")
			mode := "normal"
			if trigger == "ignore" {
				mode = "ignore"
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestShutdownBackendProcess$")
			cmd.Env = append(os.Environ(), "SCLAUDE_SHUTDOWN_FIXTURE="+mode, "SCLAUDE_SHUTDOWN_READY="+ready)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill() }) // the actual Process handle, not a stored PID
			deadline := time.Now().Add(10 * time.Second)
			for {
				if _, err := os.Stat(ready); err == nil {
					break
				}
				if time.Now().After(deadline) {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					t.Fatal("fixture not ready")
				}
				time.Sleep(10 * time.Millisecond)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			signals := make(chan os.Signal, 1)
			if trigger == "context" {
				cancel()
			}
			if trigger == "hangup" {
				signals <- syscall.SIGHUP
			}
			if trigger == "term" {
				signals <- syscall.SIGTERM
			}
			err := waitManagedBackend(ctx, cmd, signals, func() (bool, error) {
				if trigger == "storage-error" {
					return false, errors.New("injected store failure")
				}
				return trigger == "intent" || trigger == "ignore", nil
			}, 50*time.Millisecond)
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatalf("exit=%v", err)
			}
			want := syscall.SIGTERM
			if trigger == "ignore" {
				want = syscall.SIGKILL
			}
			if status := exit.Sys().(syscall.WaitStatus); !status.Signaled() || status.Signal() != want {
				t.Fatalf("status=%v want=%v", status, want)
			}
			if err := cmd.Process.Signal(syscall.SIGTERM); !errors.Is(err, os.ErrProcessDone) {
				t.Fatalf("finished handle can still signal: %v", err)
			}
		})
	}
}

func TestManagedBackendInterruptDoesNotBecomeStop(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "sleep 0.2; exit 7")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	signals := make(chan os.Signal, 1)
	signals <- os.Interrupt
	err := waitManagedBackend(context.Background(), cmd, signals, func() (bool, error) { return false, nil }, time.Millisecond)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("interrupt killed runner/backend: %v", err)
	}
}
