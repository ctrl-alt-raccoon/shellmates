package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"

	"github.com/ctrl-alt-raccoon/sclaude/internal/backend"
	"github.com/ctrl-alt-raccoon/sclaude/internal/config"
	"github.com/ctrl-alt-raccoon/sclaude/internal/managedsettings"
	screenpkg "github.com/ctrl-alt-raccoon/sclaude/internal/screen"
	"github.com/ctrl-alt-raccoon/sclaude/internal/session"
	"github.com/ctrl-alt-raccoon/sclaude/internal/setup"
	"github.com/ctrl-alt-raccoon/sclaude/internal/ui"
)

type IO struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

type Policy string

const (
	PolicyDirect  Policy = "direct"
	PolicyManaged Policy = "managed"
)

func Classify(args []string, stdinTTY, stdoutTTY bool, env map[string]string) Policy {
	return ClassifyBackend("claude", args, stdinTTY, stdoutTTY, env)
}

func ClassifyBackend(backendName string, args []string, stdinTTY, stdoutTTY bool, env map[string]string) Policy {
	if env["SCLAUDE_BYPASS"] == "1" || env["SCLAUDE_MANAGED"] == "1" {
		return PolicyDirect
	}
	if env["STY"] != "" && env["SCLAUDE_FORCE_NEST"] != "1" {
		return PolicyDirect
	}
	if backendName == "codex" && codexDirect(args) {
		return PolicyDirect
	}
	if env["SCLAUDE_FORCE"] == "1" {
		return PolicyManaged
	}
	if !stdinTTY || !stdoutTTY {
		return PolicyDirect
	}
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if backendName != "codex" && directPassThroughArg(arg) {
			return PolicyDirect
		}
	}
	return PolicyManaged
}

func directPassThroughArg(arg string) bool {
	switch arg {
	case "-p", "--print", "--help", "-h", "--version":
		return true
	}
	return strings.HasPrefix(arg, "--print=")
}

func Run(ctx context.Context, argv0 string, args []string, ioSet IO, version string) int {
	if ioSet.In == nil {
		ioSet.In = os.Stdin
	}
	if ioSet.Out == nil {
		ioSet.Out = os.Stdout
	}
	if ioSet.Err == nil {
		ioSet.Err = os.Stderr
	}
	name := filepath.Base(argv0)
	backendName, productCommand := classifyInvocation(name, args)
	if isProductBinary(name) && len(args) > 0 {
		switch args[0] {
		case "--":
			return runDirect(ctx, backendName, args[1:], ioSet)
		case "-h", "--help":
			if name == "scodex" {
				return runDirect(ctx, backendName, args, ioSet)
			}
			if len(args) != 1 {
				return usageError(ioSet.Err, errors.New("help does not accept arguments"))
			}
			printProductUsage(ioSet.Out)
			return 0
		case "--version":
			if name == "scodex" {
				return runDirect(ctx, backendName, args, ioSet)
			}
			if len(args) != 1 {
				return usageError(ioSet.Err, errors.New("--version does not accept arguments"))
			}
			_, _ = fmt.Fprintln(ioSet.Out, version)
			return 0
		}
	}
	if productCommand {
		return runSubcommand(ctx, args, ioSet, version, backendName)
	}
	return runLaunch(ctx, backendName, args, ioSet)
}

func classifyInvocation(name string, args []string) (backendName string, productCommand bool) {
	backendName = "claude"
	productBinary := isProductBinary(name)
	if name == "sclaudex" {
		backendName = "claudex"
	}
	if name == "scodex" {
		backendName = "codex"
		// Native doctor/update/help commands retain their vendor meaning.
		return backendName, len(args) > 0 && isCodexManagerCommand(args[0])
	}
	return backendName, productBinary && len(args) > 0 && isSubcommand(args[0])
}

func isProductBinary(name string) bool {
	switch name {
	case "sclaude", "sclaudex", "scodex",
		"sclaude_darwin_amd64", "sclaude_darwin_arm64",
		"sclaude_linux_amd64", "sclaude_linux_arm64":
		return true
	}
	return false
}

func runDirect(ctx context.Context, backendName string, args []string, ioSet IO) int {
	paths, err := config.DefaultPaths()
	if err != nil {
		return fail(ioSet.Err, err)
	}
	runtimeConfig, err := setup.LoadRuntime(paths)
	if err != nil {
		return fail(ioSet.Err, err)
	}
	command, commandArgs, commandEnv, err := backend.Command(runtimeConfig, backendName, args, os.Environ())
	if err != nil {
		return fail(ioSet.Err, err)
	}
	cmd := exec.CommandContext(ctx, command, commandArgs...)
	cmd.Env = commandEnv
	cmd.Stdin, cmd.Stdout, cmd.Stderr = ioSet.In, ioSet.Out, ioSet.Err
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return commandExitCode(exitErr)
		}
		return fail(ioSet.Err, err)
	}
	return 0
}

