package stateroot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/ctrl-alt-raccoon/sclaude/internal/fssecure"
)

type Hooks struct {
	BeforeFlock func(string) error
	AfterLock   func(string) error
}

type Lock struct {
	path      string
	directory *fssecure.Directory
}

func Prepare(path string, create bool) error {
	absolute, err := absoluteCleanPath(path)
	if err != nil {
		return err
	}
	info, err := os.Lstat(absolute)
	if errors.Is(err, os.ErrNotExist) {
		if !create {
			return os.ErrNotExist
		}
		if err := os.MkdirAll(absolute, 0o700); err != nil {
			return fmt.Errorf("create state root: %w", err)
		}
		info, err = os.Lstat(absolute)
	}
	if err != nil {
		return fmt.Errorf("inspect state root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("state root must be a directory, not a symlink")
	}
	directory, err := fssecure.Open(absolute)
	if err != nil {
		return fmt.Errorf("open state root: %w", err)
	}
	defer directory.Close()
	if directory.Info().Mode() != os.ModeDir|0o700 {
		if err := directory.Chmod(0o700); err != nil {
			return fmt.Errorf("secure state root: %w", err)
		}
	}
	if err := directory.Revalidate(absolute); err != nil {
		return errors.New("state root changed while preparing admission")
	}
	return nil
}

func Acquire(path string, create bool, hooks Hooks) (*Lock, error) {
	absolute, err := absoluteCleanPath(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(absolute)
	created := false
	if errors.Is(err, os.ErrNotExist) {
		if !create {
			return nil, nil
		}
		if err := os.MkdirAll(absolute, 0o700); err != nil {
			return nil, fmt.Errorf("create state root: %w", err)
		}
		created = true
		info, err = os.Lstat(absolute)
	}
	if err != nil {
		return nil, fmt.Errorf("inspect state root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("state root must be a directory, not a symlink")
	}
	if info.Mode() != os.ModeDir|0o700 {
		if !created {
			return nil, errors.New("state root must have mode 0700")
		}
		if err := os.Chmod(absolute, 0o700); err != nil {
			return nil, fmt.Errorf("secure state root: %w", err)
		}
		info, err = os.Lstat(absolute)
		if err != nil || info.Mode() != os.ModeDir|0o700 {
			return nil, errors.New("state root must have mode 0700")
		}
	}
	directory, err := fssecure.Open(absolute)
	if err != nil {
		return nil, fmt.Errorf("open state-root admission lock: %w", err)
	}
	openedInfo := directory.Info()
	if !os.SameFile(info, openedInfo) || openedInfo.Mode() != os.ModeDir|0o700 {
		_ = directory.Close()
		return nil, errors.New("state root changed while opening")
	}
	if hooks.BeforeFlock != nil {
		if err := hooks.BeforeFlock(absolute); err != nil {
			_ = directory.Close()
			return nil, err
		}
	}
	if err := syscall.Flock(int(directory.File().Fd()), syscall.LOCK_EX); err != nil {
		_ = directory.Close()
		return nil, fmt.Errorf("acquire state-root admission lock: %w", err)
	}
	if hooks.AfterLock != nil {
		if err := hooks.AfterLock(absolute); err != nil {
			_ = syscall.Flock(int(directory.File().Fd()), syscall.LOCK_UN)
			_ = directory.Close()
			return nil, err
		}
	}
	if err := directory.Revalidate(absolute); err != nil {
		_ = syscall.Flock(int(directory.File().Fd()), syscall.LOCK_UN)
		_ = directory.Close()
		return nil, errors.New("state root changed while acquiring the admission lock")
	}
	return &Lock{path: absolute, directory: directory}, nil
}

func (lock *Lock) Path() string {
	if lock == nil {
		return ""
	}
	return lock.path
}

func (lock *Lock) Directory() *fssecure.Directory {
	if lock == nil {
		return nil
	}
	return lock.directory
}

func (lock *Lock) Revalidate() error {
	if lock == nil || lock.directory == nil {
		return errors.New("state-root admission lock is closed")
	}
	if err := lock.directory.Revalidate(lock.path); err != nil {
		return fmt.Errorf("state root changed while holding the admission lock: %w", err)
	}
	return nil
}

func (lock *Lock) Release() error {
	if lock == nil || lock.directory == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(lock.directory.File().Fd()), syscall.LOCK_UN)
	closeErr := lock.directory.Close()
	lock.directory = nil
	return errors.Join(unlockErr, closeErr)
}

func absoluteCleanPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("state root path is missing")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	if !filepath.IsAbs(absolute) || absolute == string(filepath.Separator) {
		return "", errors.New("state root path is invalid")
	}
	return absolute, nil
}
