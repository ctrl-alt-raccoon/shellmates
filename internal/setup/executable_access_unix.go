//go:build darwin || linux

package setup

import (
	"os"
	"syscall"
)

func executableByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	mode := info.Mode().Perm()
	uid := uint32(os.Geteuid())
	if uid == 0 {
		return mode&0o111 != 0
	}
	if stat.Uid == uid {
		return mode&0o100 != 0
	}
	if stat.Gid == uint32(os.Getegid()) {
		return mode&0o010 != 0
	}
	groups, err := os.Getgroups()
	if err == nil {
		for _, group := range groups {
			if stat.Gid == uint32(group) {
				return mode&0o010 != 0
			}
		}
	}
	return mode&0o001 != 0
}
