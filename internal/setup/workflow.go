package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ctrl-alt-raccoon/shellmates/internal/config"
	"github.com/ctrl-alt-raccoon/shellmates/internal/managedsettings"
)

type WorkflowResult struct {
	Config                       config.Runtime
	Proxy                        ProxyConfigResult
	ExternalClaudex              bool
	DryRun                       bool
	ServicePending               bool
	ServiceAdministratorCommands [][]string
	AuthPending                  bool
	AuthenticationCommand        []string
	VerificationSkipped          bool
	Models                       []string
}

const (
	setupStageRetainedBackup  = "retained-proxy-backup"
	setupStageProxyConfig     = "proxy-config"
	setupStageProxyCredential = "proxy-credential"
	setupStageManagedSettings = "managed-settings"
	setupStageServiceEnable   = "service-enable"
	setupStageServiceAction   = "service-action"
	setupStageOAuthReady      = "oauth-ready"
	setupStageOAuth           = "oauth"
	setupStageVerification    = "verification"
	setupStageShellOwnership  = "shell-ownership"
	setupStageRuntime         = "runtime"
)

var afterSetupWorkflowStage func(string) error
var verifySetupProxyModels = WaitForProxyModels
var runSetupDependencies = RunSetup

func completeSetupWorkflowStage(stage string) error {
	if afterSetupWorkflowStage == nil {
		return nil
	}
	if err := afterSetupWorkflowStage(stage); err != nil {
		return fmt.Errorf("complete setup stage %s: %w", stage, err)
	}
	return nil
}

func RunWorkflow(ctx context.Context, opts SetupOptions) (WorkflowResult, error) {
	return runWorkflow(ctx, opts, ExecRunner{Stdin: opts.Input, Stdout: opts.Output, Stderr: opts.ErrorOutput})
}

