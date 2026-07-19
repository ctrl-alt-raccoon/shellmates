//go:build linux

package fssecure

import "syscall"

func statChangeTime(stat *syscall.Stat_t) (int64, int64) {
	return int64(stat.Ctim.Sec), int64(stat.Ctim.Nsec)
}