func printProductUsage(output io.Writer) {
	_, _ = fmt.Fprint(output, `Usage:
  sclaude [CLAUDE_ARGS...]
  sclaudex [CLAUDE_ARGS...]
  scodex [CODEX_ARGS...]
  sclaude -- CLAUDE_ARGS...
  sclaude COMMAND [OPTIONS]

Commands:
  help                    Show this help
  sessions, list          List managed sessions
  new                     Create a managed session
  attach                  Attach to a managed session
  stop                    Stop a managed session
  prune                   Remove stopped session records
  setup                   Configure selected backends (including native Codex)
  doctor                  Check the configured runtime
  verify                  Verify the managed proxy
  update                  Install a published release
  rollback                Activate the previous release
  uninstall               Remove the installed release
  version                 Print the sclaude version

Use a leading -- to pass all following arguments directly to the selected backend.
scodex reserves sessions/list/new/attach/stop/prune/setup for the manager.
scodex help, doctor, update, --help and --version belong to Codex itself.
`)
}

func runLaunch(ctx context.Context, backendName string, args []string, ioSet IO) int {
	ioSet.In = bufio.NewReader(ioSet.In)
	paths, err := config.DefaultPaths()
	if err != nil {
		return fail(ioSet.Err, err)
	}
	runtimeConfig, err := setup.LoadRuntime(paths)
	if err != nil {
		return fail(ioSet.Err, err)
	}
	if !runtimeConfig.BackendEnabled(backendName) {
		return fail(ioSet.Err, fmt.Errorf("backend %q is disabled; enable it with sclaude setup --backends", backendName))
	}
	envMap := envMap(os.Environ())
	policy := ClassifyBackend(backendName, args, isTTY(os.Stdin), isTTY(os.Stdout), envMap)
	if envMap["STY"] != "" && envMap["SCLAUDE_MANAGED"] == "" && envMap["SCLAUDE_FORCE_NEST"] != "1" &&
		(backendName != "codex" || !codexDirect(args) && isTTY(os.Stdin) && isTTY(os.Stdout)) {
		_, _ = fmt.Fprintln(ioSet.Err, "Already inside GNU Screen; running directly (not tracked by sclaude).")
	}
	if policy == PolicyDirect {
		return executeBackend(runtimeConfig, backendName, args, os.Environ(), true, ioSet)
	}
	manager, err := newManager(paths, runtimeConfig)
	if err != nil {
		return fail(ioSet.Err, err)
	}
	records, errs := manager.List(ctx)
	for _, listErr := range errs {
		_, _ = fmt.Fprintf(ioSet.Err, "warning: %v\n", listErr)
	}
	choice, err := ui.Choose(ioSet.In, ioSet.Out, backendName, records, len(args) > 0)
	if err != nil {
		return fail(ioSet.Err, err)
	}
	switch choice.Action {
	case "cancel":
		return 0
	case "sessions":
		ui.PrintTable(ioSet.Out, records)
		return 0
	case "attach":
		if choice.Record == nil {
			return fail(ioSet.Err, errors.New("missing selected session"))
		}
		mode := "normal"
		if choice.Record.ScreenStatus == session.ScreenAttached {
			_, _ = fmt.Fprint(ioSet.Out, "Session is attached: [m]ulti-attach, [t]akeover, [q]cancel: ")
			line, _ := bufio.NewReader(ioSet.In).ReadString('\n')
			switch strings.ToLower(strings.TrimSpace(line)) {
			case "m":
				mode = "multi"
			case "t":
				mode = "takeover"
			default:
				return 0
			}
		}
		if len(args) > 0 {
			_, _ = fmt.Fprint(ioSet.Out, "Attach and ignore supplied arguments? [y/N] ")
			line, _ := bufio.NewReader(ioSet.In).ReadString('\n')
			if answer := strings.ToLower(strings.TrimSpace(line)); answer != "y" && answer != "yes" {
				return 0
			}
		}
		if err := manager.Attach(ctx, choice.Record.ID, mode); err != nil {
			return fail(ioSet.Err, err)
		}
		return 0
	case "new", "resume":
		topic := os.Getenv("SCLAUDE_TOPIC")
		if choice.Record != nil && topic == "" {
			topic = choice.Record.Topic
		}
		if topic == "" {
			topic, err = ui.PromptTopic(ioSet.In, ioSet.Out)
			if err != nil {
				return fail(ioSet.Err, err)
			}
		}
		cwd, _ := os.Getwd()
		if choice.Record != nil {
			cwd = choice.Record.CWD
		}
		if choice.Action == "resume" {
			args = backend.ResumeArguments(backendName)
		}
		detach := os.Getenv("SCLAUDE_FORCE") == "1" && (!isTTY(os.Stdin) || !isTTY(os.Stdout))
		record, err := manager.Create(ctx, topic, backendName, cwd, args, detach)
		if err != nil {
			return fail(ioSet.Err, err)
		}
		if detach {
			_, _ = fmt.Fprintf(ioSet.Err, "started %s (%s)\n", record.ID[:8], record.Topic)
		}
		return exitCode(record)
	default:
		return fail(ioSet.Err, errors.New("unknown launcher choice"))
	}
}

