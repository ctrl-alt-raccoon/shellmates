package setup

import (
	"errors"
	"path/filepath"
	"strings"
)

func ValidateCLIProxyServiceExecutable(manager, executablePath string) error {
	return validateCLIProxyServiceExecutable(manager, executablePath, resolveActiveHomebrewPrefix)
}

func validateCLIProxyServiceExecutable(manager, executablePath string, homebrewPrefix func() (string, error)) error {
	resolved, err := ResolveServiceManager(manager)
	if err != nil {
		return err
	}
	if resolved != "brew" {
		return nil
	}
	prefix, err := homebrewPrefix()
	if err != nil {
		return err
	}
	if prefix = strings.TrimSpace(prefix); prefix == "" || !filepath.IsAbs(prefix) {
		return errors.New("Homebrew prefix must be an absolute path")
	}
	expected := filepath.Join(prefix, "bin", "cliproxyapi")
	if sameContainedRegularFile(
		prefix,
		executablePath,
		expected,
		validateExecutablePath,
	) {
		return nil
	}
	return errors.New("Homebrew service management requires the CLIProxyAPI executable under the active brew --prefix; use --proxy-service none, docker, or another non-Homebrew manager for a custom --proxy-executable")
}
