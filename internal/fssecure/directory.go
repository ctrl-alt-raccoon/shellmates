package fssecure

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type Directory struct {
	path string
	file *os.File
	info os.FileInfo
}

var syncDirectoryFile = func(file *os.File) error {
	return file.Sync()
}

func Open(path string) (*Directory, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return nil, fmt.Errorf("%s must be a non-symlink directory", path)
	}
	file, err := openDirectoryAtPath(path)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !os.SameFile(before, opened) || !opened.IsDir() {
		_ = file.Close()
		return nil, fmt.Errorf("%s changed while opening", path)
	}
	directory := &Directory{path: path, file: file, info: opened}
	if err := directory.Revalidate(path); err != nil {
		_ = file.Close()
		return nil, err
	}
	return directory, nil
}

func (directory *Directory) Close() error {
	if directory == nil || directory.file == nil {
		return nil
	}
	err := directory.file.Close()
	directory.file = nil
	return err
}

func (directory *Directory) File() *os.File {
	if directory == nil {
		return nil
	}
	return directory.file
}

func (directory *Directory) Info() os.FileInfo {
	if directory == nil {
		return nil
	}
	return directory.info
}

func (directory *Directory) Sync() error {
	if directory == nil || directory.file == nil {
		return errors.New("directory is closed")
	}
	return syncDirectoryFile(directory.file)
}

func (directory *Directory) Chmod(mode os.FileMode) error {
	if directory == nil || directory.file == nil || directory.info == nil {
		return errors.New("directory is closed")
	}
	if mode != mode.Perm() {
		return errors.New("directory mode must contain permissions only")
	}
	if err := directory.file.Chmod(mode); err != nil {
		return err
	}
	current, err := directory.file.Stat()
	if err != nil {
		return err
	}
	if !current.IsDir() ||
		!os.SameFile(directory.info, current) ||
		current.Mode() != os.ModeDir|mode {
		return errors.New("opened directory changed while setting permissions")
	}
	directory.info = current
	return nil
}

func (directory *Directory) Revalidate(path string) error {
	if directory == nil || directory.file == nil || directory.info == nil {
		return errors.New("directory is closed")
	}
	current, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if current.Mode()&os.ModeSymlink != 0 ||
		!current.IsDir() ||
		!os.SameFile(directory.info, current) ||
		current.Mode() != directory.info.Mode() {
		return fmt.Errorf("%s changed after opening", path)
	}
	return nil
}

func (directory *Directory) OpenFile(name string, flags int, mode os.FileMode) (*os.File, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	if directory == nil || directory.file == nil {
		return nil, errors.New("directory is closed")
	}
	return openFileAt(directory.file, name, flags, mode)
}

func (directory *Directory) ReadRegular(name string) ([]byte, os.FileInfo, error) {
	file, err := directory.OpenFile(
		name,
		os.O_RDONLY|nonBlockingOpenFlag,
		0,
	)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, nil, errors.New("target is not a regular non-symlink file")
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, nil, err
	}
	after, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !sameStableFileState(before, after) {
		return nil, nil, errors.New("target changed while reading")
	}
	return data, after, nil
}

func (directory *Directory) ReadOptionalRegular(name string) ([]byte, os.FileInfo, error) {
	data, info, err := directory.ReadRegular(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	return data, info, err
}

func (directory *Directory) InspectRegular(name string) (os.FileInfo, error) {
	file, err := directory.OpenFile(
		name,
		os.O_RDONLY|nonBlockingOpenFlag,
		0,
	)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("target is not a regular non-symlink file")
	}
	return info, nil
}

func (directory *Directory) WriteAtomic(name string, data []byte, mode os.FileMode) error {
	return directory.WriteAtomicChecked(name, data, mode, nil)
}

func (directory *Directory) WriteNew(name string, data []byte, mode os.FileMode) error {
	return directory.WriteNewVerified(name, data, mode, nil, nil)
}

func (directory *Directory) WriteNewVerified(name string, data []byte, mode os.FileMode, beforePublish func(os.FileInfo) error, afterPublish func(os.FileInfo) error) (returnErr error) {
	if err := validateName(name); err != nil {
		return err
	}
	temporaryName, temporary, err := directory.createPreparedFile(data, mode)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, temporary.Close())
	}()
	publishedInfo, err := temporary.Stat()
	if err != nil {
		return err
	}
	if beforePublish != nil {
		if err := beforePublish(publishedInfo); err != nil {
			return err
		}
	}
	if err := directory.RenameNoReplace(temporaryName, name); err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		return err
	}
	currentInfo, err := directory.InspectRegular(name)
	if err != nil {
		return err
	}
	if currentInfo == nil || !os.SameFile(publishedInfo, currentInfo) {
		return errors.New("target changed during new-file publication")
	}
	if afterPublish != nil {
		if err := afterPublish(publishedInfo); err != nil {
			return err
		}
	}
	currentInfo, err = directory.InspectRegular(name)
	if err != nil || currentInfo == nil || !os.SameFile(publishedInfo, currentInfo) {
		return errors.Join(
			errors.New("target changed after new-file publication"),
			err,
		)
	}
	return nil
}

func (directory *Directory) createPreparedFile(data []byte, mode os.FileMode) (string, *os.File, error) {
	token, err := randomToken()
	if err != nil {
		return "", nil, err
	}
	name := ".sclaude-" + token
	file, err := directory.OpenFile(
		name,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		mode,
	)
	if err != nil {
		return "", nil, err
	}
	if err := file.Chmod(mode); err != nil {
		return name, nil, errors.Join(err, file.Close())
	}
	if _, err := file.Write(data); err != nil {
		return name, nil, errors.Join(err, file.Close())
	}
	if err := file.Sync(); err != nil {
		return name, nil, errors.Join(err, file.Close())
	}
	return name, file, nil
}

