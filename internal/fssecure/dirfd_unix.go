//go:build darwin || linux

package fssecure

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

const nonBlockingOpenFlag = syscall.O_NONBLOCK

func openDirectoryAtPath(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func openFileAt(directory *os.File, name string, flags int, mode os.FileMode) (*os.File, error) {
	fd, err := rawOpenat(
		int(directory.Fd()),
		name,
		flags|syscall.O_CLOEXEC|syscall.O_NOFOLLOW,
		uint32(mode.Perm()),
	)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), filepath.Join(directory.Name(), name)), nil
}

func renameAt(directory *os.File, oldName, newName string) error {
	return rawRenameat(int(directory.Fd()), oldName, int(directory.Fd()), newName)
}

func renameNoReplaceAt(directory *os.File, oldName, newName string) error {
	return rawRenameNoReplaceAt(int(directory.Fd()), oldName, int(directory.Fd()), newName)
}

func exchangeAt(directory *os.File, firstName, secondName string) error {
	return rawExchangeAt(int(directory.Fd()), firstName, int(directory.Fd()), secondName)
}

func linkAt(directory *os.File, oldName, newName string) error {
	return rawLinkat(int(directory.Fd()), oldName, int(directory.Fd()), newName, 0)
}

func unlinkAt(directory *os.File, name string) error {
	err := rawUnlinkat(int(directory.Fd()), name, 0)
	if errors.Is(err, syscall.ENOENT) {
		return nil
	}
	return err
}
