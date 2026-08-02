package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/gordon2/manualbox/internal/config"
	"github.com/gordon2/manualbox/internal/db"
	"github.com/gordon2/manualbox/internal/db/gen"
)

// Handler runs one job. Returning nil marks it succeeded; returning an error
// retries it until max attempts are exhausted.
//
// Handlers must be idempotent. A job can run more than once: a worker may be
// killed after doing its work but before recording success, and the reclaimed
// job will run again.
type Handler func(ctx context.Context, job *Job, report Reporter) error

// Reporter lets a handler publish progress and record what a provider call cost.
type Reporter interface {
	// Progress records completion between 0 and 1 with a human-readable note.
	// The note is shown in the activity view, so it should say what is happening
	// ("translating section 4 of 12"), not merely that something is.
	Progress(ctx context.Context, fraction float64, note string) error
	// Usage adds provider token counts and cost to the job's running totals.
	Usage(ctx context.Context, tokensIn, tokensOut, costMicros int64) error
}

// Pool runs jobs from a [Queue].
type Pool struct {
	queue *Queue
	log   *slog.Logger

	mu       sync.RWMutex
	handlers map[string]Handler

	workers       int
	poll          time.Duration
	lease         time.Duration
	renewInterval time.Duration

	now     func() time.Time
	backoff func(attempt int) time.Duration
}

// NewPool returns a pool sized from cfg.
func NewPool(q *Queue, cfg config.Jobs, log *slog.Logger) *Pool {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.LeaseDuration <= 0 {
		cfg.LeaseDuration = 15 * time.Minute
	}

	return &Pool{
		queue:    q,
		log:      log,
		handlers: make(map[string]Handler),
		workers:  cfg.Workers,
		poll:     cfg.PollInterval,
		lease:    cfg.LeaseDuration,
		// Renew well before expiry so a slow database write cannot let a live
		// worker's lease lapse and its job be stolen mid-flight.
		renewInterval: cfg.LeaseDuration / 3,
		now:           q.now,
		backoff:       ExponentialBackoff,
	}
}

// Register associates a handler with a job kind. Registering twice for the same
// kind replaces the earlier handler.
func (p *Pool) Register(kind string, h Handler) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.handlers[kind] = h
}

// handlerFor looks up a handler.
func (p *Pool) handlerFor(kind string) (Handler, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	h, ok := p.handlers[kind]
	return h, ok
}

// Run starts the workers and the lease reclaimer, blocking until ctx is
// cancelled and all in-flight jobs have been released.
func (p *Pool) Run(ctx context.Context) error {
	// Any job left running by a previous process is reclaimed on startup rather
	// than waiting out its lease.
	if n, err := p.reclaim(ctx); err != nil {
		p.log.Warn("reclaiming abandoned jobs failed", "error", err)
	} else if n > 0 {
		p.log.Info("reclaimed jobs abandoned by a previous run", "count", n)
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		p.reclaimLoop(ctx)
	}()

	for i := range p.workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.worker(ctx, fmt.Sprintf("worker-%d", i))
		}()
	}

	p.log.Info("job workers started", "workers", p.workers, "poll", p.poll, "lease", p.lease)
	wg.Wait()
	p.log.Info("job workers stopped")
	return nil
}

// RunOnce claims and runs a single job, reporting whether there was one to run.
//
// It exists so a caller can drive the queue deterministically instead of starting
// workers and waiting for them, which is what makes pipeline tests assert on a
// finished job rather than on a timeout. It goes through the same claim, lease and
// completion path as a real worker, so what it exercises is the real thing —
// including that a handler is idempotent when the job is run again.
func (p *Pool) RunOnce(ctx context.Context) (bool, error) {
	job, err := p.claim(ctx, "run-once")
	if err != nil {
		return false, err
	}
	if job == nil {
		return false, nil
	}
	p.execute(ctx, p.log, job)
	return true, nil
}

// worker claims and runs jobs until ctx is cancelled.
func (p *Pool) worker(ctx context.Context, name string) {
	log := p.log.With("worker", name)

	for {
		if ctx.Err() != nil {
			return
		}

		job, err := p.claim(ctx, name)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return
			}
			log.Error("claiming a job failed", "error", err)
			if !sleepCtx(ctx, p.poll) {
				return
			}
			continue
		case job == nil:
			// Nothing to do. Idle politely rather than spinning.
			if !sleepCtx(ctx, p.poll) {
				return
			}
			continue
		}

		p.execute(ctx, log, job)
	}
}