func runWorkflow(ctx context.Context, opts SetupOptions, runner CommandRunner) (WorkflowResult, error) {
	result := WorkflowResult{}
	if err := ValidateSetupOptions(opts); err != nil {
		return result, err
	}
	paths, err := config.DefaultPaths()
	if err != nil {
		return result, err
	}
	setupLock, err := acquireSetupAdmissionLock(paths.StateRoot, !opts.DryRun)
	if err != nil {
		return result, err
	}
	if setupLock != nil {
		defer setupLock.release()
	}
	if opts.DryRun {
		if setupLock != nil {
			pending, pendingErr := setupTransactionPendingLocked(setupLock)
			if pendingErr != nil {
				return result, fmt.Errorf("inspect interrupted setup transaction: %w", pendingErr)
			}
			if pending {
				return result, errors.New("an interrupted setup transaction requires recovery; rerun setup without --dry-run")
			}
		}
	} else if err := recoverSetupTransactionLocked(setupLock); err != nil {
		return result, fmt.Errorf("recover interrupted setup transaction: %w", err)
	}
	selfExecutables := projectOwnedExecutables()
	if executable, exeErr := os.Executable(); exeErr == nil {
		selfExecutables = append(selfExecutables, executable)
	}
	external, hasExternal := "", false
	if opts.backendSelected("claudex") {
		external, hasExternal = FindExternalClaudex(selfExecutables...)
	}
	managedProxyExplicit := opts.ManagedProxyExplicit ||
		opts.CLIProxyExecutable != "" ||
		opts.CLIProxyConfig != "" ||
		opts.CLIProxySystemService ||
		(opts.CLIProxyService != "" && opts.CLIProxyService != "auto")
	if managedProxyExplicit {
		external, hasExternal = "", false
	}
	dependencyOpts := opts
	dependencyOpts.externalClaudex = hasExternal
	if hasExternal || !opts.backendSelected("claudex") {
		dependencyOpts.SkipProxy = true
	}
	if err := runSetupDependencies(ctx, dependencyOpts); err != nil {
		return result, err
	}
	if opts.DryRun {
		result.DryRun = true
		result.ExternalClaudex = hasExternal
		result.Config.RealClaudex = external
		return result, nil
	}
	preparedShell, err := prepareSetupShell(opts)
	if err != nil {
		return result, err
	}
	realClaude := ""
	if dependencyOpts.needsClaude() {
		realClaude, err = discoverClaudeExecutable()
		if err != nil {
			return result, errors.New("Claude Code is not available after setup")
		}
	}
	screenPath, err := discoverScreenExecutable()
	if err != nil {
		return result, errors.New("GNU Screen is not available after setup")
	}
	runtimeConfig := config.Runtime{
		SchemaVersion:   config.SchemaVersion,
		EnabledBackends: opts.selectedBackends(),
		RealClaude:      realClaude,
		ScreenPath:      screenPath,
		CLIProxyService: "none",
	}
	if !opts.SkipCodex && (opts.Backends == "" || opts.backendSelected("codex")) {
		realCodex, codexErr := configuredCodexExecutable(opts.CodexExecutable)
		if codexErr != nil && (opts.backendSelected("codex") || opts.CodexExecutable != "") {
			return result, fmt.Errorf("Codex CLI is not available after setup: %w", codexErr)
		}
		if codexErr == nil {
			runtimeConfig.RealCodex = realCodex
			if !runtimeConfig.BackendEnabled("codex") {
				runtimeConfig.EnabledBackends = append(runtimeConfig.EnabledBackends, "codex")
			}
		}
	}
	if opts.SkipProxy && !hasExternal {
		if opts.Backends != "" && opts.backendSelected("claudex") {
			return result, errors.New("claudex was selected but no external claudex was found and CLIProxyAPI setup was skipped")
		}
		selected := []string{}
		for _, name := range runtimeConfig.EnabledBackends {
			if name != "claudex" {
				selected = append(selected, name)
			}
		}
		runtimeConfig.EnabledBackends = selected
	}
	if hasExternal || !runtimeConfig.BackendEnabled("claudex") {
		runtimeConfig.ClaudexMode = "disabled"
		if hasExternal {
			runtimeConfig.ClaudexMode = "external"
			runtimeConfig.RealClaudex = external
		}
		transaction, runtimeIndex, err := prepareSetupTransaction(setupLock, paths, runtimeConfig, preparedShell.addFiles)
		if err != nil {
			return result, err
		}
		if err := preparedShell.apply(transaction); err != nil {
			return result, transaction.abort(err)
		}
		if err := completeSetupWorkflowStage(setupStageShellOwnership); err != nil {
			return result, transaction.abort(err)
		}
		if err := finishSetupTransaction(transaction, runtimeIndex, runtimeConfig); err != nil {
			return result, err
		}
		result.Config = runtimeConfig
		result.ExternalClaudex = hasExternal
		return result, nil
	}
	proxyExecutable := opts.CLIProxyExecutable
	if proxyExecutable == "" {
		proxyExecutable, err = DiscoverCLIProxyExecutable()
	} else {
		proxyExecutable, err = ValidateCLIProxyExecutable(proxyExecutable)
	}
	if err != nil {
		return result, err
	}
	proxyIdentity, err := trustExecutablePath(proxyExecutable)
	if err != nil {
		return result, fmt.Errorf("trust CLIProxyAPI executable: %w", err)
	}
	proxyExecutable = proxyIdentity.path()
	proxyConfig := opts.CLIProxyConfig
	if proxyConfig == "" {
		proxyConfig, err = DiscoverCLIProxyConfig()
		if err != nil {
			return result, err
		}
	}
	resolvedManager, err := ResolveServiceManager(opts.CLIProxyService)
	if err != nil {
		return result, err
	}
	if err := ValidateCLIProxyServiceExecutable(resolvedManager, proxyExecutable); err != nil {
		return result, err
	}
	if err := ValidateCLIProxyServiceConfig(resolvedManager, proxyConfig); err != nil {
		return result, err
	}
	preparedProxy, err := prepareProxyConfig(proxyConfig, paths.Credential)
	if err != nil {
		return result, err
	}
	if preparedProxy.result.ChangedConfig {
		preparedProxy.result.BackupPath = uniqueBackupPath(proxyConfig, time.Now().UTC())
	}
	managedSettingsData, err := managedsettings.Encode(
		preparedProxy.credential.BaseURL,
		preparedProxy.credential.APIKey,
	)
	if err != nil {
		return result, err
	}
	runtimeConfig.ClaudexMode = "managed_proxy"
	runtimeConfig.CLIProxyExecutable = proxyExecutable
	runtimeConfig.CLIProxyConfig = proxyConfig
	runtimeConfig.ProxyCredential = paths.Credential
	runtimeConfig.ManagedSettings = paths.ManagedSettings
	runtimeConfig.CLIProxyService = resolvedManager
	runtimeConfig.CLIProxySystemUnit = opts.CLIProxySystemService
	if err := runtimeConfig.Validate(); err != nil {
		return result, err
	}

	result.Config = runtimeConfig
	result.Proxy = preparedProxy.result
	serviceAction := "start"
	if preparedProxy.result.ChangedConfig {
		serviceAction = "restart"
	}
	systemServicePending := resolvedManager == "systemd" && opts.CLIProxySystemService
	serviceExecutable, err := prepareServiceExecutable(resolvedManager)
	if err != nil {
		return result, err
	}
	if resolvedManager == "systemd" {
		enable, enableErr := serviceCommandFor("enable", resolvedManager, opts.CLIProxySystemService, serviceExecutable.path())
		if enableErr != nil {
			return result, enableErr
		}
		if systemServicePending {
			result.ServicePending = true
			result.ServiceAdministratorCommands = append(result.ServiceAdministratorCommands, append([]string{"sudo"}, enable...))
		}
	}
	serviceState, err := prepareServiceState(ctx, runner, resolvedManager, opts.CLIProxySystemService, serviceExecutable)
	if err != nil {
		return result, err
	}
	if systemServicePending {
		service, serviceErr := serviceCommandFor(
			serviceAction,
			resolvedManager,
			true,
			serviceExecutable.path(),
		)
		if serviceErr != nil {
			return result, serviceErr
		}
		if len(service) > 0 {
			result.ServicePending = true
			result.ServiceAdministratorCommands = append(
				result.ServiceAdministratorCommands,
				append([]string{"sudo"}, service...),
			)
		}
	}
	if opts.NonInteractive {
		result.AuthPending = true
		result.AuthenticationCommand = OAuthCommand(proxyExecutable, proxyConfig, opts.Headless)
	} else {
		if opts.Input == nil {
			opts.Input = os.Stdin
		}
		if opts.Output == nil {
			opts.Output = os.Stdout
		}
		if opts.ErrorOutput == nil {
			opts.ErrorOutput = os.Stderr
		}
		if !opts.Yes {
			ok, confirmErr := confirm(opts.Input, opts.Output, "Authenticate the ChatGPT account for CLIProxyAPI now?", true)
			if confirmErr != nil {
				return result, confirmErr
			}
			if !ok {
				result.AuthPending = true
				result.AuthenticationCommand = OAuthCommand(proxyExecutable, proxyConfig, opts.Headless)
			}
		}
	}

	var retainedBackupIndex = -1
	var proxyIndex, credentialIndex, settingsIndex int
	transaction, runtimeIndex, err := prepareSetupTransaction(setupLock, paths, runtimeConfig, func(transaction *setupTransaction) error {
		var addErr error
		if addErr = transaction.setServiceState(serviceState); addErr != nil {
			return addErr
		}
		if preparedProxy.result.BackupPath != "" {
			retainedBackupIndex, addErr = transaction.addFile(preparedProxy.result.BackupPath, preparedProxy.original, 0o600)
			if addErr != nil {
				return addErr
			}
			if transaction.files[retainedBackupIndex].entry.Existed {
				return errors.New("CLIProxyAPI retained backup path appeared during setup preparation")
			}
		}
		proxyIndex, addErr = transaction.addFile(proxyConfig, preparedProxy.updated, 0o600)
		if addErr != nil {
			return addErr
		}
		credentialIndex, addErr = transaction.addFile(paths.Credential, preparedProxy.credentialData, 0o600)
		if addErr != nil {
			return addErr
		}
		settingsIndex, addErr = transaction.addFile(paths.ManagedSettings, managedSettingsData, 0o600)
		if addErr != nil {
			return addErr
		}
		return preparedShell.addFiles(transaction)
	})
	if err != nil {
		return result, err
	}
	for _, stage := range []struct {
		name  string
		index int
	}{
		{setupStageRetainedBackup, retainedBackupIndex},
		{setupStageProxyConfig, proxyIndex},
		{setupStageProxyCredential, credentialIndex},
		{setupStageManagedSettings, settingsIndex},
	} {
		if stage.index < 0 {
			continue
		}
		if err := transaction.applyFile(stage.index); err != nil {
			return result, transaction.abort(err)
		}
		if err := completeSetupWorkflowStage(stage.name); err != nil {
			return result, abortSetupWorkflow(ctx, transaction, serviceState, runner, err, false)
		}
	}
	if resolvedManager == "systemd" && !systemServicePending {
		enable, enableErr := serviceState.command("enable", false)
		if enableErr != nil {
			return result, abortSetupWorkflow(ctx, transaction, serviceState, runner, enableErr, false)
		}
		serviceState.markScheduledTouched()
		if err := transaction.updateServiceState(serviceState, false, false); err != nil {
			return result, abortSetupWorkflow(ctx, transaction, serviceState, runner, err, false)
		}
		if err := runner.Run(ctx, enable[0], enable[1:]...); err != nil {
			return result, abortSetupWorkflow(ctx, transaction, serviceState, runner, fmt.Errorf("enable CLIProxyAPI service: %w", err), false)
		}
		if err := completeSetupWorkflowStage(setupStageServiceEnable); err != nil {
			return result, abortSetupWorkflow(ctx, transaction, serviceState, runner, err, false)
		}
	}
	if !systemServicePending {
		service, serviceErr := serviceState.command(
			serviceAction,
			opts.CLIProxySystemService,
		)
		if serviceErr != nil {
			return result, abortSetupWorkflow(ctx, transaction, serviceState, runner, serviceErr, false)
		}
		if len(service) > 0 {
			serviceState.markActiveTouched()
			if resolvedManager == "brew" {
				serviceState.markScheduledTouched()
			}
			if err := transaction.updateServiceState(serviceState, false, false); err != nil {
				return result, abortSetupWorkflow(ctx, transaction, serviceState, runner, err, false)
			}
			if err := runner.Run(ctx, service[0], service[1:]...); err != nil {
				return result, abortSetupWorkflow(ctx, transaction, serviceState, runner, fmt.Errorf("%s CLIProxyAPI service: %w", serviceAction, err), false)
			}
			if err := completeSetupWorkflowStage(setupStageServiceAction); err != nil {
				return result, abortSetupWorkflow(ctx, transaction, serviceState, runner, err, false)
			}
		}
	}
	if err := completeSetupWorkflowStage(setupStageOAuthReady); err != nil {
		return result, abortSetupWorkflow(ctx, transaction, serviceState, runner, err, false)
	}
	oauthAttempted := false
	oauthCompleted := false
	if !result.AuthPending {
		oauthExecutable, err := proxyIdentity.revalidate()
		if err != nil {
			return result, abortSetupWorkflow(
				ctx,
				transaction,
				serviceState,
				runner,
				fmt.Errorf("revalidate CLIProxyAPI OAuth executable: %w", err),
				false,
			)
		}
		oauthAttempted = true
		if err := transaction.updateServiceState(serviceState, oauthAttempted, oauthCompleted); err != nil {
			return result, abortSetupWorkflow(ctx, transaction, serviceState, runner, err, false)
		}
		oauth := OAuthCommand(oauthExecutable, proxyConfig, opts.Headless)
		if err := runner.Run(ctx, oauth[0], oauth[1:]...); err != nil {
			primary := fmt.Errorf("CLIProxyAPI Codex login: %w", err)
			primary = errors.Join(primary, errors.New("CLIProxyAPI OAuth authentication was attempted; its external state was not inspected and cannot be rolled back automatically"))
			return result, abortSetupWorkflow(ctx, transaction, serviceState, runner, primary, false)
		}
		oauthCompleted = true
		if err := transaction.updateServiceState(serviceState, oauthAttempted, oauthCompleted); err != nil {
			return result, abortSetupWorkflow(ctx, transaction, serviceState, runner, err, oauthCompleted)
		}
		if err := completeSetupWorkflowStage(setupStageOAuth); err != nil {
			return result, abortSetupWorkflow(ctx, transaction, serviceState, runner, err, oauthCompleted)
		}
		if opts.SkipSmoke || result.ServicePending {
			result.VerificationSkipped = true
		} else {
			models, verifyErr := verifySetupProxyModels(ctx, paths.Credential)
			if verifyErr != nil {
				return result, abortSetupWorkflow(ctx, transaction, serviceState, runner, verifyErr, oauthCompleted)
			}
			result.Models = models
			if err := completeSetupWorkflowStage(setupStageVerification); err != nil {
				return result, abortSetupWorkflow(ctx, transaction, serviceState, runner, err, oauthCompleted)
			}
		}
	}
	if err := preparedShell.apply(transaction); err != nil {
		return result, abortSetupWorkflow(ctx, transaction, serviceState, runner, err, oauthCompleted)
	}
	if err := completeSetupWorkflowStage(setupStageShellOwnership); err != nil {
		return result, abortSetupWorkflow(ctx, transaction, serviceState, runner, err, oauthCompleted)
	}
	if err := finishSetupWorkflow(ctx, transaction, serviceState, runner, runtimeIndex, runtimeConfig, oauthCompleted); err != nil {
		return result, err
	}
	return result, nil
}

