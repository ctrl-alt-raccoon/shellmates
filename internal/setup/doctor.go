package setup

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ctrl-alt-raccoon/sclaude/internal/config"
	"github.com/ctrl-alt-raccoon/sclaude/internal/managedsettings"
	screenpkg "github.com/ctrl-alt-raccoon/sclaude/internal/screen"
)

type Check struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Detail  string `json:"detail"`
	Warning bool   `json:"warning,omitempty"`
}

type DoctorReport struct {
	Platform Platform `json:"platform"`
	Checks   []Check  `json:"checks"`
}

func (r DoctorReport) OK() bool {
	for _, check := range r.Checks {
		if !check.OK && !check.Warning {
			return false
		}
	}
	return true
}

type doctorScreen interface {
	List(context.Context) ([]screenpkg.Socket, error)
	StartDetached(context.Context, string) error
	Stop(context.Context, string) error
}

type DoctorOptions struct {
	Screen         doctorScreen
	Runner         CommandRunner
	HTTPClient     *http.Client
	PollInterval   time.Duration
	PollTimeout    time.Duration
	CleanupTimeout time.Duration
	SessionName    string
}

func RunDoctor(ctx context.Context, paths config.Paths) DoctorReport {
	return RunDoctorWithOptions(ctx, paths, DoctorOptions{})
}

func RunDoctorWithOptions(ctx context.Context, paths config.Paths, opts DoctorOptions) DoctorReport {
	report := DoctorReport{Platform: DetectPlatform()}
	report.Checks = append(report.Checks, Check{
		Name:   "platform",
		OK:     report.Platform.Supported,
		Detail: fmt.Sprintf("%s/%s (%s)", report.Platform.OS, report.Platform.Arch, report.Platform.Distribution),
	})

	runtimeConfig, err := LoadRuntime(paths)
	if err != nil {
		report.Checks = append(report.Checks, Check{Name: "runtime configuration", Detail: sanitizeError(err)})
		return report
	}
	report.Checks = append(report.Checks, Check{Name: "runtime configuration", OK: true, Detail: redactHome(paths.ConfigFile)})

	appendConfiguredExecutableCheck(&report, "Claude Code", runtimeConfig.RealClaude)
	screenOK := appendConfiguredExecutableCheck(&report, "GNU Screen", runtimeConfig.ScreenPath)
	if screenOK {
		if opts.Screen == nil {
			opts.Screen = screenpkg.Client{Path: runtimeConfig.ScreenPath}
		}
		report.Checks = append(report.Checks, checkScreenLifecycle(ctx, opts))
	} else {
		report.Checks = append(report.Checks, notRunCheck("GNU Screen lifecycle", "configured GNU Screen executable is unavailable"))
	}

	switch runtimeConfig.ClaudexMode {
	case "external":
		appendConfiguredExecutableCheck(&report, "claudex", runtimeConfig.RealClaudex)
		return report
	case "managed_proxy":
		appendManagedDoctorChecks(ctx, &report, runtimeConfig, opts)
	}
	return report
}