// claim takes the next runnable job, or returns nil if the queue is empty.
func (p *Pool) claim(ctx context.Context, worker string) (*Job, error) {
	now := p.now()
	nowMS := db.Millis(now)
	leaseUntil := db.Millis(now.Add(p.lease))

	row, err := gen.New(p.queue.db.Write()).ClaimNextJob(ctx, gen.ClaimNextJobParams{
		Worker:     worker,
		LeaseUntil: &leaseUntil,
		StartedAt:  &nowMS,
		UpdatedAt:  nowMS,
		RunAfter:   nowMS,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return fromRow(row), nil
}

// execute runs a claimed job and records the outcome.
func (p *Pool) execute(ctx context.Context, log *slog.Logger, job *Job) {
	log = log.With("job", job.ID, "kind", job.Kind, "attempt", job.Attempts)

	handler, ok := p.handlerFor(job.Kind)
	if !ok {
		// A missing handler is a programming or configuration error, not a
		// transient fault, so retrying would only waste attempts.
		log.Error("no handler registered for job kind")
		p.finishFailed(ctx, job, fmt.Errorf("%w: %s", ErrNoHandler, job.Kind))
		return
	}

	p.queue.broker.Publish(eventFor(job))

	// Keep the lease alive for as long as the handler runs.
	renewCtx, stopRenew := context.WithCancel(ctx)
	defer stopRenew()
	go p.renewLease(renewCtx, log, job.ID)

	report := &reporter{queue: p.queue, job: job}
	start := p.now()

	err := runHandler(ctx, handler, job, report)
	stopRenew()
	elapsed := p.now().Sub(start)

	switch {
	case err == nil:
		log.Info("job succeeded", "duration", elapsed)
		p.finishSucceeded(ctx, job)

	case ctx.Err() != nil && errors.Is(err, ctx.Err()):
		// Shut down mid-job. Put it back without spending an attempt: this is our
		// interruption, not the job's fault.
		log.Info("job released for shutdown", "duration", elapsed)
		p.release(job)

	case job.Attempts >= job.MaxAttempts:
		log.Error("job failed permanently", "error", err, "attempts", job.Attempts, "duration", elapsed)
		p.finishFailed(ctx, job, err)

	default:
		delay := p.backoff(job.Attempts)
		log.Warn("job failed; will retry", "error", err, "retry_in", delay, "duration", elapsed)
		p.retry(ctx, job, err, delay)
	}
}

// runHandler isolates a handler so a panic fails that job instead of taking the
// whole process down with it. One malformed PDF must not stop the server.
func runHandler(ctx context.Context, h Handler, job *Job, report Reporter) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("handler panicked: %v", r)
		}
	}()
	return h(ctx, job, report)
}

// renewLease periodically extends the lease of a running job.
func (p *Pool) renewLease(ctx context.Context, log *slog.Logger, jobID string) {
	ticker := time.NewTicker(p.renewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			until := db.Millis(p.now().Add(p.lease))
			err := gen.New(p.queue.db.Write()).ExtendLease(ctx, gen.ExtendLeaseParams{
				LeaseUntil: &until,
				UpdatedAt:  db.Millis(p.now()),
				ID:         jobID,
			})
			if err != nil && ctx.Err() == nil {
				log.Warn("extending job lease failed", "error", err)
			}
		}
	}
}

func (p *Pool) finishSucceeded(ctx context.Context, job *Job) {
	now := db.Millis(p.now())
	err := gen.New(p.queue.db.Write()).CompleteJob(ctx, gen.CompleteJobParams{
		ProgressNote: job.ProgressNote,
		FinishedAt:   &now,
		UpdatedAt:    now,
		ID:           job.ID,
	})
	if err != nil {
		p.log.Error("recording job success failed", "job", job.ID, "error", err)
		return
	}
	job.State, job.Progress = StateSucceeded, 1
	p.queue.broker.Publish(eventFor(job))
}

func (p *Pool) finishFailed(ctx context.Context, job *Job, cause error) {
	now := db.Millis(p.now())
	err := gen.New(p.queue.db.Write()).FailJob(ctx, gen.FailJobParams{
		LastError:  cause.Error(),
		FinishedAt: &now,
		UpdatedAt:  now,
		ID:         job.ID,
	})
	if err != nil {
		p.log.Error("recording job failure failed", "job", job.ID, "error", err)
		return
	}
	job.State, job.LastError = StateFailed, cause.Error()
	p.queue.broker.Publish(eventFor(job))
}

