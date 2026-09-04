//go:build linux

package fssecure

import (
	"syscall"
	"unsafe"
)

func rawOpenat(directory int, name string, flags int, mode uint32) (int, error) {
	return syscall.Openat(directory, name, flags, mode)
}

func rawMkdirat(directory int, name string, mode uint32) error {
	return syscall.Mkdirat(directory, name, mode)
}

func rawRenameat(oldDirectory int, oldName string, newDirectory int, newName string) error {
	return syscall.Renameat(oldDirectory, oldName, newDirectory, newName)
}

const (
	renameNoReplace = 1
	renameExchange  = 2
)

func rawRenameNoReplaceAt(oldDirectory int, oldName string, newDirectory int, newName string) error {
	return rawRenameat2(oldDirectory, oldName, newDirectory, newName, renameNoReplace)
}

func rawExchangeAt(firstDirectory int, firstName string, secondDirectory int, secondName string) error {
	return rawRenameat2(firstDirectory, firstName, secondDirectory, secondName, renameExchange)
}

func rawRenameat2(oldDirectory int, oldName string, newDirectory int, newName string, flags int) error {
	oldPointer, err := syscall.BytePtrFromString(oldName)
	if err != nil {
		return err
	}
	newPointer, err := syscall.BytePtrFromString(newName)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(
		renameat2Trap,
		uintptr(oldDirectory),
		uintptr(unsafe.Pointer(oldPointer)),
		uintptr(newDirectory),
		uintptr(unsafe.Pointer(newPointer)),
		uintptr(flags),
		0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}

func rawLinkat(oldDirectory int, oldName string, newDirectory int, newName string, flags int) error {
	oldPointer, err := syscall.BytePtrFromString(oldName)
	if err != nil {
		return err
	}
	newPointer, err := syscall.BytePtrFromString(newName)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(
		syscall.SYS_LINKAT,
		uintptr(oldDirectory),
		uintptr(unsafe.Pointer(oldPointer)),
		uintptr(newDirectory),
		uintptr(unsafe.Pointer(newPointer)),
		uintptr(flags),
		0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}

func rawUnlinkat(directory int, name string, flags int) error {
	namePointer, err := syscall.BytePtrFromString(name)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall(
		syscall.SYS_UNLINKAT,
		uintptr(directory),
		uintptr(unsafe.Pointer(namePointer)),
		uintptr(flags),
	)
	if errno != 0 {
		return errno
	}
	return nil
}