func prepareSetupTransaction(setupLock *setupAdmissionLock, paths config.Paths, runtimeConfig config.Runtime, addFiles func(*setupTransaction) error) (*setupTransaction, int, error) {
	runtimeData, err := config.EncodeRuntime(runtimeConfig)
	if err != nil {
		return nil, -1, err
	}
	transaction, err := newSetupTransactionLocked(setupLock)
	if err != nil {
		return nil, -1, err
	}
	if addFiles != nil {
		if err := addFiles(transaction); err != nil {
			return nil, -1, err
		}
	}
	runtimeIndex, err := transaction.addFile(paths.ConfigFile, runtimeData, 0o600)
	if err != nil {
		return nil, -1, err
	}
	if err := transaction.persist(); err != nil {
		return nil, -1, err
	}
	return transaction, runtimeIndex, nil
}

func finishSetupTransaction(transaction *setupTransaction, runtimeIndex int, runtimeConfig config.Runtime) error {
	if err := transaction.applyRuntime(runtimeIndex, runtimeConfig); err != nil {
		return transaction.abort(err)
	}
	if err := completeSetupWorkflowStage(setupStageRuntime); err != nil {
		return transaction.abort(err)
	}
	if err := transaction.commit(); err != nil {
		return transaction.reconcileCommitError(err)
	}
	return nil
}

