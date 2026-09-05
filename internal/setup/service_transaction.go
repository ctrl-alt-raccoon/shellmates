package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ctrl-alt-raccoon/shellmates/internal/config"
)

type commandOutputRunner interface {
	Output(context.Context, string, ...string) ([]byte, error)
}

type preparedServiceState struct {
	manager          string
	executable       trustedExecutable
	active           bool
	scheduled        bool
	activeTouched    bool
	scheduledTouched bool
}

type brewServiceInfo struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	Running   *bool  `json:"running"`
	Scheduled *bool  `json:"scheduled"`
}

func prepareServiceState(ctx context.Context, runner CommandRunner, manager string, systemService bool, executable trustedExecutable) (preparedServiceState, error) {
	prepared := preparedServiceState{manager: manager, executable: executable}
	if systemService || manager == "docker" || manager == "none" {
		return prepared, nil
	}
	outputRunner, ok := runner.(commandOutputRunner)
	if !ok {
		return prepared, errors.New("command runner cannot inspect CLIProxyAPI service state")
	}
	executablePath, err := executable.revalidate()
	if err != nil {
		return prepared, fmt.Errorf("revalidate CLIProxyAPI service executable: %w", err)
	}

	switch manager {
	case "brew":
		output, err := outputRunner.Output(ctx, executablePath, "services", "list", "--json")
		if err != nil {
			return prepared, fmt.Errorf("inspect CLIProxyAPI Homebrew service state: %w", err)
		}
		active, scheduled, err := parseBrewServiceState(output)
		if err != nil {
			return prepared, err
		}
		if !active && scheduled {
			return prepared, errors.New("CLIProxyAPI Homebrew service is scheduled but not running; setup cannot restore that state safely")
		}
		prepared.active = active
		prepared.scheduled = scheduled
	case "systemd":
		arguments := []string{}
		scope := "system"
		if !systemService {
			arguments = append(arguments, "--user")
			scope = "user"
		}
		output, commandErr := outputRunner.Output(
			ctx,
			executablePath,
			append(arguments, "is-active", "cliproxyapi.service")...,
		)
		active, err := parseSystemdActiveState(output, commandErr, scope)
		if err != nil {
			return prepared, err
		}
		prepared.active = active

		output, commandErr = outputRunner.Output(
			ctx,
			executablePath,
			append(arguments, "is-enabled", "cliproxyapi.service")...,
		)
		scheduled, err := parseSystemdEnabledState(output, commandErr, scope)
		if err != nil {
			return prepared, err
		}
		prepared.scheduled = scheduled
	default:
		return prepared, fmt.Errorf("unsupported CLIProxyAPI service manager %q", manager)
	}
	return prepared, nil
}

