package setup

import (
	"bytes"
	"errors"
	"fmt"
	"os"
)

type preparedSetupShell struct {
	plans        []shellPlan
	shellIndices []int
	layout       *InstallLayout
	ledgerBefore []byte
	ledgerAfter  []byte
	ledgerIndex  int
}

var beforeApplySetupShell func() error
var beforeApplySetupLedger func() error

func prepareSetupShell(opts SetupOptions) (preparedSetupShell, error) {
	prepared := preparedSetupShell{ledgerIndex: -1}
	if opts.BinDir == "" || opts.NoModifyPath || opts.DryRun {
		return prepared, nil
	}

	plans, err := prepareShellPlans(opts.BinDir, opts)
	if err != nil {
		return prepared, err
	}
	prepared.plans = plans
	prepared.shellIndices = make([]int, len(plans))
	for index := range prepared.shellIndices {
		prepared.shellIndices[index] = -1
	}

	layout, err := InstalledLayout()
	if errors.Is(err, os.ErrNotExist) {
		return prepared, nil
	}
	if err != nil {
		return prepared, err
	}

	var ledgerBefore, ledgerAfter []byte
	if err := withInstallLock(layout, func() error {
		if err := recoverInstallJournal(layout); err != nil {
			return err
		}
		if _, err := recoverUninstallJournal(layout); err != nil {
			return err
		}
		currentLayout, err := InstalledLayout()
		if err != nil {
			return err
		}
		if currentLayout != layout {
			return errors.New("install layout changed while preparing shell ownership")
		}
		ledgerBefore, _, err = readStableRegularFile(ledgerPath(layout))
		if err != nil {
			return err
		}
		ledger, exists, _, err := loadLedgerForLayout(layout)
		if err != nil {
			return err
		}
		if !exists {
			return os.ErrNotExist
		}
		if err := preflightLedger(layout, ledger); err != nil {
			return err
		}
		edits := shellEdits(plans)
		ledger, err = updateShellOwnership(ledger, edits)
		if err != nil {
			return err
		}
		ledgerAfter, err = marshalLedger(ledger)
		return err
	}); err != nil {
		return prepared, err
	}

	prepared.layout = &layout
	prepared.ledgerBefore = ledgerBefore
	prepared.ledgerAfter = ledgerAfter
	return prepared, nil
}

func (prepared *preparedSetupShell) addFiles(transaction *setupTransaction) error {
	if len(prepared.plans) == 0 {
		return nil
	}
	if prepared.layout != nil {
		if err := transaction.setInstallLock(lockPath(*prepared.layout)); err != nil {
			return err
		}
	}
	for index, plan := range prepared.plans {
		if err := revalidateShellPlan(plan); err != nil {
			return err
		}
		if !plan.changed {
			continue
		}
		fileIndex, err := transaction.addFile(plan.target, plan.replacement, plan.mode)
		if err != nil {
			return err
		}
		prepared.shellIndices[index] = fileIndex
	}
	if prepared.layout == nil {
		return nil
	}
	ledgerIndex, err := transaction.addFile(ledgerPath(*prepared.layout), prepared.ledgerAfter, 0o600)
	if err != nil {
		return err
	}
	ledgerPrepared := transaction.files[ledgerIndex]
	if !ledgerPrepared.entry.Existed || !bytes.Equal(ledgerPrepared.before, prepared.ledgerBefore) {
		return errors.New("install ledger changed after shell ownership preparation")
	}
	prepared.ledgerIndex = ledgerIndex
	return nil
}

func (prepared preparedSetupShell) apply(transaction *setupTransaction) error {
	if len(prepared.plans) == 0 {
		return nil
	}
	if beforeApplySetupShell != nil {
		if err := beforeApplySetupShell(); err != nil {
			return err
		}
	}
	apply := func() error {
		if prepared.layout != nil {
			if err := recoverInstallJournal(*prepared.layout); err != nil {
				return err
			}
			if _, err := recoverUninstallJournal(*prepared.layout); err != nil {
				return err
			}
			currentLayout, err := InstalledLayout()
			if err != nil {
				return err
			}
			if currentLayout != *prepared.layout {
				return errors.New("install layout changed after shell ownership preparation")
			}
			currentLedger, _, err := readStableRegularFile(ledgerPath(*prepared.layout))
			if err != nil {
				return err
			}
			if !bytes.Equal(currentLedger, prepared.ledgerBefore) {
				return errors.New("install ledger changed after shell ownership preparation")
			}
			ledger, exists, _, err := loadLedgerForLayout(*prepared.layout)
			if err != nil {
				return err
			}
			if !exists {
				return os.ErrNotExist
			}
			if err := preflightLedger(*prepared.layout, ledger); err != nil {
				return err
			}
			ledger, err = updateShellOwnership(ledger, shellEdits(prepared.plans))
			if err != nil {
				return err
			}
			currentAfter, err := marshalLedger(ledger)
			if err != nil {
				return err
			}
			if !bytes.Equal(currentAfter, prepared.ledgerAfter) {
				return errors.New("install ledger ownership changed after preparation")
			}
		}

		for index, plan := range prepared.plans {
			if err := revalidateShellPlan(plan); err != nil {
				return err
			}
			fileIndex := prepared.shellIndices[index]
			if fileIndex >= 0 {
				if err := transaction.applyFile(fileIndex); err != nil {
					return fmt.Errorf("apply shell profile %s: %w", plan.path, err)
				}
			}
		}
		if prepared.ledgerIndex >= 0 {
			if beforeApplySetupLedger != nil {
				if err := beforeApplySetupLedger(); err != nil {
					return fmt.Errorf("apply install ledger shell ownership: %w", err)
				}
			}
			if err := transaction.applyFile(prepared.ledgerIndex); err != nil {
				return fmt.Errorf("apply install ledger shell ownership: %w", err)
			}
		}
		return nil
	}
	if prepared.layout != nil {
		return withInstallLock(*prepared.layout, apply)
	}
	return apply()
}

func shellEdits(plans []shellPlan) []ShellEdit {
	edits := make([]ShellEdit, len(plans))
	for index, plan := range plans {
		edits[index] = plan.edit()
	}
	return edits
}
