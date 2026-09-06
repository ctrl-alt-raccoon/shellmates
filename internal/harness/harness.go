// Package harness ships the project installer and shared skills as one bundle.
// It does not load Shellmates runtime configuration or any vendor profile.
package harness

import (
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

//go:embed assets
var bundle embed.FS

// Run extracts trusted, embedded sources into a private temporary directory.
// Python interprets the installer; the temporary filesystem need not allow exec.
func Run(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	python, err := exec.LookPath("python3")
	if err != nil {
		fmt.Fprintln(errOut, "sclaude harness: Python 3.9+ is required; install it separately, then retry")
		return 2
	}
	dir, err := os.MkdirTemp("", "shellmates-harness-")
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	defer os.RemoveAll(dir)
	err = fs.WalkDir(bundle, "assets", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel("assets", path)
		if err != nil {
			return err
		}
		target := filepath.Join(dir, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		body, err := bundle.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, body, 0600)
	})
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	cmd := exec.CommandContext(ctx, python, append([]string{"-I", "-B", filepath.Join(dir, "harness.py")}, args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, errOut
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		fmt.Fprintln(errOut, err)
		return 2
	}
	return 0
}
