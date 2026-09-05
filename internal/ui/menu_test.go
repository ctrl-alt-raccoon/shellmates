package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ctrl-alt-raccoon/shellmates/internal/session"
)

func TestStoppedSessionOffersNativeResumeOrNew(t *testing.T) {
	record := session.Record{ID: strings.Repeat("a", 32), Topic: "work", Backend: "codex", State: session.StateStopped}
	for _, test := range []struct{ input, action string }{{"1\nr\n", "resume"}, {"1\nn\n", "new"}, {"1\nq\n", "cancel"}} {
		var output bytes.Buffer
		choice, err := Choose(strings.NewReader(test.input), &output, "codex", []session.Record{record}, true)
		if err != nil || choice.Action != test.action {
			t.Fatalf("choice=%+v err=%v", choice, err)
		}
		if !strings.Contains(output.String(), "not tied to this Screen record") {
			t.Fatal("resume identity distinction missing")
		}
		if !strings.Contains(output.String(), "Shellmates — codex") {
			t.Fatal("Shellmates heading or selected backend missing")
		}
	}
}

func TestStoppingSessionDoesNotOfferAttach(t *testing.T) {
	record := session.Record{Backend: "codex", State: session.StateStopping, ScreenStatus: session.ScreenDetached}
	choice, err := Choose(strings.NewReader("1\n"), &bytes.Buffer{}, "codex", []session.Record{record}, false)
	if err != nil || choice.Action != "cancel" || Status(record) != "Stopping (pending)" {
		t.Fatalf("choice=%+v status=%s err=%v", choice, Status(record), err)
	}
}