func appendManagedDoctorChecks(ctx context.Context, report *DoctorReport, runtimeConfig config.Runtime, opts DoctorOptions) {
	credential, err := config.LoadProxyCredential(runtimeConfig.ProxyCredential)
	credentialOK := err == nil
	detail := redactHome(runtimeConfig.ProxyCredential)
	if err != nil {
		detail = sanitizeError(err)
	}
	report.Checks = append(report.Checks, Check{Name: "private proxy credential", OK: credentialOK, Detail: detail})

	managedSettingsOK := false
	proxyConfigOK := false
	if credentialOK {
		err = managedsettings.ValidatePrivateFile(runtimeConfig.ManagedSettings, credential.BaseURL, credential.APIKey)
		managedSettingsOK = err == nil
		detail = redactHome(runtimeConfig.ManagedSettings)
		if err != nil {
			detail = sanitizeError(err)
		}
		report.Checks = append(report.Checks, Check{Name: "private managed settings", OK: managedSettingsOK, Detail: detail})

		err = ValidateManagedCLIProxyConfig(runtimeConfig.CLIProxyConfig, credential.APIKey)
		proxyConfigOK = err == nil
		detail = redactHome(runtimeConfig.CLIProxyConfig)
		if err != nil {
			detail = sanitizeError(err)
		}
		report.Checks = append(report.Checks, Check{Name: "CLIProxyAPI config", OK: proxyConfigOK, Detail: detail})
	} else {
		report.Checks = append(report.Checks,
			notRunCheck("private managed settings", "private proxy credential validation failed"),
			notRunCheck("CLIProxyAPI config", "private proxy credential validation failed"),
		)
	}

	proxyPath, proxyErr := ValidateCLIProxyExecutable(runtimeConfig.CLIProxyExecutable)
	detail = proxyPath
	if proxyErr != nil {
		detail = sanitizeError(proxyErr)
	}
	report.Checks = append(report.Checks, Check{Name: "CLIProxyAPI", OK: proxyErr == nil, Detail: detail})

	manager, managerErr := ResolveServiceManager(runtimeConfig.CLIProxyService)
	if managerErr == nil && manager != runtimeConfig.CLIProxyService {
		managerErr = fmt.Errorf("configured service manager %q did not resolve to itself", runtimeConfig.CLIProxyService)
	}
	detail = manager
	if managerErr != nil {
		detail = sanitizeError(managerErr)
	}
	report.Checks = append(report.Checks, Check{Name: "CLIProxyAPI service manager", OK: managerErr == nil, Detail: detail})

	identityErr := proxyErr
	if identityErr == nil && managerErr == nil {
		switch manager {
		case "systemd":
			runner := opts.Runner
			if runner == nil {
				runner = ExecRunner{}
			}
			outputRunner, ok := runner.(commandOutputRunner)
			if !ok {
				identityErr = errors.New("command runner cannot inspect CLIProxyAPI systemd service identity")
			} else {
				identityErr = inspectCLIProxyServiceIdentity(
					ctx,
					outputRunner,
					runtimeConfig.CLIProxySystemUnit,
					runtimeConfig.CLIProxyExecutable,
					runtimeConfig.CLIProxyConfig,
				)
			}
		default:
			identityErr = ValidateCLIProxyServiceExecutable(manager, runtimeConfig.CLIProxyExecutable)
			if identityErr == nil {
				identityErr = ValidateCLIProxyServiceConfig(manager, runtimeConfig.CLIProxyConfig)
			}
		}
	}
	identityDetail := manager
	if identityErr != nil {
		identityDetail = sanitizeError(identityErr)
	}
	report.Checks = append(report.Checks, Check{Name: "CLIProxyAPI service identity", OK: identityErr == nil, Detail: identityDetail})

	serviceCheck := notRunCheck("CLIProxyAPI service", "service manager validation failed")
	if managerErr == nil {
		serviceCheck = checkDoctorService(ctx, runtimeConfig, manager, opts.Runner)
	}
	report.Checks = append(report.Checks, serviceCheck)

	if !credentialOK || !managedSettingsOK || !proxyConfigOK || proxyErr != nil || managerErr != nil || identityErr != nil || !serviceCheck.OK {
		report.Checks = append(report.Checks,
			notRunCheck("CLIProxyAPI models", "managed proxy prerequisites failed"),
			notRunCheck("CLIProxyAPI messages", "managed proxy prerequisites failed"),
		)
		return
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	models, modelsErr := VerifyProxyModelsEndpoint(ctx, client, credential)
	modelsDetail := fmt.Sprintf("%d models available; required Codex models and Claude aliases present", len(models))
	if modelsErr != nil {
		modelsDetail = sanitizeError(modelsErr)
	}
	report.Checks = append(report.Checks, Check{Name: "CLIProxyAPI models", OK: modelsErr == nil, Detail: modelsDetail})
	if modelsErr != nil {
		report.Checks = append(report.Checks, notRunCheck("CLIProxyAPI messages", "models verification failed"))
		return
	}
	messagesErr := VerifyProxyMessagesEndpoint(ctx, client, credential)
	messagesDetail := "Claude alias inference succeeded"
	if messagesErr != nil {
		messagesDetail = sanitizeError(messagesErr)
	}
	report.Checks = append(report.Checks, Check{Name: "CLIProxyAPI messages", OK: messagesErr == nil, Detail: messagesDetail})
}

func appendConfiguredExecutableCheck(report *DoctorReport, name, path string) bool {
	ok, detail := configuredExecutable(path)
	report.Checks = append(report.Checks, Check{Name: name, OK: ok, Detail: detail})
	return ok
}

func configuredExecutable(path string) (bool, string) {
	info, err := os.Stat(path)
	if err != nil {
		return false, sanitizeError(err)
	}
	if !info.Mode().IsRegular() || !executableByCurrentUser(info) {
		return false, "configured path is not a regular executable file: " + redactHome(path)
	}
	return true, redactHome(path)
}

func checkScreenLifecycle(ctx context.Context, opts DoctorOptions) Check {
	name := opts.SessionName
	if name == "" {
		name = fmt.Sprintf("sclaude-doctor-%d", time.Now().UnixNano())
	}
	if err := opts.Screen.StartDetached(ctx, name); err != nil {
		return Check{Name: "GNU Screen lifecycle", Detail: "start: " + sanitizeError(err)}
	}
	observed, observeErr := waitForDoctorScreen(
		ctx,
		opts.Screen,
		screenpkg.Socket{Name: name},
		true,
		opts.PollInterval,
		opts.PollTimeout,
	)
	cleanupErr := cleanupDoctorScreen(opts, name, observed)
	if observeErr != nil || observed == nil {
		detail := "observe: " + sanitizeError(observeErr)
		if cleanupErr != nil {
			detail += "; cleanup: " + sanitizeError(cleanupErr)
		}
		return Check{Name: "GNU Screen lifecycle", Detail: detail}
	}
	if cleanupErr != nil {
		return Check{Name: "GNU Screen lifecycle", Detail: "cleanup: " + sanitizeError(cleanupErr)}
	}
	return Check{Name: "GNU Screen lifecycle", OK: true, Detail: "observed and removed disposable session " + name}
}

func cleanupDoctorScreen(opts DoctorOptions, name string, observed *screenpkg.Socket) error {
	timeout := opts.CleanupTimeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if observed == nil {
		var err error
		observed, err = waitForDoctorScreen(
			ctx,
			opts.Screen,
			screenpkg.Socket{Name: name},
			true,
			opts.PollInterval,
			timeout,
		)
		if err != nil {
			return fmt.Errorf("identify: %w", err)
		}
	}
	if observed == nil || observed.PID <= 0 || observed.Name != name {
		return errors.New("disposable session identity is invalid")
	}
	selector := fmt.Sprintf("%d.%s", observed.PID, observed.Name)
	if err := opts.Screen.Stop(ctx, selector); err != nil {
		return fmt.Errorf("stop: %w", err)
	}
	remaining, err := waitForDoctorScreen(
		ctx,
		opts.Screen,
		*observed,
		false,
		opts.PollInterval,
		timeout,
	)
	if err != nil {
		return err
	}
	if remaining != nil {
		return errors.New("disposable session remains after cleanup")
	}
	return nil
}

func waitForDoctorScreen(
	ctx context.Context,
	client doctorScreen,
	target screenpkg.Socket,
	present bool,
	interval time.Duration,
	timeout time.Duration,
) (*screenpkg.Socket, error) {
	if interval <= 0 {
		interval = 50 * time.Millisecond
	}
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		sockets, err := client.List(pollCtx)
		if err != nil {
			if pollCtx.Err() != nil {
				return nil, doctorScreenWaitError(ctx, pollCtx, present)
			}
			return nil, err
		}
		var found *screenpkg.Socket
		for index := range sockets {
			socket := sockets[index]
			if socket.Name == target.Name && (target.PID == 0 || socket.PID == target.PID) {
				found = &socket
				break
			}
		}
		if present && found != nil {
			return found, nil
		}
		if !present && found == nil {
			return nil, nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-pollCtx.Done():
			timer.Stop()
			return nil, doctorScreenWaitError(ctx, pollCtx, present)
		case <-timer.C:
		}
	}
}