func runSubcommand(ctx context.Context, args []string, ioSet IO, version, defaultBackend string) int {
	command, commandArgs := args[0], args[1:]
	switch command {
	case "help":
		if ok, code := parseNoArgs(command, commandArgs, ioSet.Err); !ok {
			return code
		}
		printProductUsage(ioSet.Out)
		return 0
	case "version":
		if ok, code := parseNoArgs(command, commandArgs, ioSet.Err); !ok {
			return code
		}
		_, _ = fmt.Fprintln(ioSet.Out, version)
		return 0
	case "setup":
		return commandSetup(ctx, commandArgs, ioSet, defaultBackend)
	case "_install-release":
		return commandInstallRelease(commandArgs, ioSet)
	case "update":
		fs := flag.NewFlagSet(command, flag.ContinueOnError)
		updateOpts := setup.UpdateOptions{}
		fs.StringVar(&updateOpts.Version, "version", "", "release version (default: latest)")
		fs.StringVar(&updateOpts.Repo, "repo", setup.DefaultReleaseRepo, "GitHub owner/repository")
		if ok, code := parseCommandFlags(fs, commandArgs, ioSet.Err); !ok {
			return code
		}
		if fs.NArg() != 0 {
			return commandUsageError(ioSet.Err, command, errors.New("does not accept positional arguments"))
		}
		layout, err := setup.InstalledLayout()
		var ledger setup.InstallLedger
		if err == nil {
			ledger, err = setup.Update(ctx, updateOpts, layout)
		}
		if err != nil {
			return fail(ioSet.Err, err)
		}
		_, _ = fmt.Fprintf(ioSet.Out, "updated sclaude to %s\n", ledger.Current)
		for _, warning := range ledger.Warnings {
			_, _ = fmt.Fprintf(ioSet.Err, "warning: %s\n", warning)
		}
		return 0
	case "rollback":
		if ok, code := parseNoArgs(command, commandArgs, ioSet.Err); !ok {
			return code
		}
		layout, err := setup.InstalledLayout()
		if err == nil {
			_, err = setup.Rollback(layout)
		}
		if err != nil {
			return fail(ioSet.Err, err)
		}
		return 0
	}

	var (
		sessionsOpts sessionsCommandOptions
		newOpts      newCommandOptions
		attachOpts   attachCommandOptions
		stopOpts     stopCommandOptions
		pruneOpts    pruneCommandOptions
		purgeState   bool
		runSessionID string
	)
	switch command {
	case "sessions", "list":
		var ok bool
		var code int
		sessionsOpts, ok, code = parseSessionsCommand(command, commandArgs, ioSet.Err)
		if !ok {
			return code
		}
	case "new":
		var ok bool
		var code int
		newOpts, ok, code = parseNewCommandForBackend(commandArgs, ioSet.Err, defaultBackend)
		if !ok {
			return code
		}
	case "attach":
		var ok bool
		var code int
		attachOpts, ok, code = parseAttachCommand(commandArgs, ioSet.Err)
		if !ok {
			return code
		}
	case "stop":
		var ok bool
		var code int
		stopOpts, ok, code = parseStopCommand(commandArgs, ioSet.Err)
		if !ok {
			return code
		}
	case "prune":
		var ok bool
		var code int
		pruneOpts, ok, code = parsePruneCommand(commandArgs, ioSet.Err)
		if !ok {
			return code
		}
	case "doctor":
		if indexArg(commandArgs, "--") >= 0 {
			return commandUsageError(ioSet.Err, command, errors.New("does not accept arguments"))
		}
		fs := flag.NewFlagSet(command, flag.ContinueOnError)
		jsonOutput := fs.Bool("json", false, "print JSON")
		if ok, code := parseCommandFlags(fs, commandArgs, ioSet.Err); !ok {
			return code
		}
		if fs.NArg() != 0 {
			return commandUsageError(ioSet.Err, command, errors.New("does not accept positional arguments"))
		}
		paths, err := config.DefaultPaths()
		if err != nil {
			return fail(ioSet.Err, err)
		}
		return commandDoctor(ctx, *jsonOutput, ioSet, paths)
	case "verify":
		if ok, code := parseNoArgs(command, commandArgs, ioSet.Err); !ok {
			return code
		}
		paths, err := config.DefaultPaths()
		if err != nil {
			return fail(ioSet.Err, err)
		}
		return commandVerify(ctx, ioSet, paths)
	case "uninstall":
		fs := flag.NewFlagSet(command, flag.ContinueOnError)
		fs.BoolVar(&purgeState, "purge-state", false, "also remove sclaude runtime configuration and session state")
		if ok, code := parseCommandFlags(fs, commandArgs, ioSet.Err); !ok {
			return code
		}
		if fs.NArg() != 0 {
			return commandUsageError(ioSet.Err, command, errors.New("does not accept positional arguments"))
		}
	case "_run-session":
		fs := flag.NewFlagSet(command, flag.ContinueOnError)
		if ok, code := parseCommandFlags(fs, commandArgs, ioSet.Err); !ok {
			return code
		}
		if fs.NArg() != 1 {
			return commandUsageError(ioSet.Err, command, errors.New("requires exactly one session ID"))
		}
		runSessionID = fs.Arg(0)
	default:
		return fail(ioSet.Err, fmt.Errorf("unknown command %q", command))
	}

	paths, err := config.DefaultPaths()
	if err != nil {
		return fail(ioSet.Err, err)
	}
	if command == "uninstall" {
		err := setup.UninstallInstalled(paths, purgeState, func() error {
			runtimeConfig, err := config.Load(paths.ConfigFile)
			if err != nil {
				return fmt.Errorf("cannot prove sessions are inactive: %w", err)
			}
			manager, err := newManager(paths, runtimeConfig)
			if err != nil {
				return fmt.Errorf("cannot prove sessions are inactive: %w", err)
			}
			records, listErrs := manager.List(ctx)
			if len(listErrs) > 0 {
				return fmt.Errorf("cannot prove sessions are inactive: %w", errors.Join(listErrs...))
			}
			for _, record := range records {
				if record.Active() {
					return fmt.Errorf("refusing uninstall while session %s (%s) is active", record.ID[:8], record.Topic)
				}
			}
			return nil
		})
		if err != nil {
			return fail(ioSet.Err, err)
		}
		return 0
	}
	if command == "_run-session" {
		// The creating manager holds the state-root admission lock until this
		// runner marks the backend started. Loading through setup.LoadRuntime
		// would try to acquire the same lock and deadlock startup.
		runtimeConfig, loadErr := config.Load(paths.ConfigFile)
		if loadErr != nil {
			return fail(ioSet.Err, loadErr)
		}
		return runSession(ctx, runSessionID, paths, runtimeConfig, ioSet)
	}
	runtimeConfig, err := setup.LoadRuntime(paths)
	if err != nil {
		return fail(ioSet.Err, err)
	}
	manager, err := newManager(paths, runtimeConfig)
	if err != nil {
		return fail(ioSet.Err, err)
	}
	switch command {
	case "sessions", "list":
		records, errs := manager.List(ctx)
		if sessionsOpts.Active {
			records = filterRecords(records, true)
		} else if sessionsOpts.Stopped {
			records = filterRecords(records, false)
		}
		if sessionsOpts.JSON {
			data, _ := json.MarshalIndent(records, "", "  ")
			_, _ = fmt.Fprintln(ioSet.Out, string(data))
		} else {
			ui.PrintTable(ioSet.Out, records)
		}
		for _, listErr := range errs {
			_, _ = fmt.Fprintf(ioSet.Err, "warning: %v\n", listErr)
		}
		return 0
	case "new":
		if !runtimeConfig.BackendEnabled(newOpts.Backend) {
			return fail(ioSet.Err, fmt.Errorf("backend %q is disabled; enable it with sclaude setup --backends", newOpts.Backend))
		}
		if newOpts.CWD == "" {
			newOpts.CWD, _ = os.Getwd()
		}
		record, err := manager.Create(ctx, newOpts.Topic, newOpts.Backend, newOpts.CWD, newOpts.BackendArgs, newOpts.Detach)
		if err != nil {
			return fail(ioSet.Err, err)
		}
		_, _ = fmt.Fprintf(ioSet.Out, "%s\t%s\n", record.ID[:8], record.Topic)
		return exitCode(record)
	case "attach":
		if err := manager.Attach(ctx, attachOpts.Selector, attachOpts.Mode); err != nil {
			return fail(ioSet.Err, err)
		}
		return 0
	case "stop":
		if !stopOpts.Yes {
			_, _ = fmt.Fprintf(ioSet.Out, "Stop session %s? [y/N] ", stopOpts.Selector)
			line, _ := bufio.NewReader(ioSet.In).ReadString('\n')
			if answer := strings.ToLower(strings.TrimSpace(line)); answer != "y" && answer != "yes" {
				return 0
			}
		}
		if err := manager.Stop(ctx, stopOpts.Selector); err != nil {
			return fail(ioSet.Err, err)
		}
		return 0
	case "prune":
		removed, err := manager.Prune(ctx, pruneOpts.Selectors, pruneOpts.OlderThan, pruneOpts.AllStopped)
		if err != nil {
			return fail(ioSet.Err, err)
		}
		_, _ = fmt.Fprintf(ioSet.Out, "pruned %d session records\n", removed)
		return 0
	default:
		return fail(ioSet.Err, fmt.Errorf("unknown command %q", command))
	}
}

