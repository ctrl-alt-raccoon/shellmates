package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	systemdExecStartSignature        = "a(sasbttttuii)"
	cliProxySystemdServiceObjectPath = "/org/freedesktop/systemd1/unit/cliproxyapi_2eservice"
)

type systemdExecCommand struct {
	path string
	argv []string
}

type busctlProperty struct {
	Type string            `json:"type"`
	Data []json.RawMessage `json:"data"`
}

func inspectCLIProxyServiceIdentity(
	ctx context.Context,
	runner commandOutputRunner,
	systemService bool,
	expectedExecutable string,
	expectedConfig string,
) error {
	busctl, err := prepareCLIProxyServiceIdentityInspector()
	if err != nil {
		return err
	}
	return inspectCLIProxyServiceIdentityWithBusctl(
		ctx,
		runner,
		systemService,
		busctl,
		expectedExecutable,
		expectedConfig,
	)
}

func prepareCLIProxyServiceIdentityInspector() (trustedExecutable, error) {
	busctl, err := resolveTrustedExecutable("busctl", "/usr/bin/busctl", "/bin/busctl")
	if err != nil {
		return trustedExecutable{}, fmt.Errorf("resolve busctl executable: %w", err)
	}
	return busctl, nil
}

func inspectCLIProxyServiceIdentityWithBusctl(
	ctx context.Context,
	runner commandOutputRunner,
	systemService bool,
	busctl trustedExecutable,
	expectedExecutable string,
	expectedConfig string,
) error {
	busctlPath, err := busctl.revalidate()
	if err != nil {
		return fmt.Errorf("revalidate busctl executable: %w", err)
	}
	arguments := []string{"--user"}
	if systemService {
		arguments = []string{"--system"}
	}
	arguments = append(
		arguments,
		"--json=short",
		"get-property",
		"org.freedesktop.systemd1",
		cliProxySystemdServiceObjectPath,
		"org.freedesktop.systemd1.Service",
		"ExecStart",
	)
	output, err := runner.Output(ctx, busctlPath, arguments...)
	if err != nil {
		return fmt.Errorf("inspect CLIProxyAPI systemd service ExecStart: %w", err)
	}
	command, err := parseSystemdExecStartJSON(output)
	if err != nil {
		return err
	}
	return validateSystemdExecStartIdentity(command, expectedExecutable, expectedConfig)
}

func parseSystemdExecStartJSON(data []byte) (systemdExecCommand, error) {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var property busctlProperty
	if err := decoder.Decode(&property); err != nil {
		return systemdExecCommand{}, fmt.Errorf("parse CLIProxyAPI systemd ExecStart: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return systemdExecCommand{}, errors.New("parse CLIProxyAPI systemd ExecStart: trailing data")
	}
	if property.Type != systemdExecStartSignature {
		return systemdExecCommand{}, fmt.Errorf("CLIProxyAPI systemd ExecStart has unsupported type %q", property.Type)
	}
	if len(property.Data) != 1 {
		return systemdExecCommand{}, errors.New("CLIProxyAPI systemd service must have exactly one ExecStart command")
	}
	var fields []json.RawMessage
	if err := json.Unmarshal(property.Data[0], &fields); err != nil {
		return systemdExecCommand{}, errors.New("CLIProxyAPI systemd ExecStart command is invalid")
	}
	if len(fields) != 10 {
		return systemdExecCommand{}, errors.New("CLIProxyAPI systemd ExecStart command has an invalid shape")
	}
	var command systemdExecCommand
	if err := json.Unmarshal(fields[0], &command.path); err != nil || command.path == "" {
		return systemdExecCommand{}, errors.New("CLIProxyAPI systemd ExecStart executable is invalid")
	}
	if err := json.Unmarshal(fields[1], &command.argv); err != nil || len(command.argv) == 0 {
		return systemdExecCommand{}, errors.New("CLIProxyAPI systemd ExecStart arguments are invalid")
	}
	var ignoreFailure bool
	if err := json.Unmarshal(fields[2], &ignoreFailure); err != nil {
		return systemdExecCommand{}, errors.New("CLIProxyAPI systemd ExecStart ignore-failure field is invalid")
	}
	for index := 3; index < len(fields); index++ {
		var err error
		switch {
		case index <= 6:
			_, err = strconv.ParseUint(string(fields[index]), 10, 64)
		case index == 7:
			_, err = strconv.ParseUint(string(fields[index]), 10, 32)
		default:
			_, err = strconv.ParseInt(string(fields[index]), 10, 32)
		}
		if err != nil {
			return systemdExecCommand{}, errors.New("CLIProxyAPI systemd ExecStart metadata is invalid")
		}
	}
	return command, nil
}

func validateSystemdExecStartIdentity(
	command systemdExecCommand,
	expectedExecutable string,
	expectedConfig string,
) error {
	if !sameExecutable(command.path, expectedExecutable) ||
		len(command.argv) == 0 ||
		!sameExecutable(command.argv[0], expectedExecutable) {
		return errors.New("CLIProxyAPI systemd service executable does not match the configured executable")
	}
	var configPath string
	for index := 1; index < len(command.argv); index++ {
		argument := command.argv[index]
		if argument == "--" {
			break
		}
		if argument == "--config" {
			if configPath != "" || index+1 >= len(command.argv) || command.argv[index+1] == "" {
				return errors.New("CLIProxyAPI systemd service must specify exactly one --config path")
			}
			configPath = command.argv[index+1]
			index++
			continue
		}
		if strings.HasPrefix(argument, "--config=") {
			if configPath != "" || strings.TrimPrefix(argument, "--config=") == "" {
				return errors.New("CLIProxyAPI systemd service must specify exactly one --config path")
			}
			configPath = strings.TrimPrefix(argument, "--config=")
		}
	}
	if configPath == "" {
		return errors.New("CLIProxyAPI systemd service must specify exactly one --config path")
	}
	if !sameRegularFile(configPath, expectedConfig) {
		return errors.New("CLIProxyAPI systemd service config does not match the configured proxy config")
	}
	return nil
}
