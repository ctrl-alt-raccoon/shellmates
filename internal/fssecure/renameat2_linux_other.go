//go:build linux && (loong64 || mips64 || mips64le || riscv64 || s390x)

package fssecure

import "syscall"

const renameat2Trap = syscall.SYS_RENAMEAT2
