package setup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	pathBlockStart = "# >>> sclaude managed PATH >>>"
	pathBlockEnd   = "# <<< sclaude managed PATH <<<"
)

type ShellEdit struct {
	Path    string `json:"path"`
	Changed bool   `json:"changed"`
	Created bool   `json:"created"`
	Digest  string `json:"digest,omitempty"`
}

type shellPlan struct {
	path            string
	target          string
	shell           string
	requestedExists bool
	requestedInfo   os.FileInfo
	requestedLink   string
	targetExists    bool
	targetInfo      os.FileInfo
	original        []byte
	mode            os.FileMode
	replacement     []byte
	beforeDigest    string
	afterDigest     string
	managedDigest   string
	createdBySetup  bool
	changed         bool
}

type shellRemovalPlan struct {
	path   string
	data   []byte
	mode   os.FileMode
	remove bool
}

func EnsureShellPATH(path, binDir, shell string) (ShellEdit, error) {
	plan, err := prepareShellPATH(path, binDir, shell)
	if err != nil {
		return ShellEdit{Path: path}, err
	}
	if err := revalidateShellPlan(plan); err != nil {
		return plan.edit(), err
	}
	if err := applyShellPlan(plan); err != nil {
		return plan.edit(), err
	}
	return plan.edit(), nil
}

func prepareShellPATH(path, binDir, shell string) (shellPlan, error) {
	plan := shellPlan{path: path, shell: shell}
	if strings.TrimSpace(binDir) == "" {
		return plan, errors.New("bin directory is required")
	}
	absoluteBinDir, err := filepath.Abs(binDir)
	if err != nil {
		return plan, fmt.Errorf("resolve bin directory: %w", err)
	}
	absoluteBinDir = filepath.Clean(absoluteBinDir)
	if strings.ContainsRune(absoluteBinDir, os.PathListSeparator) {
		return plan, fmt.Errorf("bin directory cannot contain PATH separator %q", os.PathListSeparator)
	}
	if strings.TrimSpace(path) == "" {
		return plan, errors.New("shell profile path is required")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return plan, fmt.Errorf("resolve shell profile path: %w", err)
	}
	plan.path = filepath.Clean(absolutePath)

	info, err := os.Lstat(plan.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		plan.target = plan.path
		plan.mode = 0o600
		plan.createdBySetup = true
	case err != nil:
		return plan, fmt.Errorf("inspect shell profile %s: %w", plan.path, err)
	default:
		plan.requestedExists = true
		plan.requestedInfo = info
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			plan.requestedLink, err = os.Readlink(plan.path)
			if err != nil {
				return plan, fmt.Errorf("read shell profile symlink %s: %w", plan.path, err)
			}
			plan.target, err = filepath.EvalSymlinks(plan.path)
			if err != nil {
				return plan, fmt.Errorf("resolve shell profile symlink %s: %w", plan.path, err)
			}
			plan.target, err = filepath.Abs(plan.target)
			if err != nil {
				return plan, fmt.Errorf("resolve shell profile target %s: %w", plan.path, err)
			}
			plan.target = filepath.Clean(plan.target)
		case info.Mode().IsRegular():
			plan.target = plan.path
		default:
			return plan, fmt.Errorf("shell profile %s must be a regular file or symlink to one", plan.path)
		}
	}

	if plan.requestedExists {
		original, targetInfo, err := readStableRegularFile(plan.target)
		if err != nil {
			return plan, fmt.Errorf("inspect shell profile target %s: %w", plan.target, err)
		}
		plan.targetExists = true
		plan.targetInfo = targetInfo
		plan.original = original
		plan.mode = targetInfo.Mode().Perm()
	} else {
		if _, err := os.Lstat(plan.target); err == nil {
			return plan, fmt.Errorf("shell profile target unexpectedly exists: %s", plan.target)
		} else if !errors.Is(err, os.ErrNotExist) {
			return plan, fmt.Errorf("inspect shell profile target %s: %w", plan.target, err)
		}
		plan.original = []byte{}
	}

	block, err := shellPATHBlock(absoluteBinDir, shell)
	if err != nil {
		return plan, err
	}
	blockBytes := []byte(block)
	plan.managedDigest = shellBlockDigest(blockBytes)
	plan.replacement, plan.changed, err = replaceManagedBlock(plan.original, blockBytes)
	if err != nil {
		return plan, fmt.Errorf("inspect shell profile %s: %w", plan.path, err)
	}
	plan.beforeDigest = shellBlockDigest(plan.original)
	plan.afterDigest = shellBlockDigest(plan.replacement)
	return plan, nil
}

