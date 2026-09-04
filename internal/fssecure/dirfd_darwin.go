//go:build darwin

package fssecure

import (
	"syscall"
	"unsafe"
)

const (
	sysOpenat      = 463
	sysRenameat    = 465
	sysLinkat      = 471
	sysUnlinkat    = 472
	sysMkdirat     = 475
	sysRenameatxNP = 488

	renameSwap = 0x00000002
	renameExcl = 0x00000004
)

func rawMkdirat(directory int, name string, mode uint32) error {
	namePointer, err := syscall.BytePtrFromString(name)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall(sysMkdirat, uintptr(directory), uintptr(unsafe.Pointer(namePointer)), uintptr(mode))
	if errno != 0 {
		return errno
	}
	return nil
}

func rawOpenat(directory int, name string, flags int, mode uint32) (int, error) {
	namePointer, err := syscall.BytePtrFromString(name)
	if err != nil {
		return -1, err
	}
	result, _, errno := syscall.Syscall6(
		sysOpenat,
		uintptr(directory),
		uintptr(unsafe.Pointer(namePointer)),
		uintptr(flags),
		uintptr(mode),
		0,
		0,
	)
	if errno != 0 {
		return -1, errno
	}
	return int(result), nil
}

func rawRenameat(oldDirectory int, oldName string, newDirectory int, newName string) error {
	oldPointer, err := syscall.BytePtrFromString(oldName)
	if err != nil {
		return err
	}
	newPointer, err := syscall.BytePtrFromString(newName)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(
		sysRenameat,
		uintptr(oldDirectory),
		uintptr(unsafe.Pointer(oldPointer)),
		uintptr(newDirectory),
		uintptr(unsafe.Pointer(newPointer)),
		0,
		0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}

func rawRenameNoReplaceAt(oldDirectory int, oldName string, newDirectory int, newName string) error {
	return rawRenameatxNP(oldDirectory, oldName, newDirectory, newName, renameExcl)
}

func rawExchangeAt(firstDirectory int, firstName string, secondDirectory int, secondName string) error {
	return rawRenameatxNP(firstDirectory, firstName, secondDirectory, secondName, renameSwap)
}

func rawRenameatxNP(oldDirectory int, oldName string, newDirectory int, newName string, flags int) error {
	oldPointer, err := syscall.BytePtrFromString(oldName)
	if err != nil {
		return err
	}
	newPointer, err := syscall.BytePtrFromString(newName)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(
		sysRenameatxNP,
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
		sysLinkat,
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
		sysUnlinkat,
		uintptr(directory),
		uintptr(unsafe.Pointer(namePointer)),
		uintptr(flags),
	)
	if errno != 0 {
		return errno
	}
	return nil
}
