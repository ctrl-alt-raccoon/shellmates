package screen

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseList(t *testing.T) {
	output := `There are screens on:
	1234.sc-claude-topic-aaaaaaaa	(Detached)
    5678.sc-claudex-other-bbbbbbbb   (Attached)
	broken.not-managed	(Dead ???)
2 Sockets in /var/folders/example.
`
	got := ParseList(output)
	want := []Socket{
		{PID: 1234, Name: "sc-claude-topic-aaaaaaaa", Status: Detached},
		{PID: 5678, Name: "sc-claudex-other-bbbbbbbb", Status: Attached},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
}

func TestParseEmpty(t *testing.T) {
	if got := ParseList("No Sockets found in /tmp/screens.\n"); len(got) != 0 {
		t.Fatalf("got=%+v", got)
	}
}

func TestClientListAcceptsOldScreenNoSocketsExit(t *testing.T) {
	client := Client{Path: helperScript(t, `printf '%s\n' 'No Sockets found in /tmp/screens.'; exit 1`)}
	sockets, err := client.List(context.Background())
	if err != nil || len(sockets) != 0 {
		t.Fatalf("sockets=%+v err=%v", sockets, err)
	}
}

func TestClientStartAndStopArguments(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "args")
	script := helperScript(t, `printf '%s\n' "$@" > "$SCREEN_TEST_LOG"`)
	old := os.Getenv("SCREEN_TEST_LOG")
	t.Cleanup(func() { _ = os.Setenv("SCREEN_TEST_LOG", old) })
	if err := os.Setenv("SCREEN_TEST_LOG", logPath); err != nil {
		t.Fatal(err)
	}
	client := Client{Path: script}
	if err := client.Start(context.Background(), "sc-claude-work-12345678", "Work title", t.TempDir(), "/bin/runner", "0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	assertLines(t, logPath, []string{"-dmS", "sc-claude-work-12345678", "-t", "Work title", "/bin/runner", "_run-session", "0123456789abcdef0123456789abcdef"})
	if err := client.StartDetached(context.Background(), "sclaude-doctor-12345678"); err != nil {
		t.Fatal(err)
	}
	assertLines(t, logPath, []string{"-dmS", "sclaude-doctor-12345678", "/bin/sh", "-c", "while :; do sleep 3600; done"})
	if err := client.Stop(context.Background(), "sc-claude-work-12345678"); err != nil {
		t.Fatal(err)
	}
	assertLines(t, logPath, []string{"-S", "sc-claude-work-12345678", "-X", "quit"})
}

func TestAttachModesAndValidation(t *testing.T) {
	original := runInteractive
	t.Cleanup(func() { runInteractive = original })
	var got []string
	runInteractive = func(cmd *exec.Cmd) error {
		got = append([]string(nil), cmd.Args[1:]...)
		return nil
	}
	client := Client{Path: "/usr/bin/screen"}
	for _, test := range []struct {
		mode string
		want []string
	}{
		{"normal", []string{"-r", "sc-test"}},
		{"multi", []string{"-x", "sc-test"}},
		{"takeover", []string{"-d", "-r", "sc-test"}},
	} {
		if err := client.Attach(context.Background(), "sc-test", test.mode); err != nil {
			t.Fatalf("mode %s: %v", test.mode, err)
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Fatalf("mode %s got=%v want=%v", test.mode, got, test.want)
		}
	}
	if err := client.Attach(context.Background(), "bad name", "normal"); err == nil {
		t.Fatal("invalid name accepted")
	}
	if err := client.Attach(context.Background(), "sc-test", "unknown"); err == nil {
		t.Fatal("invalid mode accepted")
	}
}

func helperScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "screen-test")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertLines(t *testing.T, path string, want []string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
}