func (directory *Directory) WriteAtomicVerified(name string, data []byte, mode os.FileMode, beforePublish func() error, afterPublish func(os.FileInfo) error) error {
	return directory.writeAtomic(name, data, mode, beforePublish, nil, afterPublish)
}

func (directory *Directory) WriteAtomicChecked(name string, data []byte, mode os.FileMode, beforePublish func() error) error {
	return directory.writeAtomic(name, data, mode, beforePublish, nil, nil)
}

func (directory *Directory) writeAtomic(name string, data []byte, mode os.FileMode, beforePublish, beforeMutation func() error, afterPublish func(os.FileInfo) error) (returnErr error) {
	if err := validateName(name); err != nil {
		return err
	}
	temporaryName, temporary, err := directory.createPreparedFile(data, mode)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, temporary.Close())
	}()
	temporaryInfo, err := temporary.Stat()
	if err != nil {
		return err
	}
	targetInfo, err := directory.InspectRegular(name)
	if err != nil {
		return err
	}
	if beforePublish != nil {
		if err := beforePublish(); err != nil {
			return err
		}
	}
	if beforeMutation != nil {
		if err := beforeMutation(); err != nil {
			return err
		}
	}
	if targetInfo == nil {
		if err := renameNoReplaceAt(directory.file, temporaryName, name); err != nil {
			return err
		}
	} else {
		if err := exchangeAt(directory.file, temporaryName, name); err != nil {
			return err
		}
		currentDisplaced, displacedErr := directory.InspectRegular(temporaryName)
		currentPublished, publishedErr := directory.InspectRegular(name)
		if displacedErr != nil ||
			publishedErr != nil ||
			currentDisplaced == nil ||
			!os.SameFile(targetInfo, currentDisplaced) ||
			currentPublished == nil ||
			!os.SameFile(temporaryInfo, currentPublished) {
			return errors.Join(
				errors.New("target or prepared file changed during atomic publication; ambiguous entries were retained"),
				displacedErr,
				publishedErr,
			)
		}
	}
	if afterPublish != nil {
		if err := afterPublish(temporaryInfo); err != nil {
			return err
		}
	}
	current, err := directory.InspectRegular(name)
	if err != nil || current == nil || !os.SameFile(temporaryInfo, current) {
		return errors.Join(
			errors.New("target changed after atomic publication"),
			err,
		)
	}
	if err := directory.Sync(); err != nil {
		return err
	}
	return nil
}

func (directory *Directory) Rename(oldName, newName string) error {
	if err := validateName(oldName); err != nil {
		return err
	}
	if err := validateName(newName); err != nil {
		return err
	}
	if directory == nil || directory.file == nil {
		return errors.New("directory is closed")
	}
	return renameAt(directory.file, oldName, newName)
}

func (directory *Directory) RenameNoReplace(oldName, newName string) error {
	if err := validateName(oldName); err != nil {
		return err
	}
	if err := validateName(newName); err != nil {
		return err
	}
	if directory == nil || directory.file == nil {
		return errors.New("directory is closed")
	}
	return renameNoReplaceAt(directory.file, oldName, newName)
}

func (directory *Directory) Exchange(firstName, secondName string) error {
	if err := validateName(firstName); err != nil {
		return err
	}
	if err := validateName(secondName); err != nil {
		return err
	}
	if directory == nil || directory.file == nil {
		return errors.New("directory is closed")
	}
	return exchangeAt(directory.file, firstName, secondName)
}

func (directory *Directory) Link(oldName, newName string) error {
	if err := validateName(oldName); err != nil {
		return err
	}
	if err := validateName(newName); err != nil {
		return err
	}
	if directory == nil || directory.file == nil {
		return errors.New("directory is closed")
	}
	return linkAt(directory.file, oldName, newName)
}

func (directory *Directory) Remove(name string) error {
	if err := directory.Unlink(name); err != nil {
		return err
	}
	return directory.Sync()
}

func (directory *Directory) RemoveRegular(name string, expected os.FileInfo, beforeRemove func() error) error {
	_, err := directory.RemoveRegularOutcome(name, expected, beforeRemove)
	return err
}

func (directory *Directory) RemoveRegularOutcome(name string, expected os.FileInfo, beforeRemove func() error) (bool, error) {
	if err := validateName(name); err != nil {
		return false, err
	}
	if expected == nil {
		return false, errors.New("expected file identity is missing")
	}
	current, err := directory.InspectRegular(name)
	if err != nil {
		return false, err
	}
	if current == nil || !os.SameFile(expected, current) {
		return false, errors.New("target changed before removal")
	}
	if beforeRemove != nil {
		if err := beforeRemove(); err != nil {
			return false, err
		}
	}
	current, err = directory.InspectRegular(name)
	if err != nil {
		return false, err
	}
	if current == nil || !os.SameFile(expected, current) {
		return false, errors.New("target changed before removal")
	}
	if err := directory.Unlink(name); err != nil {
		return false, err
	}
	if err := directory.Sync(); err != nil {
		return true, err
	}
	return true, nil
}

func (directory *Directory) Unlink(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	if directory == nil || directory.file == nil {
		return errors.New("directory is closed")
	}
	return unlinkAt(directory.file, name)
}

func validateName(name string) error {
	if name == "" ||
		name == "." ||
		name == ".." ||
		filepath.Base(name) != name {
		return errors.New("directory entry name is invalid")
	}
	return nil
}

func randomToken() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
