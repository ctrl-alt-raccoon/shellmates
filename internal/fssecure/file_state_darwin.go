//go:build darwin

package fssecure

import "syscall"

func statChangeTime(stat *syscall.Stat_t) (int64, int64) {
	return int64(stat.Ctimespec.Sec), int64(stat.Ctimespec.Nsec)
}