func parseBrewServiceState(data []byte) (bool, bool, error) {
	var services []brewServiceInfo
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(&services); err != nil {
		return false, false, fmt.Errorf("parse CLIProxyAPI Homebrew service state: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return false, false, errors.New("parse CLIProxyAPI Homebrew service state: trailing data")
	}
	for _, service := range services {
		if service.Name != "cliproxyapi" {
			continue
		}
		var active, scheduled bool
		if service.Running != nil {
			active = *service.Running
		}
		if service.Scheduled != nil {
			scheduled = *service.Scheduled
		}
		switch service.Status {
		case "started":
			if service.Running == nil {
				active = true
			}
			if service.Scheduled == nil {
				scheduled = true
			}
		case "scheduled":
			if service.Scheduled == nil {
				scheduled = true
			}
		case "", "none", "stopped", "error", "unknown":
		default:
			return false, false, fmt.Errorf("unsupported CLIProxyAPI Homebrew service state %q", service.Status)
		}
		return active, scheduled, nil
	}
	return false, false, nil
}

func parseSystemdActiveState(output []byte, commandErr error, scope string) (bool, error) {
	state := strings.TrimSpace(string(output))
	switch state {
	case "active":
		return true, nil
	case "activating", "reloading", "inactive", "failed", "deactivating", "unknown", "not-found":
		return false, nil
	case "":
		if commandErr != nil {
			return false, fmt.Errorf("inspect CLIProxyAPI %s service active state: %w", scope, commandErr)
		}
	}
	if commandErr != nil {
		return false, fmt.Errorf("inspect CLIProxyAPI %s service active state %q: %w", scope, state, commandErr)
	}
	return false, fmt.Errorf("unsupported CLIProxyAPI %s service active state %q", scope, state)
}

func parseSystemdEnabledState(output []byte, commandErr error, scope string) (bool, error) {
	state := strings.TrimSpace(string(output))
	switch state {
	case "enabled", "enabled-runtime", "linked", "linked-runtime", "alias":
		return true, nil
	case "disabled", "static", "indirect", "generated", "transient", "masked", "masked-runtime", "not-found":
		return false, nil
	case "":
		if commandErr != nil {
			return false, fmt.Errorf("inspect CLIProxyAPI %s service enabled state: %w", scope, commandErr)
		}
	}
	if commandErr != nil {
		return false, fmt.Errorf("inspect CLIProxyAPI %s service enabled state %q: %w", scope, state, commandErr)
	}
	return false, fmt.Errorf("unsupported CLIProxyAPI %s service enabled state %q", scope, state)
}

func (prepared *preparedServiceState) markActiveTouched() {
	prepared.activeTouched = true
}

func (prepared *preparedServiceState) markScheduledTouched() {
	prepared.scheduledTouched = true
}

func serviceStateFromJournal(journal setupJournal) (preparedServiceState, error) {
	prepared := preparedServiceState{
		manager:          journal.ServiceManager,
		active:           journal.ServiceActive,
		scheduled:        journal.ServiceScheduled,
		activeTouched:    journal.ServiceActiveTouched,
		scheduledTouched: journal.ServiceScheduledTouched,
	}
	if journal.ServiceExecutable != "" {
		executable, err := recoverTrustedExecutable(journal.ServiceExecutable, journal.ServiceExecutableDigest)
		if err != nil {
			return prepared, fmt.Errorf("recover CLIProxyAPI service executable: %w", err)
		}
		prepared.executable = executable
	}
	return prepared, nil
}

func (prepared preparedServiceState) quiesce(ctx context.Context, runner CommandRunner) error {
	if !prepared.activeTouched || prepared.manager == "docker" || prepared.manager == "none" || prepared.manager == "" {
		return nil
	}
	command, err := prepared.command("stop", false)
	if err != nil {
		return err
	}
	if len(command) == 0 {
		return nil
	}
	if err := runner.Run(ctx, command[0], command[1:]...); err != nil {
		return fmt.Errorf("quiesce CLIProxyAPI service before file rollback: %w", err)
	}
	return nil
}

func (prepared preparedServiceState) restore(ctx context.Context, runner CommandRunner) error {
	var restoreErr error
	if prepared.manager == "systemd" && prepared.scheduledTouched {
		action := "disable"
		if prepared.scheduled {
			action = "enable"
		}
		command, err := prepared.command(action, false)
		if err != nil {
			restoreErr = errors.Join(restoreErr, err)
		} else if err := runner.Run(ctx, command[0], command[1:]...); err != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("restore CLIProxyAPI service enabled state: %w", err))
		}
		if restoreErr != nil {
			return restoreErr
		}
	}
	if prepared.manager == "brew" && prepared.scheduledTouched && !prepared.scheduled {
		command, err := prepared.command("stop", false)
		if err != nil {
			restoreErr = errors.Join(restoreErr, err)
		} else if err := runner.Run(ctx, command[0], command[1:]...); err != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("restore CLIProxyAPI Homebrew service scheduled state: %w", err))
		}
	}
	if prepared.activeTouched && prepared.active {
		action := "restart"
		if prepared.manager == "brew" && !prepared.scheduled {
			action = "run"
		}
		command, err := prepared.command(action, false)
		if err != nil {
			restoreErr = errors.Join(restoreErr, err)
		} else if len(command) > 0 {
			if err := runner.Run(ctx, command[0], command[1:]...); err != nil {
				restoreErr = errors.Join(restoreErr, fmt.Errorf("restore CLIProxyAPI service active state: %w", err))
			}
		}
	}
	return restoreErr
}

func (prepared preparedServiceState) command(action string, systemService bool) ([]string, error) {
	if prepared.manager == "docker" || prepared.manager == "none" {
		return nil, nil
	}
	executablePath, err := prepared.executable.revalidate()
	if err != nil {
		return nil, fmt.Errorf("revalidate CLIProxyAPI service executable: %w", err)
	}
	return serviceCommandFor(action, prepared.manager, systemService, executablePath)
}

const setupCleanupTimeout = 15 * time.Second

func newSetupCleanupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), setupCleanupTimeout)
}

func rollbackRecoveredSetupJournal(_ context.Context, journalPath string, journal setupJournal, runner CommandRunner) error {
	return rollbackRecoveredSetupJournalWithRemoval(journal, runner, func(expected setupJournal) error {
		return removeSetupJournal(journalPath, expected)
	})
}