func doctorScreenWaitError(ctx, pollCtx context.Context, present bool) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(pollCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf(
			"timed out waiting for disposable session to be %s",
			map[bool]string{true: "observable", false: "removed"}[present],
		)
	}
	return pollCtx.Err()
}

func checkDoctorService(ctx context.Context, runtimeConfig config.Runtime, manager string, runner CommandRunner) Check {
	switch manager {
	case "docker", "none":
		return Check{Name: "CLIProxyAPI service", OK: true, Detail: "externally supervised (" + manager + "); endpoint verification still required"}
	case "systemd", "brew":
	}
	if runner == nil {
		runner = ExecRunner{}
	}
	outputRunner, ok := runner.(commandOutputRunner)
	if !ok {
		return Check{Name: "CLIProxyAPI service", Detail: "command runner cannot inspect CLIProxyAPI service state"}
	}
	serviceExecutable, err := prepareServiceExecutable(manager)
	if err != nil {
		return Check{Name: "CLIProxyAPI service", Detail: sanitizeError(err)}
	}
	state, err := inspectDoctorServiceState(
		ctx,
		outputRunner,
		manager,
		runtimeConfig.CLIProxySystemUnit,
		serviceExecutable,
	)
	if err != nil {
		return Check{Name: "CLIProxyAPI service", Detail: sanitizeError(err)}
	}
	scope := manager
	if manager == "systemd" && runtimeConfig.CLIProxySystemUnit {
		scope = "system systemd"
	}
	if !state.active {
		return Check{Name: "CLIProxyAPI service", Detail: "configured " + scope + " service is inactive"}
	}
	if !state.scheduled {
		return Check{Name: "CLIProxyAPI service", Detail: "configured " + scope + " service is active but not enabled or scheduled"}
	}
	return Check{Name: "CLIProxyAPI service", OK: true, Detail: "configured " + scope + " service is active and enabled or scheduled"}
}