type sessionsCommandOptions struct {
	Active  bool
	Stopped bool
	JSON    bool
}

type newCommandOptions struct {
	Backend     string
	Topic       string
	CWD         string
	Detach      bool
	BackendArgs []string
}

type attachCommandOptions struct {
	Selector string
	Mode     string
}

type stopCommandOptions struct {
	Selector string
	Yes      bool
}

type pruneCommandOptions struct {
	Selectors  []string
	OlderThan  time.Duration
	AllStopped bool
}

func parseCommandFlags(fs *flag.FlagSet, args []string, output io.Writer) (bool, int) {
	var parseOutput bytes.Buffer
	fs.SetOutput(&parseOutput)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = io.Copy(output, &parseOutput)
			return false, 0
		}
		return false, commandUsageError(output, fs.Name(), err)
	}
	return true, 0
}

func parseNoArgs(command string, args []string, output io.Writer) (bool, int) {
	if indexArg(args, "--") >= 0 {
		return false, commandUsageError(output, command, errors.New("does not accept arguments"))
	}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	if ok, code := parseCommandFlags(fs, args, output); !ok {
		return false, code
	}
	if fs.NArg() != 0 {
		return false, commandUsageError(output, command, errors.New("does not accept arguments"))
	}
	return true, 0
}

func parseSessionsCommand(command string, args []string, output io.Writer) (sessionsCommandOptions, bool, int) {
	var opts sessionsCommandOptions
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.BoolVar(&opts.Active, "active", false, "show active sessions")
	fs.BoolVar(&opts.Stopped, "stopped", false, "show stopped sessions")
	fs.BoolVar(&opts.JSON, "json", false, "print JSON")
	if ok, code := parseCommandFlags(fs, args, output); !ok {
		return opts, false, code
	}
	if fs.NArg() != 0 {
		return opts, false, commandUsageError(output, command, errors.New("does not accept positional arguments"))
	}
	if opts.Active && opts.Stopped {
		return opts, false, commandUsageError(output, command, errors.New("--active and --stopped cannot be used together"))
	}
	return opts, true, 0
}