func (p *Pool) retry(ctx context.Context, job *Job, cause error, delay time.Duration) {
	now := p.now()
	err := gen.New(p.queue.db.Write()).RetryJob(ctx, gen.RetryJobParams{
		LastError: cause.Error(),
		RunAfter:  db.Millis(now.Add(delay)),
		UpdatedAt: db.Millis(now),
		ID:        job.ID,
	})
	if err != nil {
		p.log.Error("rescheduling job failed", "job", job.ID, "error", err)
		return
	}
	job.State, job.LastError = StateQueued, cause.Error()
	p.queue.broker.Publish(eventFor(job))
}

// release requeues a job interrupted by shutdown.
//
// It deliberately uses a fresh context: the pool's context is already cancelled,
// so reusing it would fail the very write that hands the job back.
func (p *Pool) release(job *Job) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 5*time.Second)
	defer cancel()

	now := db.Millis(p.now())
	err := gen.New(p.queue.db.Write()).ReleaseJob(ctx, gen.ReleaseJobParams{
		RunAfter:  now,
		UpdatedAt: now,
		ID:        job.ID,
	})
	if err != nil {
		p.log.Error("releasing job for shutdown failed", "job", job.ID, "error", err)
		return
	}
	job.State = StateQueued
	p.queue.broker.Publish(eventFor(job))
}

// reclaimLoop periodically returns jobs with expired leases to the queue.
func (p *Pool) reclaimLoop(ctx context.Context) {
	ticker := time.NewTicker(p.renewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := p.reclaim(ctx); err != nil {
				if ctx.Err() == nil {
					p.log.Warn("reclaiming expired leases failed", "error", err)
				}
			} else if n > 0 {
				p.log.Warn("reclaimed jobs with expired leases", "count", n)
			}
		}
	}
}

// reclaim requeues every running job whose lease has expired.
func (p *Pool) reclaim(ctx context.Context) (int64, error) {
	now := db.Millis(p.now())
	return gen.New(p.queue.db.Write()).ReclaimExpiredLeases(ctx, gen.ReclaimExpiredLeasesParams{
		UpdatedAt:  now,
		LeaseUntil: &now,
	})
}

// ExponentialBackoff delays retries by 5s, 10s, 20s, … capped at 10 minutes,
// with jitter so a batch of jobs that failed together does not retry in lockstep
// and hammer whatever they were waiting on.
func ExponentialBackoff(attempt int) time.Duration {
	const (
		base     = 5 * time.Second
		maxDelay = 10 * time.Minute
	)
	if attempt < 1 {
		attempt = 1
	}
	// Bound the shift before it can overflow.
	shift := min(attempt-1, 16)
	delay := base << shift
	if delay > maxDelay || delay <= 0 {
		delay = maxDelay
	}
	jitter := time.Duration(rand.Int64N(int64(delay) / 4)) //nolint:gosec // jitter needs no cryptographic randomness
	return delay - jitter
}

// reporter is the [Reporter] handed to handlers.
type reporter struct {
	queue *Queue
	job   *Job
}

func (r *reporter) Progress(ctx context.Context, fraction float64, note string) error {
	// Clamp rather than reject: a handler miscomputing a fraction should not fail
	// the work, and the database CHECK constraint would refuse the write.
	fraction = min(max(fraction, 0), 1)

	now := db.Millis(r.queue.now())
	err := gen.New(r.queue.db.Write()).UpdateJobProgress(ctx, gen.UpdateJobProgressParams{
		Progress:     fraction,
		ProgressNote: note,
		UpdatedAt:    now,
		ID:           r.job.ID,
	})
	if err != nil {
		return fmt.Errorf("jobs: report progress for %s: %w", r.job.ID, err)
	}

	r.job.Progress, r.job.ProgressNote, r.job.UpdatedAt = fraction, note, db.Time(now)
	r.queue.broker.Publish(eventFor(r.job))
	return nil
}

func (r *reporter) Usage(ctx context.Context, tokensIn, tokensOut, costMicros int64) error {
	err := gen.New(r.queue.db.Write()).RecordJobUsage(ctx, gen.RecordJobUsageParams{
		TokensIn:   tokensIn,
		TokensOut:  tokensOut,
		CostMicros: costMicros,
		UpdatedAt:  db.Millis(r.queue.now()),
		ID:         r.job.ID,
	})
	if err != nil {
		return fmt.Errorf("jobs: record usage for %s: %w", r.job.ID, err)
	}
	r.job.TokensIn += tokensIn
	r.job.TokensOut += tokensOut
	r.job.CostMicros += costMicros
	return nil
}

// sleepCtx waits for d, returning false if ctx was cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
