//go:build !darwin && !linux

package setup

import "os"

func executableByCurrentUser(info os.FileInfo) bool {
	return info.Mode().Perm()&0o111 != 0
}