func parseNewCommand(args []string, output io.Writer) (newCommandOptions, bool, int) {
	return parseNewCommandForBackend(args, output, "claude")
}

func parseNewCommandForBackend(args []string, output io.Writer, defaultBackend string) (newCommandOptions, bool, int) {
	opts := newCommandOptions{Backend: defaultBackend}
	prefix, backendArgs := args, []string(nil)
	if boundary := indexArg(args, "--"); boundary >= 0 {
		prefix, backendArgs = args[:boundary], args[boundary+1:]
	}
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	fs.StringVar(&opts.Backend, "backend", opts.Backend, "claude, claudex, or codex")
	fs.StringVar(&opts.Topic, "topic", "", "session topic")
	fs.StringVar(&opts.CWD, "cwd", "", "working directory")
	fs.BoolVar(&opts.Detach, "detach", false, "do not attach")
	if ok, code := parseCommandFlags(fs, prefix, output); !ok {
		return opts, false, code
	}
	if fs.NArg() != 0 {
		return opts, false, commandUsageError(output, "new", errors.New("backend arguments must follow --"))
	}
	if !config.ValidBackend(opts.Backend) {
		return opts, false, commandUsageError(output, "new", errors.New("backend must be claude, claudex, or codex"))
	}
	opts.BackendArgs = append([]string(nil), backendArgs...)
	return opts, true, 0
}

func parseAttachCommand(args []string, output io.Writer) (attachCommandOptions, bool, int) {
	opts := attachCommandOptions{Mode: "normal"}
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	multi := fs.Bool("multi", false, "multi-attach")
	takeover := fs.Bool("takeover", false, "take over attached session")
	if ok, code := parseCommandFlags(fs, intersperseFlags(args), output); !ok {
		return opts, false, code
	}
	if fs.NArg() != 1 {
		return opts, false, commandUsageError(output, "attach", errors.New("requires exactly one session selector"))
	}
	if *multi && *takeover {
		return opts, false, commandUsageError(output, "attach", errors.New("--multi and --takeover cannot be used together"))
	}
	opts.Selector = fs.Arg(0)
	if *multi {
		opts.Mode = "multi"
	} else if *takeover {
		opts.Mode = "takeover"
	}
	return opts, true, 0
}

func parseStopCommand(args []string, output io.Writer) (stopCommandOptions, bool, int) {
	var opts stopCommandOptions
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	fs.BoolVar(&opts.Yes, "yes", false, "do not prompt")
	if ok, code := parseCommandFlags(fs, intersperseFlags(args), output); !ok {
		return opts, false, code
	}
	if fs.NArg() != 1 {
		return opts, false, commandUsageError(output, "stop", errors.New("requires exactly one session selector"))
	}
	opts.Selector = fs.Arg(0)
	return opts, true, 0
}

