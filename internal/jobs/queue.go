package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gordon2/manualbox/internal/db"
	"github.com/gordon2/manualbox/internal/db/gen"
	"github.com/gordon2/manualbox/internal/id"
)

// defaultMaxAttempts applies when a caller does not specify one.
const defaultMaxAttempts = 3

// Queue enqueues and inspects jobs. It does not run them; see [Pool].
type Queue struct {
	db     *db.DB
	log    *slog.Logger
	broker *Broker

	// now is injectable so tests can control backoff and lease timing without
	// sleeping.
	now func() time.Time
}

// NewQueue returns a queue backed by d.
func NewQueue(d *db.DB, log *slog.Logger) *Queue {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Queue{db: d, log: log, broker: NewBroker(), now: time.Now}
}

// Broker returns the progress event broker, which the API's SSE endpoint
// subscribes to.
func (q *Queue) Broker() *Broker { return q.broker }

// EnqueueOptions tunes how a job is scheduled.
type EnqueueOptions struct {
	// Priority orders the queue; higher runs first. An interactive request
	// (translate the section I am reading) should outrank a bulk backfill.
	Priority int
	// MaxAttempts bounds retries. Zero uses the default.
	MaxAttempts int
	// DedupeKey suppresses a duplicate while an identical job is still pending.
	// Use something stable and specific, such as "doc_01H8…:convert".
	DedupeKey string
	// RunAfter delays the job until a given time. Zero means immediately.
	RunAfter time.Time
}

// Enqueue adds a job.
//
// When DedupeKey is set and an identical job is already pending, the existing job
// is returned along with [ErrAlreadyQueued]. Callers that only need the work to
// happen can treat that as success; the pattern exists so that clicking
// "translate" twice does not translate twice.
func (q *Queue) Enqueue(ctx context.Context, kind string, payload any, opts EnqueueOptions) (*Job, error) {
	if kind == "" {
		return nil, errors.New("jobs: kind is required")
	}

	raw := []byte("{}")
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("jobs: encode payload for %s: %w", kind, err)
		}
		raw = encoded
	}

	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = defaultMaxAttempts
	}
	runAfter := opts.RunAfter
	if runAfter.IsZero() {
		runAfter = q.now()
	}

	var dedupe *string
	if opts.DedupeKey != "" {
		dedupe = &opts.DedupeKey
	}

	now := db.Millis(q.now())
	row, err := gen.New(q.db.Write()).EnqueueJob(ctx, gen.EnqueueJobParams{
		ID:          id.New(id.Job),
		Kind:        kind,
		Payload:     string(raw),
		Priority:    int64(opts.Priority),
		MaxAttempts: int64(opts.MaxAttempts),
		DedupeKey:   dedupe,
		RunAfter:    db.Millis(runAfter),
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		// The partial unique index on dedupe_key rejected this insert, which means
		// the same work is already pending. Return that job instead of an error the
		// caller would have to special-case at every call site.
		if opts.DedupeKey != "" && isUniqueViolation(err) {
			if existing, findErr := q.findPending(ctx, opts.DedupeKey); findErr == nil {
				return existing, ErrAlreadyQueued
			}
		}
		return nil, fmt.Errorf("jobs: enqueue %s: %w", kind, err)
	}

	job := fromRow(row)
	q.log.Debug("job enqueued", "id", job.ID, "kind", job.Kind, "priority", job.Priority)
	q.broker.Publish(eventFor(job))
	return job, nil
}

// findPending locates the pending job holding a dedupe key.
func (q *Queue) findPending(ctx context.Context, dedupeKey string) (*Job, error) {
	row, err := gen.New(q.db.Read()).GetPendingJobByDedupeKey(ctx, &dedupeKey)
	if err != nil {
		return nil, err
	}
	return fromRow(row), nil
}

// Get returns one job.
func (q *Queue) Get(ctx context.Context, jobID string) (*Job, error) {
	row, err := gen.New(q.db.Read()).GetJob(ctx, jobID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, jobID)
		}
		return nil, fmt.Errorf("jobs: get %s: %w", jobID, err)
	}
	return fromRow(row), nil
}

// List returns recent jobs, newest first. An empty state lists every state.
func (q *Queue) List(ctx context.Context, state State, limit int) ([]*Job, error) {
	if limit <= 0 {
		limit = 50
	}

	var rows []gen.Job
	var err error
	if state == "" {
		rows, err = gen.New(q.db.Read()).ListJobs(ctx, int64(limit))
	} else {
		rows, err = gen.New(q.db.Read()).ListJobsByState(ctx, gen.ListJobsByStateParams{
			State: string(state),
			Limit: int64(limit),
		})
	}
	if err != nil {
		return nil, fmt.Errorf("jobs: list: %w", err)
	}

	out := make([]*Job, 0, len(rows))
	for i := range rows {
		out = append(out, fromRow(rows[i]))
	}
	return out, nil
}

// Active returns every queued or running job, for the activity view.
func (q *Queue) Active(ctx context.Context) ([]*Job, error) {
	rows, err := gen.New(q.db.Read()).ListActiveJobs(ctx)
	if err != nil {
		return nil, fmt.Errorf("jobs: list active: %w", err)
	}
	out := make([]*Job, 0, len(rows))
	for i := range rows {
		out = append(out, fromRow(rows[i]))
	}
	return out, nil
}

// Cancel marks a pending or running job cancelled.
//
// A running handler is not forcibly stopped; it observes cancellation the next
// time it reports progress. Killing a handler mid-write could leave a document
// half-converted, so cooperative cancellation is the safer contract.
func (q *Queue) Cancel(ctx context.Context, jobID string) error {
	now := db.Millis(q.now())
	n, err := gen.New(q.db.Write()).CancelJob(ctx, gen.CancelJobParams{
		FinishedAt: &now,
		UpdatedAt:  now,
		ID:         jobID,
	})
	if err != nil {
		return fmt.Errorf("jobs: cancel %s: %w", jobID, err)
	}
	if n == 0 {
		// Either the job does not exist or it already finished. Distinguish the
		// two so the API can answer 404 versus 409.
		if _, err := q.Get(ctx, jobID); err != nil {
			return err
		}
		return fmt.Errorf("jobs: %s has already finished", jobID)
	}

	if job, err := q.Get(ctx, jobID); err == nil {
		q.broker.Publish(eventFor(job))
	}
	return nil
}

// Stats counts jobs by state, for the dashboard.
func (q *Queue) Stats(ctx context.Context) (map[State]int64, error) {
	rows, err := gen.New(q.db.Read()).CountJobsByState(ctx)
	if err != nil {
		return nil, fmt.Errorf("jobs: stats: %w", err)
	}
	out := make(map[State]int64, len(rows))
	for _, r := range rows {
		out[State(r.State)] = r.Count
	}
	return out, nil
}

// PurgeFinishedBefore deletes terminal jobs older than cutoff, keeping the
// activity history bounded.
func (q *Queue) PurgeFinishedBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	n, err := gen.New(q.db.Write()).DeleteFinishedJobsBefore(ctx, db.MillisPtr(&cutoff))
	if err != nil {
		return 0, fmt.Errorf("jobs: purge: %w", err)
	}
	return n, nil
}

// isUniqueViolation reports whether err is a SQLite uniqueness failure.
//
// The pure-Go driver does not export a typed constraint error, so this matches on
// the message. It is only used to convert a duplicate into [ErrAlreadyQueued]; if
// the match ever fails the caller sees the raw error rather than wrong behaviour.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint failed")
}
