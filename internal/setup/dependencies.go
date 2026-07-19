package setup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

const (
	ClaudeInstallerURL   = "https://claude.ai/install.sh"
	CodexInstallerURL    = "https://chatgpt.com/codex/install.sh"
	CLIProxyInstallerURL = "https://raw.githubusercontent.com/router-for-me/cliproxyapi-installer/refs/heads/master/cliproxyapi-installer"

	ClaudeManualInstallCommand   = "curl -fsSL " + ClaudeInstallerURL + " | bash"
	CodexManualInstallCommand    = "curl -fsSL " + CodexInstallerURL + " | sh"
	CLIProxyManualInstallCommand = "curl -fsSL " + CLIProxyInstallerURL + " | bash"
)

type Platform struct {
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	Distribution string `json:"distribution,omitempty"`
	Supported    bool   `json:"supported"`
}

type Dependency struct {
	Name        string `json:"name"`
	Command     string `json:"command"`
	Path        string `json:"path,omitempty"`
	Installed   bool   `json:"installed"`
	Required    bool   `json:"required"`
	InstallHint string `json:"install_hint,omitempty"`
}

type SetupOptions struct {
	Yes                   bool
	Headless              bool
	NonInteractive        bool
	DryRun                bool
	SkipCodex             bool
	SkipProxy             bool
	SkipSmoke             bool
	NoModifyPath          bool
	BinDir                string
	CLIProxyExecutable    string
	CLIProxyConfig        string
	CLIProxyService       string
	CLIProxySystemService bool
	ManagedProxyExplicit  bool
	Input                 io.Reader
	Output                io.Writer
	ErrorOutput           io.Writer
}

type CommandRunner interface {
	Run(context.Context, string, ...string) error
}

type ExecRunner struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	DryRun bool
}