func parsePruneCommand(args []string, output io.Writer) (pruneCommandOptions, bool, int) {
	var opts pruneCommandOptions
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	olderText := fs.String("older-than", "30d", "minimum stopped age")
	fs.BoolVar(&opts.AllStopped, "all-stopped", false, "prune every stopped session")
	if ok, code := parseCommandFlags(fs, args, output); !ok {
		return opts, false, code
	}
	older, err := parseAge(*olderText)
	if err != nil {
		return opts, false, commandUsageError(output, "prune", err)
	}
	opts.OlderThan = older
	opts.Selectors = append([]string(nil), fs.Args()...)
	return opts, true, 0
}

func indexArg(args []string, wanted string) int {
	for index, arg := range args {
		if arg == wanted {
			return index
		}
	}
	return -1
}

func intersperseFlags(args []string) []string {
	flags := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	boundary := len(args)
	if index := indexArg(args, "--"); index >= 0 {
		boundary = index
	}
	for _, arg := range args[:boundary] {
		if arg != "-" && strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
		} else {
			positionals = append(positionals, arg)
		}
	}
	result := append(flags, positionals...)
	return append(result, args[boundary:]...)
}

func commandSetup(ctx context.Context, args []string, ioSet IO, defaultBackend string) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(ioSet.Err)
	opts := setup.SetupOptions{Input: ioSet.In, Output: ioSet.Out, ErrorOutput: ioSet.Err}
	selectedBackends := ""
	if defaultBackend == "codex" {
		selectedBackends = "codex"
	}
	fs.StringVar(&opts.Backends, "backends", selectedBackends, "comma-separated enabled backends: claude,claudex,codex")
	fs.StringVar(&opts.CodexExecutable, "codex-executable", "", "absolute path to native Codex CLI")
	fs.BoolVar(&opts.Yes, "yes", false, "accept ordinary setup choices")
	fs.BoolVar(&opts.Headless, "headless", false, "use device login")
	fs.BoolVar(&opts.NonInteractive, "non-interactive", false, "do not prompt")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "show dependency changes")
	fs.BoolVar(&opts.SkipCodex, "skip-codex", false, "skip Codex CLI")
	fs.BoolVar(&opts.SkipProxy, "skip-proxy", false, "skip CLIProxyAPI")
	fs.BoolVar(&opts.SkipSmoke, "skip-smoke", false, "skip smoke request")
	fs.BoolVar(&opts.NoModifyPath, "no-modify-path", false, "do not edit PATH startup files")
	fs.StringVar(&opts.BinDir, "bin-dir", "", "installed command directory")
	fs.StringVar(&opts.CLIProxyExecutable, "proxy-executable", "", "CLIProxyAPI executable path")
	fs.StringVar(&opts.CLIProxyConfig, "proxy-config", "", "CLIProxyAPI YAML config path")
	fs.StringVar(&opts.CLIProxyService, "proxy-service", "auto", "service manager: auto, brew, systemd, docker, or none")
	fs.BoolVar(&opts.CLIProxySystemService, "proxy-system-service", false, "use the system systemd unit instead of --user")
	if ok, code := parseCommandFlags(fs, args, ioSet.Err); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return commandUsageError(ioSet.Err, "setup", errors.New("does not accept positional arguments"))
	}
	opts.ManagedProxyExplicit = managedProxyOptionWasSet(fs)
	result, err := setup.RunWorkflow(ctx, opts)
	if err != nil {
		return fail(ioSet.Err, err)
	}
	setup.PrintWorkflowResult(ioSet.Out, result)
	return 0
}

func managedProxyOptionWasSet(fs *flag.FlagSet) bool {
	explicit := false
	fs.Visit(func(setFlag *flag.Flag) {
		switch setFlag.Name {
		case "proxy-executable", "proxy-config", "proxy-service", "proxy-system-service":
			explicit = true
		}
	})
	return explicit
}

func commandVerify(ctx context.Context, ioSet IO, paths config.Paths) int {
	return commandVerifyWithClient(ctx, ioSet, paths, &http.Client{Timeout: 15 * time.Second})
}

func commandVerifyWithClient(ctx context.Context, ioSet IO, paths config.Paths, client *http.Client) int {
	runtimeConfig, err := setup.LoadRuntime(paths)
	if err != nil {
		return fail(ioSet.Err, err)
	}
	if runtimeConfig.ClaudexMode != "managed_proxy" {
		return fail(ioSet.Err, errors.New("CLIProxyAPI verification is not applicable to external claudex mode"))
	}
	credential, err := config.LoadProxyCredential(runtimeConfig.ProxyCredential)
	if err == nil {
		err = setup.ValidateManagedCLIProxyConfig(runtimeConfig.CLIProxyConfig, credential.APIKey)
	}
	if err == nil {
		err = managedsettings.ValidatePrivateFile(runtimeConfig.ManagedSettings, credential.BaseURL, credential.APIKey)
	}
	if err != nil {
		return fail(ioSet.Err, err)
	}
	result, err := setup.VerifyProxy(ctx, client, credential)
	if err != nil {
		return fail(ioSet.Err, err)
	}
	_, _ = fmt.Fprintf(ioSet.Out, "CLIProxyAPI verified: %d models available; required Codex models and Claude aliases present.\n", len(result.Models))
	return 0
}

