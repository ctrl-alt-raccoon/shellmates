package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/ctrl-alt-raccoon/sclaude/internal/config"
)

type State string

type ScreenStatus string

const (
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateStopping State = "stopping"
	StateStopped  State = "stopped"
	StateFailed   State = "failed"

	ScreenAttached ScreenStatus = "Attached"
	ScreenDetached ScreenStatus = "Detached"
)

type Record struct {
	SchemaVersion    int          `json:"schema_version"`
	ID               string       `json:"id"`
	Topic            string       `json:"topic"`
	TopicSlug        string       `json:"topic_slug"`
	Backend          string       `json:"backend"`
	CWD              string       `json:"cwd"`
	ScreenName       string       `json:"screen_name"`
	CreatedAt        time.Time    `json:"created_at"`
	StartedAt        *time.Time   `json:"started_at,omitempty"`
	UpdatedAt        time.Time    `json:"updated_at"`
	EndedAt          *time.Time   `json:"ended_at,omitempty"`
	LastSeenAt       *time.Time   `json:"last_seen_at,omitempty"`
	State            State        `json:"state"`
	ScreenStatus     ScreenStatus `json:"screen_status,omitempty"`
	ScreenPID        int          `json:"screen_pid,omitempty"`
	RunnerPID        int          `json:"runner_pid,omitempty"`
	BackendPID       int          `json:"backend_pid,omitempty"`
	ShutdownProtocol int          `json:"shutdown_protocol,omitempty"`
	BackendExited    bool         `json:"backend_exited,omitempty"`
	ExitCode         *int         `json:"exit_code,omitempty"`
	TermSignal       string       `json:"term_signal,omitempty"`
	EndReason        string       `json:"end_reason,omitempty"`
	LaunchError      string       `json:"launch_error,omitempty"`
}

type LaunchRequest struct {
	SchemaVersion int      `json:"schema_version"`
	SessionID     string   `json:"session_id"`
	Args          []string `json:"args"`
}

func NewRecord(topic, backend, cwd string, now time.Time) (Record, error) {
	topic, err := ValidateTopic(topic)
	if err != nil {
		return Record{}, err
	}
	if !config.ValidBackend(backend) {
		return Record{}, fmt.Errorf("unknown backend %q", backend)
	}
	if strings.TrimSpace(cwd) == "" {
		return Record{}, errors.New("working directory is required")
	}
	id, err := randomID()
	if err != nil {
		return Record{}, err
	}
	slug := Slugify(topic)
	return Record{
		SchemaVersion: 1,
		ID:            id,
		Topic:         topic,
		TopicSlug:     slug,
		Backend:       backend,
		CWD:           cwd,
		ScreenName:    fmt.Sprintf("sc-%s-%s-%s", backend, slug, id[:8]),
		CreatedAt:     now.UTC(),
		UpdatedAt:     now.UTC(),
		State:         StateStarting,
	}, nil
}

func ValidateTopic(topic string) (string, error) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return "", errors.New("topic cannot be empty")
	}
	if len([]rune(topic)) > 200 {
		return "", errors.New("topic cannot exceed 200 characters")
	}
	for _, r := range topic {
		if unicode.IsControl(r) {
			return "", errors.New("topic cannot contain control characters or newlines")
		}
	}
	return topic, nil
}

func Slugify(topic string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(topic) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
		if b.Len() >= 32 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "topic"
	}
	return slug
}

func (r Record) Active() bool {
	return r.State == StateStarting || r.State == StateRunning || r.State == StateStopping
}

// ExitUnconfirmed is intentionally conservative for older runners. Stored PIDs
// are diagnostic metadata, never authority for sending a signal after a restart.
func (r Record) ExitUnconfirmed() bool {
	if r.ShutdownProtocol == 1 {
		return !r.BackendExited
	}
	return (r.BackendPID > 0 || r.RunnerPID > 0) && r.ExitCode == nil
}

func randomID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}
