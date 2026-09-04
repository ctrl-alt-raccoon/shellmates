package screen

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

type Status string

const (
	Attached Status = "Attached"
	Detached Status = "Detached"
)

type Socket struct {
	PID    int
	Name   string
	Status Status
}

// Controller is the GNU Screen surface used by the session manager.
type Controller interface {
	List(context.Context) ([]Socket, error)
	Start(context.Context, string, string, string, string, string) error
	Attach(context.Context, string, string) error
	Stop(context.Context, string) error
}

type Client struct {
	Path string
}

var (
	socketLine      = regexp.MustCompile(`^\s*([0-9]+)\.([^\s]+)\s+(?:\([^()]+\)\s+)?\((Attached|Detached)\)\s*$`)
	socketCandidate = regexp.MustCompile(`^\s*[0-9]+\.[^\s]+`)
	screenName      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
)

func (c Client) List(ctx context.Context) ([]Socket, error) {
	if strings.TrimSpace(c.Path) == "" {
		return nil, errors.New("screen path is required")
	}
	cmd := exec.CommandContext(ctx, c.Path, "-ls")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	output, err := cmd.CombinedOutput()
	for _, line := range strings.Split(string(output), "\n") {
		if socketCandidate.MatchString(line) && len(ParseList(line)) != 1 {
			return nil, errors.New("screen -ls returned an unrecognized socket line; liveness is unknown")
		}
	}
	sockets := ParseList(string(output))
	if len(sockets) > 0 {
		return sockets, nil
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(string(output))), "no sockets found in ") {
		return nil, nil
	}
	if err != nil {
		return nil, commandError("screen -ls", err, output)
	}
	return nil, errors.New("screen -ls did not confirm any sockets or their absence; liveness is unknown")
}

// ParseList accepts the socket lines emitted by old and current GNU Screen
// releases, including the Screen 4.00.03 bundled with older macOS versions.
func ParseList(output string) []Socket {
	var sockets []Socket
	for _, line := range strings.Split(output, "\n") {
		match := socketLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		pid, err := strconv.Atoi(match[1])
		if err != nil || pid <= 0 {
			continue
		}
		sockets = append(sockets, Socket{PID: pid, Name: match[2], Status: Status(match[3])})
	}
	return sockets
}

func (c Client) Start(ctx context.Context, name, title, cwd, binary, sessionID string) error {
	if strings.TrimSpace(c.Path) == "" {
		return errors.New("screen path is required")
	}
	if err := validateName(name); err != nil {
		return err
	}
	if strings.TrimSpace(binary) == "" || strings.TrimSpace(sessionID) == "" {
		return errors.New("screen start requires binary and session ID")
	}
	cmd := exec.CommandContext(ctx, c.Path, "-dmS", name, "-t", title, binary, "_run-session", sessionID)
	cmd.Dir = cwd
	if output, err := cmd.CombinedOutput(); err != nil {
		return commandError("start screen", err, output)
	}
	return nil
}

// StartDetached starts a disposable detached session that remains alive until
// explicitly stopped. It is used by health checks that need to prove the GNU
// Screen socket lifecycle independently of a managed sclaude session.
func (c Client) StartDetached(ctx context.Context, name string) error {
	if strings.TrimSpace(c.Path) == "" {
		return errors.New("screen path is required")
	}
	if err := validateName(name); err != nil {
		return err
	}
	cmd := exec.CommandContext(
		ctx,
		c.Path,
		"-dmS",
		name,
		"/bin/sh",
		"-c",
		"while :; do sleep 3600; done",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return commandError("start disposable screen", err, output)
	}
	return nil
}

func (c Client) Attach(ctx context.Context, name, mode string) error {
	if strings.TrimSpace(c.Path) == "" {
		return errors.New("screen path is required")
	}
	if err := validateName(name); err != nil {
		return err
	}
	args := []string{"-r", name}
	switch mode {
	case "", "normal":
	case "multi":
		args = []string{"-x", name}
	case "takeover":
		args = []string{"-d", "-r", name}
	default:
		return fmt.Errorf("unknown attach mode %q", mode)
	}
	return runInteractive(exec.CommandContext(ctx, c.Path, args...))
}

func (c Client) Stop(ctx context.Context, name string) error {
	if strings.TrimSpace(c.Path) == "" {
		return errors.New("screen path is required")
	}
	if err := validateName(name); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, c.Path, "-S", name, "-X", "quit")
	if output, err := cmd.CombinedOutput(); err != nil {
		return commandError("stop screen", err, output)
	}
	return nil
}

func validateName(name string) error {
	if !screenName.MatchString(name) {
		return fmt.Errorf("invalid screen name %q", name)
	}
	return nil
}

func commandError(operation string, err error, output []byte) error {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return fmt.Errorf("%s: %w: %s", operation, err, detail)
}

var runInteractive = func(cmd *exec.Cmd) error {
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