func commandDoctor(ctx context.Context, jsonOutput bool, ioSet IO, paths config.Paths) int {
	report := setup.RunDoctor(ctx, paths)
	if jsonOutput {
		data, _ := json.MarshalIndent(report, "", "  ")
		_, _ = fmt.Fprintln(ioSet.Out, string(data))
	} else {
		for _, check := range report.Checks {
			state := "OK"
			if !check.OK {
				state = "FAIL"
				if check.Warning {
					state = "WARN"
				}
			}
			_, _ = fmt.Fprintf(ioSet.Out, "%-5s %-28s %s\n", state, check.Name, check.Detail)
		}
	}
	if report.OK() {
		return 0
	}
	return 1
}

func commandInstallRelease(args []string, ioSet IO) int {
	fs := flag.NewFlagSet("_install-release", flag.ContinueOnError)
	fs.SetOutput(ioSet.Err)
	source := fs.String("source", "", "source binary")
	version := fs.String("version", "", "release version")
	binDir := fs.String("bin-dir", "", "command directory")
	if ok, code := parseCommandFlags(fs, args, ioSet.Err); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return commandUsageError(ioSet.Err, "_install-release", errors.New("does not accept positional arguments"))
	}
	if *source == "" || *version == "" {
		return commandUsageError(ioSet.Err, "_install-release", errors.New("requires --source and --version"))
	}
	layout, err := setup.DefaultInstallLayout(*binDir)
	if err == nil {
		_, err = setup.InstallBinary(*source, *version, layout)
	}
	if err != nil {
		return fail(ioSet.Err, err)
	}
	_, _ = fmt.Fprintf(ioSet.Out, "installed sclaude %s in %s\n", *version, layout.BinDir)
	return 0
}

func runSession(ctx context.Context, id string, paths config.Paths, runtimeConfig config.Runtime, ioSet IO) int {
	store := session.NewStore(paths.StateRoot)
	record, err := store.Load(id)
	if err != nil {
		return fail(ioSet.Err, err)
	}
	request, err := store.ConsumeLaunch(id)
	if err != nil {
		_, _ = store.FailLaunch(id, "launch request unavailable", time.Now())
		return fail(ioSet.Err, err)
	}
	if err := os.Chdir(record.CWD); err != nil {
		_, _ = store.FailLaunch(id, "working directory unavailable", time.Now())
		return fail(ioSet.Err, err)
	}
	env := setEnv(os.Environ(), "SCLAUDE_MANAGED", "1")
	env = setEnv(env, "SCLAUDE_BYPASS", "1")
	env = setEnv(env, "SCLAUDE_SESSION_ID", record.ID)
	env = setEnv(env, "SCLAUDE_BACKEND", record.Backend)
	command, commandArgs, commandEnv, err := backend.Command(runtimeConfig, record.Backend, request.Args, env)
	if err != nil {
		_, _ = store.FailLaunch(id, err.Error(), time.Now())
		return fail(ioSet.Err, err)
	}
	// Only this live runner owns the Process handle. No manager reconstructs it
	// from a persisted PID, which could have been reused by an unrelated process.
	if _, err := store.ClaimRunner(id, os.Getpid(), time.Now()); err != nil {
		return fail(ioSet.Err, err)
	}
	cmd := exec.Command(command, commandArgs...)
	cmd.Env, cmd.Dir = commandEnv, record.CWD
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	stopSignals, releaseSignals := runnerSignals()
	defer releaseSignals()
	if err := cmd.Start(); err != nil {
		_, _ = store.FailLaunch(id, err.Error(), time.Now())
		_, _ = store.Finish(id, 1, "", time.Now())
		return fail(ioSet.Err, err)
	}
	if _, err := store.MarkStarted(id, os.Getpid(), cmd.Process.Pid, time.Now()); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_, _ = store.Finish(id, 1, "", time.Now())
		if errors.Is(err, session.ErrSessionNotRunnable) {
			return fail(ioSet.Err, errors.New("session was stopped before the backend started"))
		}
		return fail(ioSet.Err, err)
	}
	nextScreenProbe := time.Now().Add(time.Second)
	err = waitManagedBackend(ctx, cmd, stopSignals, func() (bool, error) {
		current, err := store.Load(id)
		if err != nil || (current.State != session.StateStarting && current.State != session.StateRunning) {
			return true, err
		}
		// A Screen server can die without a forwarded HUP (notably through
		// macOS login). Confirmed socket absence also asks this owning runner
		// to shut down; a transient/ambiguous Screen error does not.
		if time.Now().After(nextScreenProbe) {
			nextScreenProbe = time.Now().Add(time.Second)
			probeCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			sockets, probeErr := (screenpkg.Client{Path: runtimeConfig.ScreenPath}).List(probeCtx)
			cancel()
			if probeErr == nil {
				for _, socket := range sockets {
					if socket.Name == record.ScreenName {
						return false, nil
					}
				}
				return true, nil
			}
		}
		return false, nil
	}, backendStopGrace)
	exit := 0
	termSignal := ""
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exit = commandExitCode(exitErr)
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				termSignal = status.Signal().String()
			}
		} else {
			return fail(ioSet.Err, fmt.Errorf("backend wait did not confirm exit: %w", err))
		}
	}
	if _, finishErr := store.Finish(id, exit, termSignal, time.Now()); finishErr != nil {
		return fail(ioSet.Err, finishErr)
	}
	return exit
}