func rollbackRecoveredSetupJournalLocked(_ context.Context, lock *setupAdmissionLock, journal setupJournal, runner CommandRunner) error {
	return rollbackRecoveredSetupJournalWithRemoval(journal, runner, lock.removeJournal)
}

func rollbackRecoveredSetupJournalWithRemoval(journal setupJournal, runner CommandRunner, removeJournal func(setupJournal) error) error {
	cleanupCtx, cancel := newSetupCleanupContext()
	defer cancel()
	serviceState, stateErr := serviceStateFromJournal(journal)
	if stateErr != nil {
		return stateErr
	}
	quiesceErr := serviceState.quiesce(cleanupCtx, runner)
	var rollbackErr error
	var restoreErr error
	if quiesceErr == nil {
		rollbackErr = rollbackSetupJournalFiles(journal)
		if rollbackErr == nil {
			restoreErr = serviceState.restore(cleanupCtx, runner)
		}
	}
	if quiesceErr == nil && rollbackErr == nil && restoreErr == nil {
		if err := validateRolledBackSetupTargets(journal); err != nil {
			rollbackErr = err
		} else if err := removeJournal(journal); err != nil {
			rollbackErr = err
		}
	}
	var recoveryErr error
	for _, err := range []error{quiesceErr, rollbackErr, restoreErr} {
		if err != nil {
			recoveryErr = errors.Join(recoveryErr, err)
		}
	}
	if journal.OAuthAttempted {
		message := "CLIProxyAPI OAuth authentication was attempted before setup was interrupted; its external state was not inspected and cannot be rolled back automatically"
		if journal.OAuthCompleted {
			message = "CLIProxyAPI OAuth authentication completed before setup was interrupted and cannot be rolled back automatically"
		}
		recoveryErr = errors.Join(recoveryErr, errors.New(message))
	}
	return recoveryErr
}

func finishSetupWorkflow(ctx context.Context, transaction *setupTransaction, service preparedServiceState, runner CommandRunner, runtimeIndex int, runtimeConfig config.Runtime, oauthCompleted bool) error {
	if err := transaction.applyRuntime(runtimeIndex, runtimeConfig); err != nil {
		return abortSetupWorkflow(ctx, transaction, service, runner, err, oauthCompleted)
	}
	if err := completeSetupWorkflowStage(setupStageRuntime); err != nil {
		return abortSetupWorkflow(ctx, transaction, service, runner, err, oauthCompleted)
	}
	if err := transaction.commit(); err != nil {
		journal, loadErr := transaction.loadJournal()
		if loadErr == nil && journal.Committed {
			return transaction.reconcileCommitError(err)
		}
		if errors.Is(loadErr, os.ErrNotExist) {
			if reconcileErr := transaction.reconcileMissingJournal(); reconcileErr != nil {
				return errors.Join(err, fmt.Errorf("reconcile missing setup transaction journal: %w", reconcileErr))
			}
			return nil
		}
		if loadErr != nil {
			return errors.Join(err, fmt.Errorf("inspect setup transaction after commit failure: %w", loadErr))
		}
		return abortSetupWorkflow(ctx, transaction, service, runner, err, oauthCompleted)
	}
	return nil
}

func abortSetupWorkflow(_ context.Context, transaction *setupTransaction, service preparedServiceState, runner CommandRunner, primary error, oauthCompleted bool) error {
	cleanupCtx, cancel := newSetupCleanupContext()
	defer cancel()
	result := primary
	quiesceErr := service.quiesce(cleanupCtx, runner)
	if quiesceErr != nil {
		result = errors.Join(result, quiesceErr)
	}
	var rollbackErr error
	var restoreErr error
	if quiesceErr == nil {
		rollbackErr = transaction.rollbackFiles()
		if rollbackErr != nil {
			result = errors.Join(result, fmt.Errorf("roll back setup transaction files: %w", rollbackErr))
		} else {
			restoreErr = service.restore(cleanupCtx, runner)
			if restoreErr != nil {
				result = errors.Join(result, restoreErr)
			}
		}
	}
	if quiesceErr == nil && rollbackErr == nil && restoreErr == nil {
		if finishErr := transaction.finishAbort(); finishErr != nil {
			result = errors.Join(result, fmt.Errorf("finish setup transaction rollback: %w", finishErr))
		}
	}
	if oauthCompleted {
		result = errors.Join(result, errors.New("CLIProxyAPI OAuth authentication completed before setup failed and cannot be rolled back automatically"))
	}
	return result
}
