package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/ctrl-alt-raccoon/shellmates/internal/fssecure"
)

func (s Store) open(create bool) (opened Store, returnErr error) {
	if !filepath.IsAbs(s.Root) || filepath.Clean(s.Root) == string(filepath.Separator) ||
		filepath.Clean(s.SessionsDir) != filepath.Join(s.Root, "sessions") ||
		filepath.Clean(s.LaunchDir) != filepath.Join(s.Root, "launch") ||
		filepath.Clean(s.LockPath) != filepath.Join(s.Root, "lock") {
		return Store{}, errors.New("session store paths must be confined to an absolute state root")
	}
	if create {
		if err := os.MkdirAll(s.Root, 0o700); err != nil {
			return Store{}, err
		}
	}
	dirs := &storeDirectories{}
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, dirs.close())
		}
	}()
	var err error
	dirs.root, err = fssecure.Open(s.Root)
	if err != nil {
		return Store{}, err
	}
	if err := secureDirectory(dirs.root, create); err != nil {
		return Store{}, err
	}
	dirs.sessions, err = dirs.root.OpenChild("sessions", create, 0o700)
	if err != nil {
		return Store{}, err
	}
	if err := secureDirectory(dirs.sessions, create); err != nil {
		return Store{}, err
	}
	dirs.launch, err = dirs.root.OpenChild("launch", create, 0o700)
	if err != nil {
		return Store{}, err
	}
	if err := secureDirectory(dirs.launch, create); err != nil {
		return Store{}, err
	}
	s.dirs = dirs
	return s, s.revalidate()
}

func secureDirectory(directory *fssecure.Directory, repairMode bool) error {
	stat, ok := directory.Info().Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("session directory must be owned by the current user")
	}
	if directory.Info().Mode() != os.ModeDir|0o700 {
		if !repairMode {
			return errors.New("session directory must have mode 0700")
		}
		return directory.Chmod(0o700)
	}
	return nil
}

func ownedRegular(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return errors.New("session file must be an owned regular file with one link")
	}
	return nil
}

func privateFile(info os.FileInfo) error {
	if err := ownedRegular(info); err != nil {
		return err
	}
	if info.Mode() != 0o600 {
		return errors.New("session file must have mode 0600")
	}
	return nil
}

func (dirs *storeDirectories) close() error {
	return errors.Join(dirs.launch.Close(), dirs.sessions.Close(), dirs.root.Close())
}

func (s Store) revalidate() error {
	return errors.Join(
		s.dirs.root.Revalidate(s.Root),
		s.dirs.sessions.Revalidate(s.SessionsDir),
		s.dirs.launch.Revalidate(s.LaunchDir),
	)
}

// Every writer and consumer holds the same lock, including older releases.
// Once that lock is acquired, reserved temporary entries cannot have a live
// writer. Recover both old .consume-* claims and interrupted atomic writes.
func (s Store) cleanupTemporaries() error {
	for _, name := range []string{"sessions", "launch"} {
		if err := s.cleanupDirectoryTemporaries(name); err != nil {
			return err
		}
	}
	return nil
}

func (s Store) cleanupDirectoryTemporaries(name string) error {
	directory, err := s.dirs.root.OpenChild(name, false, 0)
	if err != nil {
		return err
	}
	defer directory.Close()
	expected := s.dirs.sessions
	if name == "launch" {
		expected = s.dirs.launch
	}
	if !os.SameFile(directory.Info(), expected.Info()) {
		return errors.New("session directory changed before temporary-file recovery")
	}
	entries, err := directory.File().ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".sclaude-") &&
			!(name == "launch" && strings.HasPrefix(entry.Name(), ".consume-")) {
			continue
		}
		if err := removeRegular(directory, entry.Name()); err != nil {
			return fmt.Errorf("recover abandoned session file: %w", err)
		}
	}
	return nil
}
