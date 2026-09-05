package app

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

const backendStopGrace = 2 * time.Second

func runnerSignals() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, 8)
	// Ctrl-C already goes to the foreground backend, too. Keep the runner alive
	// to supervise it; an interrupt used inside a TUI is not a manager stop.
	signal.Notify(ch, syscall.SIGHUP, syscall.SIGTERM, os.Interrupt)
	return ch, func() { signal.Stop(ch) }
}

// waitManagedBackend leaves the foreground process group and terminal unchanged.
// It signals only the Process returned by Start, whose Signal/Wait synchronization
// protects against PID reuse on Linux and Darwin. Launchers must exec their real
// backend or forward signals; deliberately daemonized jobs are not adopted.
func waitManagedBackend(ctx context.Context, cmd *exec.Cmd, signals <-chan os.Signal, requested func() (bool, error), grace time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var timer *time.Timer
	var escalation <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	stopping := false
	requestStop := func() {
		if stopping {
			return
		}
		stopping = true
		_ = cmd.Process.Signal(syscall.SIGTERM)
		timer = time.NewTimer(grace)
		escalation = timer.C
	}
	ctxDone := ctx.Done()
	for {
		select {
		case err := <-done:
			return err
		case sig := <-signals:
			if sig != os.Interrupt {
				requestStop()
			}
		case <-ctxDone:
			ctxDone = nil
			requestStop()
		case <-ticker.C:
			if !stopping {
				stop, err := requested()
				// Losing access to the stop channel must not leave an unsupervised
				// backend running. The durable acknowledgement may also fail closed.
				if stop || err != nil {
					requestStop()
				}
			}
		case <-escalation:
			escalation = nil
			_ = cmd.Process.Kill()
			// Wait must actually finish before an acknowledgement is persisted.
		}
	}
}
