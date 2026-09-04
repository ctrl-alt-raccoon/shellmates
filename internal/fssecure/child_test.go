package fssecure

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenChildRejectsSymlinkAndReplacedParent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	directory, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	external := t.TempDir()
	if err := os.Symlink(external, filepath.Join(path, "link")); err != nil {
		t.Fatal(err)
	}
	if child, err := directory.OpenChild("link", true, 0o700); err == nil {
		child.Close()
		t.Fatal("symlink child accepted")
	}
	if err := os.Rename(path, path+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, path); err != nil {
		t.Fatal(err)
	}
	if child, err := directory.OpenChild("new", true, 0o700); err == nil {
		child.Close()
		t.Fatal("replaced parent accepted")
	}
	entries, err := os.ReadDir(external)
	if err != nil || len(entries) != 0 {
		t.Fatalf("external directory modified: %v %v", entries, err)
	}
}

func TestReadRegularLimit(t *testing.T) {
	path := t.TempDir()
	if err := os.WriteFile(filepath.Join(path, "data"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	directory, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	if _, _, err := directory.ReadRegularLimit("data", 4); err == nil {
		t.Fatal("oversize file accepted")
	}
	if data, _, err := directory.ReadRegularLimit("data", 5); err != nil || string(data) != "12345" {
		t.Fatalf("data=%q err=%v", data, err)
	}
}
