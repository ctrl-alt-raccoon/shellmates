//go:build !darwin && !linux

package fssecure

import "os"

func sameStableFileState(before, after os.FileInfo) bool {
	return os.SameFile(before, after) &&
		before.Mode() == after.Mode() &&
		before.Size() == after.Size() &&
		before.ModTime().Equal(after.ModTime())
}
