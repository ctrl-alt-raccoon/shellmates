package setup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func systemdExecStartJSON(t *testing.T, path string, argv []string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"type": systemdExecStartSignature,
		"data": []any{
			[]any{path, argv, false, 0, 0, 0, 0, 0, 0, 0},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestInspectCLIProxyServiceIdentityUsesReadOnlyScopedBusctl(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	busctl := writeDoctorExecutable(t, binDir, "busctl")
	busctl, err := filepath.EvalSymlinks(busctl)
	if err != nil {
		t.Fatal(err)
	}
	executable := writeDoctorExecutable(t, dir, "proxy executable")
	configPath := filepath.Join(dir, "proxy config.yaml")
	if err := os.WriteFile(configPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	for _, test := range []struct {
		name          string
		systemService bool
		scope         string
	}{
		{name: "user", scope: "--user"},
		{name: "system", systemService: true, scope: "--system"},
	} {
		t.Run(test.name, func(t *testing.T) {
			want := []string{
				busctl,
				test.scope,
				"--json=short",
				"get-property",
				"org.freedesktop.systemd1",
				cliProxySystemdServiceObjectPath,
				"org.freedesktop.systemd1.Service",
				"ExecStart",
			}
			runner := &recordingRunner{outputs: map[string][]byte{
				strings.Join(want, "\x00"): systemdExecStartJSON(
					t,
					executable,
					[]string{executable, "--config", configPath},
				),
			}}
			if err := inspectCLIProxyServiceIdentity(
				context.Background(),
				runner,
				test.systemService,
				executable,
				configPath,
			); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(runner.outputCommands, [][]string{want}) {
				t.Fatalf("output commands = %q, want %q", runner.outputCommands, [][]string{want})
			}
			if len(runner.commands) != 0 {
				t.Fatalf("mutating commands = %q", runner.commands)
			}
		})
	}
}

func TestInspectCLIProxyServiceIdentityRevalidatesBusctl(t *testing.T) {
	dir := t.TempDir()
	busctlPath := writeDoctorExecutable(t, dir, "busctl")
	busctl, err := trustExecutablePath(busctlPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(busctlPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(busctlPath, []byte("replacement"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	err = inspectCLIProxyServiceIdentityWithBusctl(
		context.Background(),
		runner,
		false,
		busctl,
		filepath.Join(dir, "proxy"),
		filepath.Join(dir, "config.yaml"),
	)
	if err == nil || !strings.Contains(err.Error(), "revalidate busctl executable") {
		t.Fatalf("error = %v", err)
	}
	if len(runner.outputCommands) != 0 || len(runner.commands) != 0 {
		t.Fatalf("commands after replacement = output %q run %q", runner.outputCommands, runner.commands)
	}
}

func TestValidateSystemdExecStartIdentity(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, `proxy executable'"\\`)
	configPath := filepath.Join(dir, `proxy config'"\\.yaml`)
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		argv []string
		want string
	}{
		{name: "separate config", argv: []string{executable, "--config", configPath}},
		{name: "equals config", argv: []string{executable, "--config=" + configPath}},
		{name: "missing config", argv: []string{executable}, want: "exactly one --config"},
		{name: "dangling config", argv: []string{executable, "--config"}, want: "exactly one --config"},
		{name: "duplicate config", argv: []string{executable, "--config", configPath, "--config=" + configPath}, want: "exactly one --config"},
		{name: "config after boundary", argv: []string{executable, "--", "--config", configPath}, want: "exactly one --config"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateSystemdExecStartIdentity(
				systemdExecCommand{path: executable, argv: test.argv},
				executable,
				configPath,
			)
			if test.want == "" && err != nil {
				t.Fatal(err)
			}
			if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestValidateSystemdExecStartIdentityAcceptsFileAliases(t *testing.T) {
	dir := t.TempDir()
	executable := writeDoctorExecutable(t, dir, "cliproxyapi")
	executableSymlink := filepath.Join(dir, "proxy symlink")
	executableHardlink := filepath.Join(dir, "proxy hardlink")
	if err := os.Symlink(executable, executableSymlink); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(executable, executableHardlink); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "proxy.yaml")
	configSymlink := filepath.Join(dir, "config symlink")
	configHardlink := filepath.Join(dir, "config hardlink")
	if err := os.WriteFile(configPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(configPath, configSymlink); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(configPath, configHardlink); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		command    systemdExecCommand
		configPath string
	}{
		{
			name:       "symlinks",
			command:    systemdExecCommand{path: executableSymlink, argv: []string{executableSymlink, "--config", configSymlink}},
			configPath: configPath,
		},
		{
			name:       "hardlinks",
			command:    systemdExecCommand{path: executableHardlink, argv: []string{executableHardlink, "--config", configHardlink}},
			configPath: configPath,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateSystemdExecStartIdentity(test.command, executable, test.configPath); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestValidateSystemdExecStartIdentityRejectsMismatches(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "cliproxyapi")
	otherExecutable := filepath.Join(dir, "other-proxy")
	configPath := filepath.Join(dir, "proxy.yaml")
	otherConfig := filepath.Join(dir, "other.yaml")
	for _, path := range []string{executable, otherExecutable} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{configPath, otherConfig} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name    string
		command systemdExecCommand
		want    string
	}{
		{
			name:    "tuple executable",
			command: systemdExecCommand{path: otherExecutable, argv: []string{executable, "--config", configPath}},
			want:    "executable does not match",
		},
		{
			name:    "argv executable",
			command: systemdExecCommand{path: executable, argv: []string{otherExecutable, "--config", configPath}},
			want:    "executable does not match",
		},
		{
			name:    "config",
			command: systemdExecCommand{path: executable, argv: []string{executable, "--config", otherConfig}},
			want:    "config does not match",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateSystemdExecStartIdentity(test.command, executable, configPath)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestParseSystemdExecStartJSON(t *testing.T) {
	valid := systemdExecStartJSON(t, "/tmp/proxy", []string{"/tmp/proxy", "--config", "/tmp/config"})
	command, err := parseSystemdExecStartJSON(valid)
	if err != nil {
		t.Fatal(err)
	}
	if command.path != "/tmp/proxy" || len(command.argv) != 3 {
		t.Fatalf("command = %+v", command)
	}

	for _, test := range []struct {
		name string
		data string
		want string
	}{
		{name: "malformed", data: "{", want: "parse"},
		{name: "trailing", data: string(valid) + "{}", want: "trailing data"},
		{name: "unknown field", data: `{"type":"a(sasbttttuii)","data":[],"extra":true}`, want: "unknown field"},
		{name: "wrong signature", data: `{"type":"s","data":[]}`, want: "unsupported type"},
		{name: "no commands", data: `{"type":"a(sasbttttuii)","data":[]}`, want: "exactly one"},
		{name: "multiple commands", data: `{"type":"a(sasbttttuii)","data":[[],[]]}`, want: "exactly one"},
		{name: "wrong shape", data: `{"type":"a(sasbttttuii)","data":[["/tmp/proxy",[]]]}`, want: "invalid shape"},
		{name: "empty executable", data: `{"type":"a(sasbttttuii)","data":[["",["/tmp/proxy"],false,0,0,0,0,0,0,0]]}`, want: "executable is invalid"},
		{name: "empty arguments", data: `{"type":"a(sasbttttuii)","data":[["/tmp/proxy",[],false,0,0,0,0,0,0,0]]}`, want: "arguments are invalid"},
		{name: "invalid boolean", data: `{"type":"a(sasbttttuii)","data":[["/tmp/proxy",["/tmp/proxy"],0,0,0,0,0,0,0,0]]}`, want: "ignore-failure field is invalid"},
		{name: "invalid metadata", data: `{"type":"a(sasbttttuii)","data":[["/tmp/proxy",["/tmp/proxy"],false,"0",0,0,0,0,0,0]]}`, want: "metadata is invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseSystemdExecStartJSON([]byte(test.data))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}
