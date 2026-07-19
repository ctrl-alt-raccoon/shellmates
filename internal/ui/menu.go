package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ctrl-alt-raccoon/sclaude/internal/session"
)

type Choice struct {
	Action string
	Record *session.Record
}

func Choose(input io.Reader, output io.Writer, backend string, records []session.Record, haveArgs bool) (Choice, error) {
	filtered := make([]session.Record, 0, len(records))
	for _, record := range records {
		if record.Backend == backend {
			filtered = append(filtered, record)
		}
	}
	if len(filtered) == 0 {
		return Choice{Action: "new"}, nil
	}
	_, _ = fmt.Fprintf(output, "sclaude — %s\n\n", backend)
	for i, record := range filtered {
		_, _ = fmt.Fprintf(output, "%d  %-18s %-32s %s\n", i+1, Status(record), truncate(record.Topic, 32), homeRelative(record.CWD))
	}
	_, _ = fmt.Fprintln(output, "\nn  New named session\nm  All sessions\nq  Cancel")
	if haveArgs {
		_, _ = fmt.Fprintln(output, "Supplied Claude arguments apply only to a new session.")
	}
	_, _ = fmt.Fprint(output, "Choice: ")
	line, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && err != io.EOF {
		return Choice{}, err
	}
	value := strings.TrimSpace(line)
	switch value {
	case "n", "N", "":
		return Choice{Action: "new"}, nil
	case "m", "M":
		return Choice{Action: "sessions"}, nil
	case "q", "Q":
		return Choice{Action: "cancel"}, nil
	}
	index, err := strconv.Atoi(value)
	if err != nil || index < 1 || index > len(filtered) {
		return Choice{}, fmt.Errorf("invalid choice %q", value)
	}
	return Choice{Action: "attach", Record: &filtered[index-1]}, nil
}

func PromptTopic(input io.Reader, output io.Writer) (string, error) {
	reader := bufio.NewReader(input)
	for {
		_, _ = fmt.Fprint(output, "Topic (do not include secrets): ")
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", err
		}
		topic, validateErr := session.ValidateTopic(line)
		if validateErr == nil {
			return topic, nil
		}
		_, _ = fmt.Fprintf(output, "%v\n", validateErr)
		if err == io.EOF {
			return "", validateErr
		}
	}
}

func PrintTable(output io.Writer, records []session.Record) {
	_, _ = fmt.Fprintln(output, "ID        STATE               BACKEND  TOPIC                            CWD")
	for _, record := range records {
		_, _ = fmt.Fprintf(output, "%-9s %-19s %-8s %-32s %s\n", record.ID[:min(8, len(record.ID))], Status(record), record.Backend, truncate(record.Topic, 32), homeRelative(record.CWD))
	}
}

func Status(record session.Record) string {
	if record.Active() && record.ScreenStatus != "" {
		return "Running/" + string(record.ScreenStatus)
	}
	if record.State == session.StateFailed {
		return "Failed"
	}
	if record.ExitCode != nil {
		return fmt.Sprintf("Stopped (exit %d)", *record.ExitCode)
	}
	value := string(record.State)
	if value == "" {
		return "Unknown"
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func homeRelative(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(home, path)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.Join("~", rel)
	}
	return path
}

func truncate(value string, width int) string {
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width < 2 {
		return string(runes[:width])
	}
	return string(runes[:width-1]) + "…"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
