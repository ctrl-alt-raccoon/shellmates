package setup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareShellPATHDoesNotMutate(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".profile")
	original := []byte("export OTHER=1\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}

	plan, err := prepareShellPATH(path, "/tmp/sclaude-bin", "sh")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.changed || plan.createdBySetup {
		t.Fatalf("unexpected plan: %+v", plan.edit())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(original) {
		t.Fatalf("preparation changed contents to %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("preparation changed mode to %o", info.Mode().Perm())
	}
}

func TestShellPlanPreservesExistingMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".profile")
	if err := os.WriteFile(path, []byte("export OTHER=1\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}

	plan, err := prepareShellPATH(path, "/tmp/sclaude-bin", "sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := revalidateShellPlan(plan); err != nil {
		t.Fatal(err)
	}
	if err := applyShellPlan(plan); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("applied mode = %o, want 640", info.Mode().Perm())
	}
}

func TestShellPlanCreatesPrivateProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", ".profile")
	plan, err := prepareShellPATH(path, "/tmp/sclaude-bin", "sh")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.createdBySetup || !plan.changed {
		t.Fatalf("unexpected plan: %+v", plan.edit())
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("prepare created profile: %v", err)
	}
	if err := revalidateShellPlan(plan); err != nil {
		t.Fatal(err)
	}
	if err := applyShellPlan(plan); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("created mode = %o, want 600", info.Mode().Perm())
	}
}

func TestShellPlanPreservesRequestedSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "shared-profile")
	path := filepath.Join(dir, ".zshrc")
	if err := os.WriteFile(target, []byte("export OTHER=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("shared-profile", path); err != nil {
		t.Fatal(err)
	}

	plan, err := prepareShellPATH(path, "/tmp/sclaude-bin", "zsh")
	if err != nil {
		t.Fatal(err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if plan.target != resolvedTarget {
		t.Fatalf("target = %q, want %q", plan.target, resolvedTarget)
	}
	if err := revalidateShellPlan(plan); err != nil {
		t.Fatal(err)
	}
	if err := applyShellPlan(plan); err != nil {
		t.Fatal(err)
	}
	link, err := os.Readlink(path)
	if err != nil {
		t.Fatal(err)
	}
	if link != "shared-profile" {
		t.Fatalf("symlink changed to %q", link)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), pathBlockStart) {
		t.Fatalf("target was not updated:\n%s", data)
	}
}

func TestPrepareShellPATHRejectsUnsafeProfiles(t *testing.T) {
	t.Run("broken symlink", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), ".zshrc")
		if err := os.Symlink("missing", path); err != nil {
			t.Fatal(err)
		}
		if _, err := prepareShellPATH(path, "/tmp/bin", "zsh"); err == nil || !strings.Contains(err.Error(), "resolve shell profile symlink") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("symlink to directory", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".zshrc")
		if err := os.Symlink(filepath.Join(dir, "target"), path); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(dir, "target"), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := prepareShellPATH(path, "/tmp/bin", "zsh"); err == nil || !strings.Contains(err.Error(), "regular non-symlink") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("requested directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), ".zshrc")
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := prepareShellPATH(path, "/tmp/bin", "zsh"); err == nil || !strings.Contains(err.Error(), "regular file or symlink") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestPrepareShellPATHRejectsDamagedMarkers(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
	}{
		{name: "missing end", data: pathBlockStart + "\nold\n"},
		{name: "orphan end", data: pathBlockEnd + "\n"},
		{name: "duplicate start", data: pathBlockStart + "\n" + pathBlockStart + "\n" + pathBlockEnd + "\n"},
		{name: "duplicate block", data: pathBlockStart + "\n" + pathBlockEnd + "\n" + pathBlockStart + "\n" + pathBlockEnd + "\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".profile")
			if err := os.WriteFile(path, []byte(test.data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := prepareShellPATH(path, "/tmp/bin", "sh"); err == nil || !strings.Contains(err.Error(), "PATH markers") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestRevalidateShellPlanRejectsChanges(t *testing.T) {
	newProfile := func(t *testing.T) (string, shellPlan) {
		t.Helper()
		path := filepath.Join(t.TempDir(), ".profile")
		if err := os.WriteFile(path, []byte("export ORIGINAL=1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		plan, err := prepareShellPATH(path, "/tmp/bin", "sh")
		if err != nil {
			t.Fatal(err)
		}
		return path, plan
	}

	t.Run("contents", func(t *testing.T) {
		path, plan := newProfile(t)
		if err := os.WriteFile(path, []byte("export CHANGED=1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := revalidateShellPlan(plan); err == nil || !strings.Contains(err.Error(), "changed after preparation") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("mode", func(t *testing.T) {
		path, plan := newProfile(t)
		if err := os.Chmod(path, 0o640); err != nil {
			t.Fatal(err)
		}
		if err := revalidateShellPlan(plan); err == nil || !strings.Contains(err.Error(), "changed after preparation") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("identity", func(t *testing.T) {
		path, plan := newProfile(t)
		if err := os.Rename(path, path+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, plan.original, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := revalidateShellPlan(plan); err == nil || !strings.Contains(err.Error(), "changed after preparation") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("new path appeared", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), ".profile")
		plan, err := prepareShellPATH(path, "/tmp/bin", "sh")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("user data\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := revalidateShellPlan(plan); err == nil || !strings.Contains(err.Error(), "changed after preparation") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("symlink replaced", func(t *testing.T) {
		dir := t.TempDir()
		first := filepath.Join(dir, "first")
		second := filepath.Join(dir, "second")
		path := filepath.Join(dir, ".zshrc")
		for _, target := range []string{first, second} {
			if err := os.WriteFile(target, []byte("export ORIGINAL=1\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink("first", path); err != nil {
			t.Fatal(err)
		}
		plan, err := prepareShellPATH(path, "/tmp/bin", "zsh")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("second", path); err != nil {
			t.Fatal(err)
		}
		if err := revalidateShellPlan(plan); err == nil || !strings.Contains(err.Error(), "changed after preparation") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("symlink target identity", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target")
		path := filepath.Join(dir, ".zshrc")
		if err := os.WriteFile(target, []byte("export ORIGINAL=1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("target", path); err != nil {
			t.Fatal(err)
		}
		plan, err := prepareShellPATH(path, "/tmp/bin", "zsh")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(target, target+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, plan.original, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := revalidateShellPlan(plan); err == nil || !strings.Contains(err.Error(), "changed after preparation") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestConfigureShellPATHReturnsUnchangedOwnershipMetadata(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")
	path := filepath.Join(home, ".zshrc")
	if _, err := EnsureShellPATH(path, "/tmp/bin", "zsh"); err != nil {
		t.Fatal(err)
	}

	edits, err := ConfigureShellPATH("/tmp/bin", SetupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(edits) != 1 || edits[0].Changed || edits[0].Digest == "" || edits[0].Path != path {
		t.Fatalf("unexpected edits: %+v", edits)
	}
}

func TestManagedBlockReplacementPreservesSurroundingCRLF(t *testing.T) {
	original := []byte("before\r\n" + pathBlockStart + "\r\nold\r\n" + pathBlockEnd + "\r\n\r\nafter\r\n")
	block := []byte(pathBlockStart + "\nnew\n" + pathBlockEnd + "\n")

	updated, changed, err := replaceManagedBlock(original, block)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("replacement reported no change")
	}
	want := "before\r\n" + string(block) + "\r\nafter\r\n"
	if string(updated) != want {
		t.Fatalf("updated = %q, want %q", updated, want)
	}

	removed, changed, err := removeManagedBlocks(original)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("removal reported no change")
	}
	if string(removed) != "before\r\n\r\nafter\r\n" {
		t.Fatalf("removed = %q", removed)
	}
}
