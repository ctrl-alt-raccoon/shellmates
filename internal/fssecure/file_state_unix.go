//go:build darwin || linux

package fssecure

import (
	"os"
	"syscall"
)

type stableFileState struct {
	uid       uint32
	gid       uint32
	linkCount uint64
	ctimeSec  int64
	ctimeNsec int64
}

func sameStableFileState(before, after os.FileInfo) bool {
	beforeState, beforeOK := stableFileStateFromInfo(before)
	afterState, afterOK := stableFileStateFromInfo(after)
	return beforeOK && afterOK &&
		os.SameFile(before, after) &&
		before.Mode() == after.Mode() &&
		before.Size() == after.Size() &&
		before.ModTime().Equal(after.ModTime()) &&
		beforeState == afterState
}

func stableFileStateFromInfo(info os.FileInfo) (stableFileState, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return stableFileState{}, false
	}
	sec, nsec := statChangeTime(stat)
	return stableFileState{
		uid:       stat.Uid,
		gid:       stat.Gid,
		linkCount: uint64(stat.Nlink),
		ctimeSec:  sec,
		ctimeNsec: nsec,
	}, true
}