func (r ExecRunner) Run(ctx context.Context, name string, args ...string) error {
	if r.DryRun {
		_, _ = fmt.Fprintf(r.Stdout, "[dry-run] %s %s\n", name, strings.Join(args, " "))
		return nil
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = r.Stdin, r.Stdout, r.Stderr
	return cmd.Run()
}

func (r ExecRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	if r.DryRun {
		_, _ = fmt.Fprintf(r.Stdout, "[dry-run] %s %s\n", name, strings.Join(args, " "))
		return nil, nil
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin, cmd.Stderr = r.Stdin, r.Stderr
	return cmd.Output()
}

func DetectPlatform() Platform {
	p := Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
	switch runtime.GOOS {
	case "darwin":
		p.Distribution, p.Supported = "macos", runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64"
	case "linux":
		p.Distribution = detectLinuxDistribution("/etc/os-release")
		p.Supported = (p.Distribution == "ubuntu" || p.Distribution == "debian") && (runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64")
	}
	return p
}

func detectLinuxDistribution(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return "linux"
	}
	defer f.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			continue
		}
		values[key] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	id := strings.ToLower(values["ID"])
	if id == "ubuntu" || id == "debian" {
		return id
	}
	if id != "" {
		return id
	}
	return "linux"
}

func CheckDependencies() []Dependency {
	return checkDependencies(SetupOptions{})
}

func checkDependencies(opts SetupOptions) []Dependency {
	checks := []struct {
		name, command string
		required      bool
	}{
		{"GNU Screen", "screen", true},
		{"Claude Code", "claude", true},
		{"Codex CLI", "codex", false},
		{"CLIProxyAPI", "cliproxyapi", true},
	}
	result := make([]Dependency, 0, len(checks))
	for _, check := range checks {
		var path string
		var err error
		switch check.command {
		case "claude":
			path, err = discoverClaudeExecutable()
		case "screen":
			path, err = discoverScreenExecutable()
		case "cliproxyapi":
			if opts.CLIProxyExecutable != "" {
				path, err = ValidateCLIProxyExecutable(opts.CLIProxyExecutable)
			} else {
				path, err = DiscoverCLIProxyExecutable()
			}
		default:
			path, err = exec.LookPath(check.command)
		}
		result = append(result, Dependency{Name: check.name, Command: check.command, Path: path, Installed: err == nil, Required: check.required})
	}
	return result
}

func ValidateSetupOptions(opts SetupOptions) error {
	switch opts.CLIProxyService {
	case "", "auto", "brew", "systemd", "docker", "none":
	default:
		return fmt.Errorf("unsupported CLIProxyAPI service manager %q", opts.CLIProxyService)
	}
	if opts.SkipProxy && opts.ManagedProxyExplicit {
		return errors.New("cannot combine --skip-proxy with managed proxy options")
	}
	if opts.CLIProxySystemService && opts.CLIProxyService != "systemd" {
		return errors.New("--proxy-system-service requires --proxy-service systemd")
	}
	if opts.CLIProxyService == "brew" {
		if opts.CLIProxyExecutable != "" {
			if err := ValidateCLIProxyServiceExecutable("brew", opts.CLIProxyExecutable); err != nil {
				return err
			}
		}
		if opts.CLIProxyConfig != "" {
			if err := ValidateCLIProxyServiceConfig("brew", opts.CLIProxyConfig); err != nil {
				return err
			}
		}
	}
	return nil
}

func RunSetup(ctx context.Context, opts SetupOptions) error {
	if err := ValidateSetupOptions(opts); err != nil {
		return err
	}
	if opts.CLIProxyExecutable != "" {
		resolved, err := ValidateCLIProxyExecutable(opts.CLIProxyExecutable)
		if err != nil {
			return err
		}
		opts.CLIProxyExecutable = resolved
		opts.SkipProxy = true
	}
	if opts.Input == nil {
		opts.Input = os.Stdin
	}
	if opts.Output == nil {
		opts.Output = os.Stdout
	}
	if opts.ErrorOutput == nil {
		opts.ErrorOutput = os.Stderr
	}
	platform := DetectPlatform()
	if err := validateSetupPlatform(platform); err != nil {
		return err
	}
	runner := ExecRunner{Stdin: opts.Input, Stdout: opts.Output, Stderr: opts.ErrorOutput, DryRun: opts.DryRun}
	return runDependencySetup(ctx, opts, platform, runner, checkDependencies(opts))
}

func validateSetupPlatform(platform Platform) error {
	if platform.Supported ||
		(platform.OS == "linux" && (platform.Arch == "amd64" || platform.Arch == "arm64")) {
		return nil
	}
	return fmt.Errorf(
		"unsupported platform %s/%s (%s); automatic setup currently supports macOS and Debian/Ubuntu on amd64 or arm64",
		platform.OS,
		platform.Arch,
		platform.Distribution,
	)
}

func runDependencySetup(ctx context.Context, opts SetupOptions, platform Platform, runner CommandRunner, deps []Dependency) error {
	return runDependencySetupWithHomebrewResolver(
		ctx,
		opts,
		platform,
		runner,
		deps,
		resolveTrustedHomebrewExecutableAtPaths,
	)
}

func runDependencySetupWithHomebrewResolver(
	ctx context.Context,
	opts SetupOptions,
	platform Platform,
	runner CommandRunner,
	deps []Dependency,
	resolveHomebrew func(string, ...string) (trustedExecutable, error),
) error {
	missing := map[string]bool{}
	for _, dep := range deps {
		missing[dep.Command] = !dep.Installed
	}

	var dependencyErr error
	packages := missingSystemPackages(missing)
	if platform.OS == "linux" && platform.Supported && len(packages) > 0 {
		if err := installLinuxSystemPackages(ctx, opts, runner, packages); err != nil {
			dependencyErr = errors.Join(dependencyErr, err)
		}
	} else if platform.OS == "linux" && len(packages) > 0 {
		message := unsupportedLinuxPackagesMessage(platform, packages)
		if opts.DryRun {
			_, _ = fmt.Fprintln(opts.Output, "[dry-run] "+message)
		} else {
			dependencyErr = errors.Join(dependencyErr, errors.New(message))
		}
	}
	if platform.OS == "darwin" && missing["screen"] {
		brew, err := resolveHomebrew("brew", "/opt/homebrew/bin/brew", "/usr/local/bin/brew")
		if err != nil {
			message := "GNU Screen is missing and trusted Homebrew is unavailable; install Homebrew from https://brew.sh/ or provide a working screen executable; setup did not run an installer"
			if opts.DryRun {
				_, _ = fmt.Fprintln(opts.Output, "[dry-run] "+message)
			} else {
				dependencyErr = errors.Join(dependencyErr, errors.New(message))
			}
		} else {
			brewPath, err := brew.revalidate()
			if err != nil {
				dependencyErr = errors.Join(
					dependencyErr,
					fmt.Errorf("revalidate Homebrew before installing GNU Screen: %w", err),
				)
			} else if err := runner.Run(ctx, brewPath, "install", "screen"); err != nil {
				dependencyErr = errors.Join(
					dependencyErr,
					fmt.Errorf("install GNU Screen with Homebrew: %w", err),
				)
			}
		}
	}

	if missing["claude"] {
		if opts.DryRun {
			printManualDependencyInstall(
				opts.Output,
				true,
				"Claude Code",
				ClaudeManualInstallCommand,
			)
		} else {
			dependencyErr = errors.Join(
				dependencyErr,
				manualDependencyInstallError("Claude Code", ClaudeManualInstallCommand),
			)
		}
	}
	if missing["codex"] && !opts.SkipCodex {
		printManualDependencyInstall(
			opts.Output,
			opts.DryRun,
			"optional Codex CLI",
			CodexManualInstallCommand,
		)
	}
	if missing["cliproxyapi"] && !opts.SkipProxy {
		if platform.OS == "darwin" {
			brew, err := resolveHomebrew("brew", "/opt/homebrew/bin/brew", "/usr/local/bin/brew")
			if err != nil {
				message := "CLIProxyAPI is missing and trusted Homebrew is unavailable; install Homebrew from https://brew.sh/, then run: brew install cliproxyapi; setup did not run either installer"
				if opts.DryRun {
					_, _ = fmt.Fprintln(opts.Output, "[dry-run] "+message)
				} else {
					dependencyErr = errors.Join(dependencyErr, errors.New(message))
				}
			} else {
				brewPath, err := brew.revalidate()
				if err != nil {
					dependencyErr = errors.Join(
						dependencyErr,
						fmt.Errorf("revalidate Homebrew before installing CLIProxyAPI: %w", err),
					)
				} else if err := runner.Run(ctx, brewPath, "install", "cliproxyapi"); err != nil {
					dependencyErr = errors.Join(
						dependencyErr,
						fmt.Errorf("install CLIProxyAPI with Homebrew: %w", err),
					)
				}
			}
		} else if opts.DryRun {
			printManualDependencyInstall(
				opts.Output,
				true,
				"CLIProxyAPI",
				CLIProxyManualInstallCommand,
			)
		} else {
			dependencyErr = errors.Join(
				dependencyErr,
				manualDependencyInstallError("CLIProxyAPI", CLIProxyManualInstallCommand),
			)
		}
	}
	return dependencyErr
}

func installLinuxSystemPackages(ctx context.Context, opts SetupOptions, runner CommandRunner, packages []string) error {
	sudo, sudoErr := resolveTrustedExecutable("sudo", "/usr/bin/sudo", "/bin/sudo")
	aptGet, aptErr := resolveTrustedExecutable("apt-get", "/usr/bin/apt-get")
	if sudoErr != nil || aptErr != nil {
		command := linuxPackageInstallCommand("sudo", "apt-get", packages)
		if opts.DryRun {
			_, _ = fmt.Fprintf(opts.Output, "[dry-run] administrator command required; setup did not run: %s\n", command)
			return nil
		}
		return missingSystemPackagesError(packages, "sudo", "apt-get", errors.Join(sudoErr, aptErr))
	}

	command := linuxPackageInstallCommand(sudo.path(), aptGet.path(), packages)
	if opts.DryRun {
		_, _ = fmt.Fprintf(opts.Output, "[dry-run] administrator command required; setup did not run: %s\n", command)
		return nil
	}
	if opts.NonInteractive {
		return missingSystemPackagesError(packages, sudo.path(), aptGet.path(), nil)
	}
	ok, err := confirm(opts.Input, opts.Output, "Run this administrator command now? "+command, false)
	if err != nil {
		return err
	}
	if !ok {
		return missingSystemPackagesError(packages, sudo.path(), aptGet.path(), nil)
	}
	sudoPath, err := sudo.revalidate()
	if err != nil {
		return fmt.Errorf("revalidate sudo after administrator consent: %w", err)
	}
	aptGetPath, err := aptGet.revalidate()
	if err != nil {
		return fmt.Errorf("revalidate apt-get after administrator consent: %w", err)
	}
	if err := runner.Run(ctx, sudoPath, aptGetPath, "update"); err != nil {
		return err
	}
	sudoPath, err = sudo.revalidate()
	if err != nil {
		return fmt.Errorf("revalidate sudo before package installation: %w", err)
	}
	aptGetPath, err = aptGet.revalidate()
	if err != nil {
		return fmt.Errorf("revalidate apt-get before package installation: %w", err)
	}
	if err := runner.Run(ctx, sudoPath, append([]string{aptGetPath, "install", "-y"}, packages...)...); err != nil {
		return err
	}
	return nil
}

func missingSystemPackages(missing map[string]bool) []string {
	packages := []string{}
	if missing["screen"] {
		packages = append(packages, "screen")
	}
	return packages
}

func linuxPackageInstallCommand(sudoPath, aptGetPath string, packages []string) string {
	return shellQuoteCommand([]string{sudoPath, aptGetPath, "update"}) +
		" && " +
		shellQuoteCommand(append([]string{sudoPath, aptGetPath, "install", "-y"}, packages...))
}

func missingSystemPackagesError(packages []string, sudoPath, aptGetPath string, commandErr error) error {
	result := fmt.Errorf("missing system packages: %s; administrator command: %s; setup did not run it", strings.Join(packages, ", "), linuxPackageInstallCommand(sudoPath, aptGetPath, packages))
	if commandErr != nil {
		result = errors.Join(result, fmt.Errorf("resolve administrator command: %w", commandErr))
	}
	return result
}

func unsupportedLinuxPackagesMessage(platform Platform, packages []string) string {
	return fmt.Sprintf(
		"missing system packages on unsupported Linux distribution %s: %s; install them with the distribution's trusted package manager, then rerun setup; setup did not guess or run a package-manager command",
		platform.Distribution,
		strings.Join(packages, ", "),
	)
}

func manualDependencyInstallError(name, command string) error {
	return fmt.Errorf(
		"%s is missing; sclaude does not download or execute mutable third-party installer scripts automatically; the command below downloads and immediately executes remote content, so independently authenticate the source or download and inspect a fixed copy before running it, then rerun setup: %s",
		name,
		command,
	)
}

func printManualDependencyInstall(output io.Writer, dryRun bool, name, command string) {
	prefix := ""
	if dryRun {
		prefix = "[dry-run] "
	}
	_, _ = fmt.Fprintf(
		output,
		"%s%s is missing; sclaude did not download or execute its mutable installer. The optional command below downloads and immediately executes remote content; independently authenticate the source or download and inspect a fixed copy before running it: %s\n",
		prefix,
		name,
		command,
	)
}

func confirm(input io.Reader, output io.Writer, prompt string, defaultYes bool) (bool, error) {
	suffix := " [y/N] "
	if defaultYes {
		suffix = " [Y/n] "
	}
	if _, err := fmt.Fprint(output, prompt+suffix); err != nil {
		return false, err
	}
	line, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "" {
		return defaultYes, nil
	}
	return answer == "y" || answer == "yes", nil
}