func newManager(paths config.Paths, runtimeConfig config.Runtime) (session.Manager, error) {
	binary, err := os.Executable()
	if err != nil {
		return session.Manager{}, err
	}
	return session.Manager{
		Store:      session.NewStore(paths.StateRoot),
		Screen:     screenpkg.Client{Path: runtimeConfig.ScreenPath},
		BinaryPath: binary,
		AdmitCreate: func(context.Context) error {
			current, err := config.Load(paths.ConfigFile)
			if err != nil {
				return fmt.Errorf("session creation is no longer admitted: %w", err)
			}
			if !reflect.DeepEqual(current, runtimeConfig) {
				return errors.New("session creation is no longer admitted: runtime configuration changed")
			}
			return nil
		},
	}, nil
}

func executeBackend(runtimeConfig config.Runtime, backendName string, args, env []string, replace bool, ioSet IO) int {
	command, commandArgs, commandEnv, err := backend.Command(runtimeConfig, backendName, args, env)
	if err != nil {
		return fail(ioSet.Err, err)
	}
	if replace {
		if err := syscall.Exec(command, append([]string{command}, commandArgs...), commandEnv); err != nil {
			return fail(ioSet.Err, err)
		}
		return 0
	}
	cmd := exec.Command(command, commandArgs...)
	cmd.Env = commandEnv
	cmd.Stdin, cmd.Stdout, cmd.Stderr = ioSet.In, ioSet.Out, ioSet.Err
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return commandExitCode(exitErr)
		}
		return fail(ioSet.Err, err)
	}
	return 0
}

func isSubcommand(value string) bool {
	switch value {
	case "help", "sessions", "list", "new", "attach", "stop", "prune", "doctor", "verify", "setup", "update", "rollback", "uninstall", "version", "_run-session", "_install-release":
		return true
	}
	return false
}

func commandExitCode(err *exec.ExitError) int {
	if status, ok := err.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return err.ExitCode()
}

func isTTY(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func envMap(env []string) map[string]string {
	result := map[string]string{}
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			result[key] = value
		}
	}
	return result
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(env)+1)
	for _, item := range env {
		if !strings.HasPrefix(item, prefix) {
			result = append(result, item)
		}
	}
	return append(result, prefix+value)
}

func fail(output io.Writer, err error) int {
	_, _ = fmt.Fprintf(output, "sclaude: %v\n", err)
	return 1
}

func usageError(output io.Writer, err error) int {
	_, _ = fmt.Fprintf(output, "sclaude: %v\n", err)
	return 2
}

func commandUsageError(output io.Writer, command string, err error) int {
	_, _ = fmt.Fprintf(output, "sclaude %s: %v\n", command, err)
	return 2
}

func parseAge(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("--older-than requires a duration")
	}
	if strings.HasSuffix(value, "d") {
		days, err := time.ParseDuration(strings.TrimSuffix(value, "d") + "h")
		if err != nil {
			return 0, fmt.Errorf("invalid --older-than duration %q", value)
		}
		if days < 0 {
			return 0, errors.New("--older-than cannot be negative")
		}
		return days * 24, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid --older-than duration %q", value)
	}
	if duration < 0 {
		return 0, errors.New("--older-than cannot be negative")
	}
	return duration, nil
}

func exitCode(record session.Record) int {
	if record.ExitCode != nil {
		return *record.ExitCode
	}
	return 0
}

func contains(args []string, wanted string) bool {
	for _, arg := range args {
		if arg == wanted {
			return true
		}
	}
	return false
}

func firstNonFlag(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}

func nonFlagArgs(args []string) []string {
	var result []string
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			result = append(result, arg)
		}
	}
	return result
}

func filterRecords(records []session.Record, active bool) []session.Record {
	result := make([]session.Record, 0, len(records))
	for _, record := range records {
		if record.Active() == active {
			result = append(result, record)
		}
	}
	return result
}
