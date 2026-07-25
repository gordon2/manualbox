// Package jobs is a SQLite-backed background job queue.
//
// Converting a PDF, OCRing a scan, translating eighty pages, and extracting a
// maintenance plan are all far too slow to run inside an HTTP request, and all
// of them must survive a restart: a user who uploads a manual and closes the tab
// should come back to a finished document, not a lost upload.
//
// So the queue lives in the database rather than in memory. A worker claims a job
// by taking a time-bounded lease on it; if the process dies, the lease lapses and
// another worker picks the job up. Nothing is lost, and no external broker
// (Redis, NATS) is needed for what is fundamentally a single-host application.
package jobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gordon2/manualbox/internal/db"
	"github.com/gordon2/manualbox/internal/db/gen"
)

// State is a job's lifecycle state.
type State string

// Job states. queued and running are live; the rest are terminal.
const (
	StateQueued    State = "queued"
	StateRunning   State = "running"
	StateSucceeded State = "succeeded"
	StateFailed    State = "failed"
	StateCancelled State = "cancelled"
)

// Terminal reports whether no further work will happen for this state.
func (s State) Terminal() bool {
	switch s {
	case StateSucceeded, StateFailed, StateCancelled:
		return true
	default:
		return false
	}
}

var (
	// ErrNotFound is returned when a job ID does not exist.
	ErrNotFound = errors.New("job not found")
	// ErrAlreadyQueued is returned by Enqueue when an identical job (same dedupe
	// key) is already pending. The returned job is the existing one, so callers
	// that only need the work to happen can ignore this error.
	ErrAlreadyQueued = errors.New("an identical job is already queued")
	// ErrNoHandler is recorded on a job whose kind has no registered handler.
	ErrNoHandler = errors.New("no handler registered for job kind")
)

// Job is a unit of background work.
type Job struct {
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
	State   State           `json:"state"`

	Priority int `json:"priority"`

	Progress     float64 `json:"progress"`
	ProgressNote string  `json:"progressNote"`

	Attempts    int    `json:"attempts"`
	MaxAttempts int    `json:"maxAttempts"`
	LastError   string `json:"lastError,omitempty"`

	// Usage accounting, populated by handlers that call a paid provider. This is
	// what lets the UI show what a translation actually cost.
	TokensIn   int64 `json:"tokensIn"`
	TokensOut  int64 `json:"tokensOut"`
	CostMicros int64 `json:"costMicros"`

	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// CostString renders the accumulated cost as a decimal currency amount.
func (j *Job) CostString() string {
	return fmt.Sprintf("%.6f", float64(j.CostMicros)/1_000_000)
}

// Unmarshal decodes the job payload into v.
func (j *Job) Unmarshal(v any) error {
	if len(j.Payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(j.Payload, v); err != nil {
		return fmt.Errorf("job %s: decode payload: %w", j.ID, err)
	}
	return nil
}

// fromRow converts a generated row into a domain job.
func fromRow(r gen.Job) *Job {
	return &Job{
		ID:           r.ID,
		Kind:         r.Kind,
		Payload:      json.RawMessage(r.Payload),
		State:        State(r.State),
		Priority:     int(r.Priority),
		Progress:     r.Progress,
		ProgressNote: r.ProgressNote,
		Attempts:     int(r.Attempts),
		MaxAttempts:  int(r.MaxAttempts),
		LastError:    r.LastError,
		TokensIn:     r.TokensIn,
		TokensOut:    r.TokensOut,
		CostMicros:   r.CostMicros,
		CreatedAt:    db.Time(r.CreatedAt),
		UpdatedAt:    db.Time(r.UpdatedAt),
		StartedAt:    db.TimePtr(r.StartedAt),
		FinishedAt:   db.TimePtr(r.FinishedAt),
	}
}