func inspectDoctorServiceState(
	ctx context.Context,
	runner commandOutputRunner,
	manager string,
	systemService bool,
	executable trustedExecutable,
) (preparedServiceState, error) {
	if manager != "systemd" || !systemService {
		return prepareServiceState(
			ctx,
			outputRunnerAdapter{runner},
			manager,
			false,
			executable,
		)
	}
	prepared := preparedServiceState{manager: manager, executable: executable}
	executablePath, err := executable.revalidate()
	if err != nil {
		return prepared, fmt.Errorf("revalidate CLIProxyAPI service executable: %w", err)
	}
	output, commandErr := runner.Output(ctx, executablePath, "is-active", "cliproxyapi.service")
	prepared.active, err = parseSystemdActiveState(output, commandErr, "system")
	if err != nil {
		return prepared, err
	}
	output, commandErr = runner.Output(ctx, executablePath, "is-enabled", "cliproxyapi.service")
	prepared.scheduled, err = parseSystemdEnabledState(output, commandErr, "system")
	if err != nil {
		return prepared, err
	}
	return prepared, nil
}

type outputRunnerAdapter struct {
	commandOutputRunner
}

func (outputRunnerAdapter) Run(context.Context, string, ...string) error {
	return nil
}

func notRunCheck(name, reason string) Check {
	return Check{Name: name, Detail: "not run: " + reason}
}

func sanitizeError(err error) string {
	if err == nil {
		return "ok"
	}
	return strings.ReplaceAll(err.Error(), "\n", " ")
}

func redactHome(path string) string {
	home, err := os.UserHomeDir()
	if err == nil {
		if rel, relErr := filepath.Rel(home, path); relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return filepath.Join("~", rel)
		}
	}
	return path
}