func prepareShellPlans(binDir string, opts SetupOptions) ([]shellPlan, error) {
	if opts.NoModifyPath || opts.DryRun {
		return nil, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	type candidate struct {
		path  string
		shell string
	}
	candidates := []candidate{
		{filepath.Join(home, ".zshrc"), "zsh"},
		{filepath.Join(home, ".bashrc"), "bash"},
		{filepath.Join(home, ".profile"), "sh"},
	}
	activeShell := filepath.Base(os.Getenv("SHELL"))
	if activeShell == "fish" {
		candidates = append(candidates, candidate{
			filepath.Join(home, ".config", "fish", "conf.d", "sclaude.fish"),
			"fish",
		})
	}
	anyExisting := false
	for _, item := range candidates {
		if item.shell == "fish" {
			continue
		}
		if _, err := os.Lstat(item.path); err == nil {
			anyExisting = true
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	if !anyExisting && activeShell != "fish" {
		chosen := candidate{filepath.Join(home, ".profile"), "sh"}
		switch activeShell {
		case "zsh":
			chosen = candidate{filepath.Join(home, ".zshrc"), "zsh"}
		case "bash":
			chosen = candidate{filepath.Join(home, ".bashrc"), "bash"}
		}
		candidates = []candidate{chosen}
	}

	plans := make([]shellPlan, 0, len(candidates))
	for _, item := range candidates {
		if item.shell != "fish" {
			if _, err := os.Lstat(item.path); errors.Is(err, os.ErrNotExist) && anyExisting {
				continue
			} else if err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		}
		plan, err := prepareShellPATH(item.path, binDir, item.shell)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func revalidateShellPlan(plan shellPlan) error {
	info, err := os.Lstat(plan.path)
	if !plan.requestedExists {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("revalidate shell profile %s: %w", plan.path, err)
		}
		return fmt.Errorf("shell profile %s changed after preparation", plan.path)
	}
	if err != nil {
		return fmt.Errorf("revalidate shell profile %s: %w", plan.path, err)
	}
	if plan.requestedInfo == nil || !os.SameFile(plan.requestedInfo, info) || info.Mode() != plan.requestedInfo.Mode() {
		return fmt.Errorf("shell profile %s changed after preparation", plan.path)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(plan.path)
		if err != nil || link != plan.requestedLink {
			return fmt.Errorf("shell profile symlink %s changed after preparation", plan.path)
		}
		resolved, err := filepath.EvalSymlinks(plan.path)
		if err != nil {
			return fmt.Errorf("revalidate shell profile symlink %s: %w", plan.path, err)
		}
		resolved, err = filepath.Abs(resolved)
		if err != nil || filepath.Clean(resolved) != plan.target {
			return fmt.Errorf("shell profile symlink %s changed after preparation", plan.path)
		}
	}
	data, targetInfo, err := readStableRegularFile(plan.target)
	if err != nil {
		return fmt.Errorf("revalidate shell profile target %s: %w", plan.target, err)
	}
	if !plan.targetExists || plan.targetInfo == nil || !os.SameFile(plan.targetInfo, targetInfo) || targetInfo.Mode() != plan.targetInfo.Mode() || shellBlockDigest(data) != plan.beforeDigest {
		return fmt.Errorf("shell profile target %s changed after preparation", plan.target)
	}
	return nil
}

func applyShellPlan(plan shellPlan) error {
	if !plan.changed {
		return nil
	}
	return writePrivateFileSynced(plan.target, plan.replacement, plan.mode)
}

func (plan shellPlan) edit() ShellEdit {
	return ShellEdit{
		Path:    plan.path,
		Changed: plan.changed,
		Created: plan.createdBySetup,
		Digest:  plan.managedDigest,
	}
}

func readStableRegularFile(path string) ([]byte, os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, nil, errors.New("target is not a regular non-symlink file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !os.SameFile(before, opened) {
		return nil, nil, errors.New("target changed while opening")
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, nil, err
	}
	after, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !os.SameFile(opened, after) || after.Mode() != opened.Mode() {
		return nil, nil, errors.New("target changed while reading")
	}
	return data, after, nil
}

func RemoveShellPATH(path string) (ShellEdit, error) {
	edit := ShellEdit{Path: path}
	original, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return edit, nil
	}
	if err != nil {
		return edit, err
	}
	updated, changed, err := removeManagedBlocks(original)
	if err != nil {
		return edit, err
	}
	if !changed {
		return edit, nil
	}
	if err := writePrivateFilePreservingSymlink(path, updated, fileModeOr(path, 0o600)); err != nil {
		return edit, err
	}
	edit.Changed = true
	return edit, nil
}

func shellPATHBlock(binDir, shell string) (string, error) {
	quoted := shellSingleQuote(binDir)
	switch shell {
	case "sh", "bash", "zsh":
		return fmt.Sprintf(`%s
_sclaude_bin=%s
_sclaude_found=
_sclaude_old_ifs=$IFS
_sclaude_noglob=
case $- in
  *f*) _sclaude_noglob=1 ;;
  *) set -f ;;
esac
IFS=:
for _sclaude_component in $PATH; do
  if [ "$_sclaude_component" = "$_sclaude_bin" ]; then
    _sclaude_found=1
    break
  fi
done
IFS=$_sclaude_old_ifs
if [ -z "$_sclaude_noglob" ]; then
  set +f
fi
if [ -z "$_sclaude_found" ]; then
  PATH="$_sclaude_bin${PATH:+:$PATH}"
  export PATH
fi
unset _sclaude_bin _sclaude_found _sclaude_old_ifs _sclaude_noglob _sclaude_component
%s
`, pathBlockStart, quoted, pathBlockEnd), nil
	case "fish":
		return fmt.Sprintf(`%s
if not contains -- %s $PATH
    set -gx PATH %s $PATH
end
%s
`, pathBlockStart, quoted, quoted, pathBlockEnd), nil
	default:
		return "", fmt.Errorf("unsupported shell %q", shell)
	}
}

func removeManagedBlocks(original []byte) ([]byte, bool, error) {
	updated := append([]byte{}, original...)
	changed := false
	for {
		start, end, err := managedBlockBounds(updated)
		if err != nil {
			return nil, false, err
		}
		if start < 0 {
			return updated, changed, nil
		}
		next := append([]byte{}, updated[:start]...)
		next = append(next, updated[end:]...)
		updated = next
		changed = true
	}
}

func managedBlockBounds(data []byte) (int, int, error) {
	startMarker := []byte(pathBlockStart)
	endMarker := []byte(pathBlockEnd)
	start := bytes.Index(data, startMarker)
	end := bytes.Index(data, endMarker)
	if start < 0 && end < 0 {
		return -1, -1, nil
	}
	if start < 0 || end < start {
		return -1, -1, errors.New("damaged sclaude PATH markers; refusing automatic removal")
	}
	if bytes.Index(data[start+len(startMarker):end], startMarker) >= 0 || bytes.Index(data[end+len(endMarker):], startMarker) >= 0 || bytes.Index(data[end+len(endMarker):], endMarker) >= 0 {
		return -1, -1, errors.New("multiple or damaged sclaude PATH markers; refusing automatic removal")
	}
	end += len(endMarker)
	if end < len(data) && data[end] == '\r' {
		end++
	}
	if end < len(data) && data[end] == '\n' {
		end++
	}
	return start, end, nil
}

func replaceManagedBlock(original, block []byte) ([]byte, bool, error) {
	start, end, err := managedBlockBounds(original)
	if err != nil {
		return nil, false, err
	}
	if start < 0 {
		updated := append([]byte{}, original...)
		if len(updated) > 0 && updated[len(updated)-1] != '\n' {
			updated = append(updated, '\n')
		}
		updated = append(updated, block...)
		return updated, true, nil
	}
	updated := append([]byte{}, original[:start]...)
	updated = append(updated, block...)
	updated = append(updated, original[end:]...)
	return updated, !bytes.Equal(updated, original), nil
}

func prepareOwnedShellRemoval(path string, ownership ShellBlockOwnership) (shellRemovalPlan, error) {
	if path == "" || ownership.Digest == "" {
		return shellRemovalPlan{}, errors.New("shell ownership record is incomplete")
	}
	original, err := os.ReadFile(path)
	if err != nil {
		return shellRemovalPlan{}, fmt.Errorf("inspect managed shell file %s: %w", path, err)
	}
	start, end, err := managedBlockBounds(original)
	if err != nil {
		return shellRemovalPlan{}, fmt.Errorf("inspect managed shell file %s: %w", path, err)
	}
	if start < 0 {
		return shellRemovalPlan{}, fmt.Errorf("managed PATH block is missing from %s", path)
	}
	blockEnd := bytes.Index(original[start:], []byte(pathBlockEnd))
	if blockEnd < 0 {
		return shellRemovalPlan{}, fmt.Errorf("managed PATH block is damaged in %s", path)
	}
	blockEnd += start + len(pathBlockEnd)
	if blockEnd < len(original) && original[blockEnd] == '\r' {
		blockEnd++
	}
	if blockEnd < len(original) && original[blockEnd] == '\n' {
		blockEnd++
	}
	if shellBlockDigest(original[start:blockEnd]) != ownership.Digest {
		return shellRemovalPlan{}, fmt.Errorf("managed PATH block in %s was modified", path)
	}
	updated := append([]byte{}, original[:start]...)
	updated = append(updated, original[end:]...)
	return shellRemovalPlan{
		path:   path,
		data:   updated,
		mode:   fileModeOr(path, 0o600),
		remove: ownership.Created && len(bytes.TrimSpace(updated)) == 0,
	}, nil
}

func applyOwnedShellRemoval(plan shellRemovalPlan) error {
	if plan.remove {
		return os.Remove(plan.path)
	}
	return writePrivateFilePreservingSymlink(plan.path, plan.data, plan.mode)
}

func shellBlockDigest(block []byte) string {
	sum := sha256.Sum256(block)
	return hex.EncodeToString(sum[:])
}

func writePrivateFilePreservingSymlink(path string, data []byte, mode os.FileMode) error {
	target := path
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		target = resolved
	}
	return writePrivateFile(target, data, mode)
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
