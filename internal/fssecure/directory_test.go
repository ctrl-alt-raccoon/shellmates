package fssecure

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestOpenAllowsSymlinkedAncestor(t *testing.T) {
	root := t.TempDir()
	realAncestor := filepath.Join(root, "real")
	realParent := filepath.Join(realAncestor, "parent")
	if err := os.MkdirAll(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(realAncestor, link); err != nil {
		t.Fatal(err)
	}

	directory, err := Open(filepath.Join(link, "parent"))
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	if err := directory.WriteAtomic("target", []byte("data\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertSecureFile(t, filepath.Join(realParent, "target"), "data\n", 0o600)
}

func TestOpenRejectsSymlinkFinalComponent(t *testing.T) {
	root := t.TempDir()
	realParent := filepath.Join(root, "real")
	if err := os.Mkdir(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(realParent, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(link); err == nil {
		t.Fatal("Open accepted a symlink final component")
	}
}

func TestDirectoryRevalidateRejectsModeChange(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "parent")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	directory, err := Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	if err := os.Chmod(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := directory.Revalidate(parent); err == nil {
		t.Fatal("Revalidate accepted a directory mode change")
	}
}

func TestDirectoryChmodRemainsBoundAfterPathReplacement(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	directory, err := Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()

	original := filepath.Join(root, "original")
	if err := os.Rename(parent, original); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := directory.Chmod(0o750); err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Lstat(original)
	if err != nil {
		t.Fatal(err)
	}
	if originalInfo.Mode() != os.ModeDir|0o750 {
		t.Fatalf("original mode = %v, want drwxr-x---", originalInfo.Mode())
	}
	replacementInfo, err := os.Lstat(parent)
	if err != nil {
		t.Fatal(err)
	}
	if replacementInfo.Mode() != os.ModeDir|0o700 {
		t.Fatalf("replacement mode = %v, want drwx------", replacementInfo.Mode())
	}
	if directory.Info().Mode() != os.ModeDir|0o750 {
		t.Fatalf("opened directory mode = %v, want drwxr-x---", directory.Info().Mode())
	}
	if err := directory.Revalidate(parent); err == nil {
		t.Fatal("Revalidate accepted a replacement directory after chmod")
	}
}

func TestDirectoryRemainsBoundAfterPathReplacement(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	directory, err := Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()

	original := filepath.Join(root, "original")
	if err := os.Rename(parent, original); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := directory.Revalidate(parent); err == nil {
		t.Fatal("Revalidate accepted a replacement directory")
	}
	if err := directory.WriteAtomic("target", []byte("bound\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertSecureFile(t, filepath.Join(original, "target"), "bound\n", 0o600)
	if _, err := os.Lstat(filepath.Join(parent, "target")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement directory was mutated: %v", err)
	}
}

func TestWriteNewRejectsExistingTarget(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.WriteFile(target, []byte("external\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	directory, err := Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()

	if err := directory.WriteNew("target", []byte("published\n"), 0o600); err == nil {
		t.Fatal("WriteNew replaced an existing target")
	}
	assertSecureFile(t, target, "external\n", 0o600)
}

func TestDirectoryOperationsRejectParentEntryName(t *testing.T) {
	directory := &Directory{}
	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "OpenFile",
			run: func() error {
				file, err := directory.OpenFile("..", os.O_RDONLY, 0)
				if file != nil {
					_ = file.Close()
				}
				return err
			},
		},
		{
			name: "ReadRegular",
			run: func() error {
				_, _, err := directory.ReadRegular("..")
				return err
			},
		},
		{
			name: "ReadOptionalRegular",
			run: func() error {
				_, _, err := directory.ReadOptionalRegular("..")
				return err
			},
		},
		{
			name: "InspectRegular",
			run: func() error {
				_, err := directory.InspectRegular("..")
				return err
			},
		},
		{
			name: "WriteNew",
			run: func() error {
				return directory.WriteNew("..", nil, 0o600)
			},
		},
		{
			name: "WriteNewVerified",
			run: func() error {
				return directory.WriteNewVerified("..", nil, 0o600, nil, nil)
			},
		},
		{
			name: "WriteAtomic",
			run: func() error {
				return directory.WriteAtomic("..", nil, 0o600)
			},
		},
		{
			name: "WriteAtomicChecked",
			run: func() error {
				return directory.WriteAtomicChecked("..", nil, 0o600, nil)
			},
		},
		{
			name: "WriteAtomicVerified",
			run: func() error {
				return directory.WriteAtomicVerified("..", nil, 0o600, nil, nil)
			},
		},
		{
			name: "Rename source",
			run: func() error {
				return directory.Rename("..", "target")
			},
		},
		{
			name: "Rename destination",
			run: func() error {
				return directory.Rename("target", "..")
			},
		},
		{
			name: "RenameNoReplace source",
			run: func() error {
				return directory.RenameNoReplace("..", "target")
			},
		},
		{
			name: "RenameNoReplace destination",
			run: func() error {
				return directory.RenameNoReplace("target", "..")
			},
		},
		{
			name: "Exchange first",
			run: func() error {
				return directory.Exchange("..", "target")
			},
		},
		{
			name: "Exchange second",
			run: func() error {
				return directory.Exchange("target", "..")
			},
		},
		{
			name: "Link source",
			run: func() error {
				return directory.Link("..", "target")
			},
		},
		{
			name: "Link destination",
			run: func() error {
				return directory.Link("target", "..")
			},
		},
		{
			name: "Remove",
			run: func() error {
				return directory.Remove("..")
			},
		},
		{
			name: "RemoveRegular",
			run: func() error {
				return directory.RemoveRegular("..", nil, nil)
			},
		},
		{
			name: "Unlink",
			run: func() error {
				return directory.Unlink("..")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.run()
			if err == nil || !strings.Contains(err.Error(), "entry name is invalid") {
				t.Fatalf("operation error = %v", err)
			}
		})
	}
}

func TestRemoveRegularRejectsReplacementInsertedBeforeRemoval(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.WriteFile(target, []byte("owned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	directory, err := Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	expected, err := directory.InspectRegular("target")
	if err != nil {
		t.Fatal(err)
	}

	err = directory.RemoveRegular("target", expected, func() error {
		replacement := filepath.Join(parent, "replacement")
		if err := os.WriteFile(replacement, []byte("external\n"), 0o600); err != nil {
			return err
		}
		return os.Rename(replacement, target)
	})
	if err == nil || !strings.Contains(err.Error(), "changed before removal") {
		t.Fatalf("RemoveRegular error = %v", err)
	}
	assertSecureFile(t, target, "external\n", 0o600)
}

func TestRemoveRegularOutcomeReportsUnlinkBeforeSyncFailure(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.WriteFile(target, []byte("private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	directory, err := Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	expected, err := directory.InspectRegular("target")
	if err != nil {
		t.Fatal(err)
	}
	primary := errors.New("injected directory sync failure")
	originalSync := syncDirectoryFile
	syncDirectoryFile = func(*os.File) error { return primary }
	t.Cleanup(func() { syncDirectoryFile = originalSync })

	removed, err := directory.RemoveRegularOutcome("target", expected, nil)
	if !removed || !errors.Is(err, primary) {
		t.Fatalf("RemoveRegularOutcome = removed %v, error %v", removed, err)
	}
	if _, statErr := os.Lstat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target remains after successful unlink: %v", statErr)
	}
}

func TestWriteAtomicCheckedPreservesReplacementInsertedAtPublication(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.WriteFile(target, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	directory, err := Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()

	err = directory.writeAtomic("target", []byte("published\n"), 0o600, nil, func() error {
		replacement := filepath.Join(parent, "replacement")
		if err := os.WriteFile(replacement, []byte("external\n"), 0o600); err != nil {
			return err
		}
		return os.Rename(replacement, target)
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "changed during atomic publication") {
		t.Fatalf("WriteAtomicChecked error = %v", err)
	}
	assertSecureFile(t, target, "published\n", 0o600)
	tombstones := atomicArtifacts(t, parent)
	if len(tombstones) != 1 {
		t.Fatalf("atomic artifacts = %v, want one", tombstones)
	}
	assertSecureFile(t, filepath.Join(parent, tombstones[0]), "external\n", 0o600)
}

func TestWriteAtomicRetainsTemporaryAfterPrepublicationFailure(t *testing.T) {
	parent := t.TempDir()
	directory, err := Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()

	injected := errors.New("stop before publication")
	err = directory.WriteAtomicChecked(
		"target",
		[]byte("private-new-value\n"),
		0o600,
		func() error { return injected },
	)
	if !errors.Is(err, injected) {
		t.Fatalf("WriteAtomicChecked error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(parent, "target")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target exists after failed publication: %v", err)
	}
	tombstones := atomicArtifacts(t, parent)
	if len(tombstones) != 1 {
		t.Fatalf("atomic artifacts = %v, want one", tombstones)
	}
	assertSecureFile(t, filepath.Join(parent, tombstones[0]), "private-new-value\n", 0o600)
}

func TestWriteAtomicRetainsDisplacedTargetAfterReplacement(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.WriteFile(target, []byte("private-old-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	directory, err := Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()

	if err := directory.WriteAtomic("target", []byte("private-new-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertSecureFile(t, target, "private-new-value\n", 0o600)
	tombstones := atomicArtifacts(t, parent)
	if len(tombstones) != 1 {
		t.Fatalf("atomic artifacts = %v, want one", tombstones)
	}
	assertSecureFile(t, filepath.Join(parent, tombstones[0]), "private-old-value\n", 0o600)
}

func TestWriteAtomicPreservesMultiplyLinkedTarget(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	alias := filepath.Join(parent, "alias")
	if err := os.WriteFile(target, []byte("private-old-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, alias); err != nil {
		t.Fatal(err)
	}
	directory, err := Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()

	if err := directory.WriteAtomic("target", []byte("private-new-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertSecureFile(t, target, "private-new-value\n", 0o600)
	assertSecureFile(t, alias, "private-old-value\n", 0o600)
	tombstones := atomicArtifacts(t, parent)
	if len(tombstones) != 1 {
		t.Fatalf("atomic artifacts = %v, want one", tombstones)
	}
	assertSecureFile(t, filepath.Join(parent, tombstones[0]), "private-old-value\n", 0o600)
}

func TestWriteAtomicDoesNotRetainTombstoneForNewTarget(t *testing.T) {
	parent := t.TempDir()
	directory, err := Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()

	if err := directory.WriteAtomic("target", []byte("private-new-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertSecureFile(t, filepath.Join(parent, "target"), "private-new-value\n", 0o600)
	assertNoAtomicArtifacts(t, parent)
}

func TestWriteAtomicRetainsPublishedInodeAfterPostpublicationReplacement(t *testing.T) {
	parent := t.TempDir()
	directory, err := Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()

	retainedPublished := filepath.Join(parent, "retained-published")
	injected := errors.New("published target changed")
	err = directory.WriteAtomicVerified(
		"target",
		[]byte("private-new-value\n"),
		0o600,
		nil,
		func(os.FileInfo) error {
			if err := os.Link(filepath.Join(parent, "target"), retainedPublished); err != nil {
				return err
			}
			replacement := filepath.Join(parent, "replacement")
			if err := os.WriteFile(replacement, []byte("external-value\n"), 0o600); err != nil {
				return err
			}
			if err := os.Rename(replacement, filepath.Join(parent, "target")); err != nil {
				return err
			}
			return injected
		},
	)
	if !errors.Is(err, injected) {
		t.Fatalf("WriteAtomicVerified error = %v", err)
	}
	assertSecureFile(t, filepath.Join(parent, "target"), "external-value\n", 0o600)
	assertSecureFile(t, retainedPublished, "private-new-value\n", 0o600)
	assertNoAtomicArtifacts(t, parent)
}

func TestWriteAtomicAllowsMultiplyLinkedDisplacedTombstone(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.WriteFile(target, []byte("private-old-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	directory, err := Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()

	var alias string
	err = directory.WriteAtomicVerified(
		"target",
		[]byte("private-new-value\n"),
		0o600,
		nil,
		func(os.FileInfo) error {
			tombstones := atomicArtifacts(t, parent)
			if len(tombstones) != 1 {
				return fmt.Errorf("atomic artifacts = %v, want one", tombstones)
			}
			alias = filepath.Join(parent, "retained-alias")
			return os.Link(filepath.Join(parent, tombstones[0]), alias)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertSecureFile(t, target, "private-new-value\n", 0o600)
	assertSecureFile(t, alias, "private-old-value\n", 0o600)
	tombstones := atomicArtifacts(t, parent)
	if len(tombstones) != 1 {
		t.Fatalf("atomic artifacts = %v, want one", tombstones)
	}
	assertSecureFile(t, filepath.Join(parent, tombstones[0]), "private-old-value\n", 0o600)
}

func TestWriteAtomicRetainsOwnedInodeAfterTombstoneSubstitution(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.WriteFile(target, []byte("private-old-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	directory, err := Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()

	foreign := []byte("foreign-value\n")
	retainedOwned := filepath.Join(parent, "retained-owned")
	err = directory.WriteAtomicVerified(
		"target",
		[]byte("private-new-value\n"),
		0o600,
		nil,
		func(os.FileInfo) error {
			tombstones := atomicArtifacts(t, parent)
			if len(tombstones) != 1 {
				return fmt.Errorf("atomic artifacts = %v, want one", tombstones)
			}
			tombstone := filepath.Join(parent, tombstones[0])
			if err := os.Rename(tombstone, retainedOwned); err != nil {
				return err
			}
			return os.WriteFile(tombstone, foreign, 0o600)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertSecureFile(t, target, "private-new-value\n", 0o600)
	assertSecureFile(t, retainedOwned, "private-old-value\n", 0o600)
	tombstones := atomicArtifacts(t, parent)
	if len(tombstones) != 1 {
		t.Fatalf("atomic artifacts = %v, want one", tombstones)
	}
	assertSecureFile(t, filepath.Join(parent, tombstones[0]), string(foreign), 0o600)
}

func TestRegularInspectionRejectsFIFOWithoutBlocking(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func(*Directory) error
	}{
		{
			name: "ReadRegular",
			run: func(directory *Directory) error {
				_, _, err := directory.ReadRegular("target")
				return err
			},
		},
		{
			name: "InspectRegular",
			run: func(directory *Directory) error {
				_, err := directory.InspectRegular("target")
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			if err := syscall.Mkfifo(filepath.Join(parent, "target"), 0o600); err != nil {
				t.Fatal(err)
			}
			directory, err := Open(parent)
			if err != nil {
				t.Fatal(err)
			}
			defer directory.Close()

			done := make(chan error, 1)
			go func() { done <- test.run(directory) }()
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "not a regular") {
					t.Fatalf("operation error = %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("operation blocked opening a FIFO")
			}
		})
	}
}

func TestStableFileStateDetectsMetadataChanges(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	alias := filepath.Join(parent, "alias")
	if err := os.WriteFile(target, []byte("contents\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !sameStableFileState(before, before) {
		t.Fatal("stable file state rejected identical metadata")
	}
	if err := os.Link(target, alias); err != nil {
		t.Fatal(err)
	}
	afterLink, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if sameStableFileState(before, afterLink) {
		t.Fatal("stable file state accepted a changed link count or ctime")
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	afterMode, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if sameStableFileState(afterLink, afterMode) {
		t.Fatal("stable file state accepted changed mode metadata")
	}
}

func TestReadRegularUsesOpenedDirectory(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "target"), []byte("original\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	directory, err := Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()

	original := filepath.Join(root, "original")
	if err := os.Rename(parent, original); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "target"), []byte("replacement\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	data, info, err := directory.ReadRegular("target")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original\n" || info.Mode().Perm() != 0o640 {
		t.Fatalf("ReadRegular = %q mode %v", data, info.Mode())
	}
}

func atomicArtifacts(t *testing.T, parent string) []string {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".sclaude-") {
			names = append(names, entry.Name())
		}
	}
	return names
}

func assertNoAtomicArtifacts(t *testing.T, parent string) {
	t.Helper()
	if names := atomicArtifacts(t, parent); len(names) != 0 {
		t.Fatalf("atomic artifacts = %v, want none", names)
	}
}

func assertSecureFile(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != contents {
		t.Fatalf("%s contents = %q, want %q", path, data, contents)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != mode {
		t.Fatalf("%s mode = %v, want %o regular", path, info.Mode(), mode)
	}
}