func ConfigureShellPATH(binDir string, opts SetupOptions) ([]ShellEdit, error) {
	plans, err := prepareShellPlans(binDir, opts)
	if err != nil {
		return nil, err
	}
	edits := make([]ShellEdit, 0, len(plans))
	for _, plan := range plans {
		if err := revalidateShellPlan(plan); err != nil {
			return edits, err
		}
		if err := applyShellPlan(plan); err != nil {
			return edits, err
		}
		edits = append(edits, plan.edit())
	}
	return edits, nil
}

func shellQuote(argument string) string {
	return "'" + strings.ReplaceAll(argument, "'", `'"'"'`) + "'"
}

func shellQuoteCommand(arguments []string) string {
	quoted := make([]string, len(arguments))
	for i, argument := range arguments {
		quoted[i] = shellQuote(argument)
	}
	return strings.Join(quoted, " ")
}

func PrintWorkflowResult(output io.Writer, result WorkflowResult) {
	if output == nil {
		output = os.Stdout
	}
	if result.DryRun {
		if result.ExternalClaudex {
			_, _ = fmt.Fprintf(output, "[dry-run] existing opaque claudex would be used at %s; no files or services changed.\n", result.Config.RealClaudex)
		} else {
			_, _ = fmt.Fprintln(output, "[dry-run] setup plan complete; no files or services changed.")
		}
		return
	}
	if result.ExternalClaudex {
		_, _ = fmt.Fprintf(output, "sclaudex will use the existing opaque claudex executable at %s\n", result.Config.RealClaudex)
		return
	}
	if !result.Config.BackendEnabled("claudex") {
		_, _ = fmt.Fprintf(output, "Configured backends: %s. Native harness configuration and authentication were left unchanged.\n", strings.Join(result.Config.Backends(), ", "))
		return
	}
	if result.ServicePending {
		_, _ = fmt.Fprintln(output, "CLIProxyAPI is configured. An administrator must apply the system service changes:")
		for _, command := range result.ServiceAdministratorCommands {
			_, _ = fmt.Fprintf(output, "  %s\n", shellQuoteCommand(command))
		}
	}
	if result.AuthPending {
		_, _ = fmt.Fprintf(output, "Authentication remains: %s\n", shellQuoteCommand(result.AuthenticationCommand))
	}
	if result.ServicePending {
		if result.AuthPending {
			_, _ = fmt.Fprintln(output, "After authentication and the administrator commands complete, run 'sclaude' 'verify'.")
		} else {
			_, _ = fmt.Fprintln(output, "After the administrator commands complete, run 'sclaude' 'verify'.")
		}
		return
	}
	if result.AuthPending {
		_, _ = fmt.Fprintln(output, "After authentication completes, run 'sclaude' 'verify'.")
		return
	}
	if result.VerificationSkipped {
		_, _ = fmt.Fprintln(output, "CLIProxyAPI authentication completed; model verification was skipped.")
		_, _ = fmt.Fprintln(output, "Run 'sclaude' 'verify' to verify the configured proxy.")
		return
	}
	if len(result.Models) > 0 {
		_, _ = fmt.Fprintf(output, "CLIProxyAPI authenticated; %d models available.\n", len(result.Models))
	}
}
