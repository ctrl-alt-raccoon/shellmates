//go:build !darwin && !linux

package fssecure

import (
	"errors"
	"os"
	"path/filepath"
)

const nonBlockingOpenFlag = 0
const directoryOpenFlag = 0

func mkdirAt(directory *os.File, name string, mode os.FileMode) error {
	return errors.New("descriptor-relative directory creation is unsupported on this platform")
}

func openDirectoryAtPath(path string) (*os.File, error) {
	return os.Open(path)
}

func openFileAt(directory *os.File, name string, flags int, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(filepath.Join(directory.Name(), name), flags, mode)
}

func renameAt(directory *os.File, oldName, newName string) error {
	return os.Rename(filepath.Join(directory.Name(), oldName), filepath.Join(directory.Name(), newName))
}

func renameNoReplaceAt(directory *os.File, oldName, newName string) error {
	oldPath := filepath.Join(directory.Name(), oldName)
	newPath := filepath.Join(directory.Name(), newName)
	if err := os.Link(oldPath, newPath); err != nil {
		return err
	}
	if err := os.Remove(oldPath); err != nil {
		_ = os.Remove(newPath)
		return err
	}
	return nil
}

func exchangeAt(directory *os.File, firstName, secondName string) error {
	return errors.New("atomic directory entry exchange is unsupported on this platform")
}

func linkAt(directory *os.File, oldName, newName string) error {
	return os.Link(filepath.Join(directory.Name(), oldName), filepath.Join(directory.Name(), newName))
}

func unlinkAt(directory *os.File, name string) error {
	err := os.Remove(filepath.Join(directory.Name(), name))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
