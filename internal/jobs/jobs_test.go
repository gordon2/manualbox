package jobs

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gordon2/manualbox/internal/config"
	"github.com/gordon2/manualbox/internal/db"
	"github.com/gordon2/manualbox/internal/logging"
)

func newQueue(t *testing.T) *Queue {
	t.Helper()
	d, err := db.Open(context.Background(), db.Options{Path: filepath.Join(t.TempDir(), "jobs.db")})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	q := NewQueue(d, logging.Discard())
	t.Cleanup(q.broker.Close)
	return q
}

// testPool returns a pool with timings compressed so lifecycle tests run fast.
func testPool(t *testing.T, q *Queue, workers int) *Pool {
	t.Helper()
	p := NewPool(q, config.Jobs{
		Workers:       workers,
		PollInterval:  5 * time.Millisecond,
		LeaseDuration: 300 * time.Millisecond,
		MaxAttempts:   3,
	}, logging.Discard())
	p.renewInterval = 50 * time.Millisecond
	p.backoff = func(int) time.Duration { return 0 } // no waiting in tests
	return p
}

type payload struct {
	DocumentID string `json:"documentId"`
	Page       int    `json:"page"`
}

func TestEnqueueAndGet(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()

	job, err := q.Enqueue(ctx, "convert", payload{DocumentID: "doc_1", Page: 7}, EnqueueOptions{Priority: 5})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if job.State != StateQueued {
		t.Errorf("State = %q, want queued", job.State)
	}
	if job.Priority != 5 {
		t.Errorf("Priority = %d, want 5", job.Priority)
	}
	if job.MaxAttempts != defaultMaxAttempts {
		t.Errorf("MaxAttempts = %d, want the default %d", job.MaxAttempts, defaultMaxAttempts)
	}

	got, err := q.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	var decoded payload
	if err := got.Unmarshal(&decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.DocumentID != "doc_1" || decoded.Page != 7 {
		t.Errorf("payload round trip = %+v, want {doc_1 7}", decoded)
	}
}

func TestEnqueueRequiresKind(t *testing.T) {
	q := newQueue(t)
	if _, err := q.Enqueue(context.Background(), "", nil, EnqueueOptions{}); err == nil {
		t.Error("Enqueue with an empty kind should fail")
	}
}

func TestGetMissing(t *testing.T) {
	q := newQueue(t)
	if _, err := q.Get(context.Background(), "job_nonexistent"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

// TestEnqueueDedupe covers the "user clicked translate twice" case.
func TestEnqueueDedupe(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	opts := EnqueueOptions{DedupeKey: "doc_1:convert"}

	first, err := q.Enqueue(ctx, "convert", nil, opts)
	if err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}

	second, err := q.Enqueue(ctx, "convert", nil, opts)
	if !errors.Is(err, ErrAlreadyQueued) {
		t.Fatalf("second Enqueue should report ErrAlreadyQueued, got %v", err)
	}
	// The existing job comes back, so a caller that ignores the error still has a
	// valid job to watch.
	if second == nil || second.ID != first.ID {
		t.Errorf("second Enqueue returned %v, want the existing job %s", second, first.ID)
	}

	all, err := q.List(ctx, "", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("%d jobs queued, want 1", len(all))
	}
}

func TestEnqueueDedupeAllowsReRunAfterCompletion(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	opts := EnqueueOptions{DedupeKey: "doc_1:convert"}

	first, err := q.Enqueue(ctx, "convert", nil, opts)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Finish it, as a worker would.
	p := testPool(t, q, 1)
	p.finishSucceeded(ctx, first)

	// The same work must be schedulable again — re-translating after correcting a
	// glossary term is a normal thing to want.
	if _, err := q.Enqueue(ctx, "convert", nil, opts); err != nil {
		t.Errorf("re-enqueue after completion should succeed, got %v", err)
	}
}

func TestClaimIsExclusive(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	p := testPool(t, q, 1)

	for i := range 3 {
		if _, err := q.Enqueue(ctx, "convert", payload{Page: i}, EnqueueOptions{}); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}

	// Claiming repeatedly must never hand out the same job twice.
	seen := map[string]bool{}
	for range 3 {
		job, err := p.claim(ctx, "w1")
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		if job == nil {
			t.Fatal("claim returned nil while jobs remain queued")
		}
		if seen[job.ID] {
			t.Fatalf("job %s was claimed twice", job.ID)
		}
		seen[job.ID] = true
		if job.Attempts != 1 {
			t.Errorf("Attempts = %d after first claim, want 1", job.Attempts)
		}
	}

	// Queue is drained.
	job, err := p.claim(ctx, "w1")
	if err != nil {
		t.Fatalf("claim on empty queue: %v", err)
	}
	if job != nil {
		t.Errorf("claim returned %s from an empty queue", job.ID)
	}
}

func TestClaimConcurrentlyNeverDuplicates(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	p := testPool(t, q, 1)

	const n = 30
	for i := range n {
		if _, err := q.Enqueue(ctx, "convert", payload{Page: i}, EnqueueOptions{}); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}

	var mu sync.Mutex
	seen := map[string]int{}

	var wg sync.WaitGroup
	for w := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				job, err := p.claim(ctx, fmt.Sprintf("w%d", w))
				if err != nil || job == nil {
					return
				}
				mu.Lock()
				seen[job.ID]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(seen) != n {
		t.Errorf("claimed %d distinct jobs, want %d", len(seen), n)
	}
	for jobID, count := range seen {
		if count != 1 {
			t.Errorf("job %s claimed %d times, want exactly 1", jobID, count)
		}
	}
}

func TestClaimRespectsPriority(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	p := testPool(t, q, 1)

	low, _ := q.Enqueue(ctx, "bulk", nil, EnqueueOptions{Priority: 0})
	high, _ := q.Enqueue(ctx, "interactive", nil, EnqueueOptions{Priority: 10})

	first, err := p.claim(ctx, "w1")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	// An interactive request must not wait behind a bulk backfill.
	if first.ID != high.ID {
		t.Errorf("claimed %s (%s), want the higher-priority job %s", first.ID, first.Kind, high.ID)
	}

	second, _ := p.claim(ctx, "w1")
	if second.ID != low.ID {
		t.Errorf("second claim = %s, want %s", second.ID, low.ID)
	}
}

func TestClaimSkipsFutureJobs(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	p := testPool(t, q, 1)

	if _, err := q.Enqueue(ctx, "later", nil, EnqueueOptions{RunAfter: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	job, err := p.claim(ctx, "w1")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if job != nil {
		t.Error("a job scheduled in the future must not be claimed yet")
	}
}

func TestHandlerSuccess(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	p := testPool(t, q, 1)

	var ran atomic.Bool
	p.Register("convert", func(ctx context.Context, job *Job, r Reporter) error {
		ran.Store(true)
		if err := r.Progress(ctx, 0.5, "halfway"); err != nil {
			return err
		}
		return nil
	})

	job, _ := q.Enqueue(ctx, "convert", nil, EnqueueOptions{})
	runPool(t, p, func() bool { return terminal(t, q, job.ID) })

	if !ran.Load() {
		t.Error("handler never ran")
	}
	final := mustGet(t, q, job.ID)
	if final.State != StateSucceeded {
		t.Errorf("State = %q, want succeeded (last error: %s)", final.State, final.LastError)
	}
	if final.Progress != 1 {
		t.Errorf("Progress = %v, want 1 on success", final.Progress)
	}
	if final.FinishedAt == nil {
		t.Error("FinishedAt should be set")
	}
}

func TestHandlerRetriesThenFails(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	p := testPool(t, q, 1)

	var calls atomic.Int32
	p.Register("flaky", func(context.Context, *Job, Reporter) error {
		calls.Add(1)
		return errors.New("provider unavailable")
	})

	job, _ := q.Enqueue(ctx, "flaky", nil, EnqueueOptions{MaxAttempts: 3})
	runPool(t, p, func() bool { return terminal(t, q, job.ID) })

	final := mustGet(t, q, job.ID)
	if final.State != StateFailed {
		t.Errorf("State = %q, want failed", final.State)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("handler ran %d times, want 3 (max attempts)", got)
	}
	if final.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3", final.Attempts)
	}
	if !strings.Contains(final.LastError, "provider unavailable") {
		t.Errorf("LastError = %q, should carry the handler's error", final.LastError)
	}
}

func TestHandlerSucceedsOnRetry(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	p := testPool(t, q, 1)

	var calls atomic.Int32
	p.Register("eventually", func(context.Context, *Job, Reporter) error {
		if calls.Add(1) < 3 {
			return errors.New("temporary")
		}
		return nil
	})

	job, _ := q.Enqueue(ctx, "eventually", nil, EnqueueOptions{MaxAttempts: 5})
	runPool(t, p, func() bool { return terminal(t, q, job.ID) })

	final := mustGet(t, q, job.ID)
	if final.State != StateSucceeded {
		t.Errorf("State = %q, want succeeded after retries", final.State)
	}
	if calls.Load() != 3 {
		t.Errorf("handler ran %d times, want 3", calls.Load())
	}
}

func TestUnregisteredKindFailsWithoutRetrying(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	p := testPool(t, q, 1)

	job, _ := q.Enqueue(ctx, "nobody-handles-this", nil, EnqueueOptions{MaxAttempts: 5})
	runPool(t, p, func() bool { return terminal(t, q, job.ID) })

	final := mustGet(t, q, job.ID)
	if final.State != StateFailed {
		t.Errorf("State = %q, want failed", final.State)
	}
	// A missing handler is a deployment problem; burning five attempts on it is
	// pointless noise.
	if final.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1 — a missing handler must not be retried", final.Attempts)
	}
	if !strings.Contains(final.LastError, "no handler registered") {
		t.Errorf("LastError = %q, want it to name the missing handler", final.LastError)
	}
}

// TestHandlerPanicDoesNotKillTheWorker matters because handlers parse untrusted
// PDFs. One malformed file must fail its own job, not the server.
func TestHandlerPanicDoesNotKillTheWorker(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	p := testPool(t, q, 1)

	p.Register("explodes", func(context.Context, *Job, Reporter) error {
		panic("malformed PDF trailer")
	})
	var okRan atomic.Bool
	p.Register("fine", func(context.Context, *Job, Reporter) error {
		okRan.Store(true)
		return nil
	})

	bad, _ := q.Enqueue(ctx, "explodes", nil, EnqueueOptions{MaxAttempts: 1})
	good, _ := q.Enqueue(ctx, "fine", nil, EnqueueOptions{})

	runPool(t, p, func() bool {
		return terminal(t, q, bad.ID) && terminal(t, q, good.ID)
	})

	badFinal := mustGet(t, q, bad.ID)
	if badFinal.State != StateFailed {
		t.Errorf("panicking job State = %q, want failed", badFinal.State)
	}
	if !strings.Contains(badFinal.LastError, "panicked") {
		t.Errorf("LastError = %q, want it to mention the panic", badFinal.LastError)
	}
	// The worker survived and kept processing.
	if !okRan.Load() {
		t.Error("worker died after a panic; the following job never ran")
	}
	if s := mustGet(t, q, good.ID).State; s != StateSucceeded {
		t.Errorf("following job State = %q, want succeeded", s)
	}
}

func TestProgressAndUsageAreRecorded(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	p := testPool(t, q, 1)

	p.Register("translate", func(ctx context.Context, job *Job, r Reporter) error {
		if err := r.Progress(ctx, 0.25, "translating section 1 of 4"); err != nil {
			return err
		}
		if err := r.Usage(ctx, 1000, 500, 4200); err != nil {
			return err
		}
		return r.Usage(ctx, 200, 100, 800)
	})

	job, _ := q.Enqueue(ctx, "translate", nil, EnqueueOptions{})
	runPool(t, p, func() bool { return terminal(t, q, job.ID) })

	final := mustGet(t, q, job.ID)
	// Usage accumulates across calls, so a multi-request handler reports a total.
	if final.TokensIn != 1200 || final.TokensOut != 600 {
		t.Errorf("tokens = %d in / %d out, want 1200/600", final.TokensIn, final.TokensOut)
	}
	if final.CostMicros != 5000 {
		t.Errorf("CostMicros = %d, want 5000", final.CostMicros)
	}
	if got := final.CostString(); got != "0.005000" {
		t.Errorf("CostString = %q, want 0.005000", got)
	}
}

func TestProgressClampsOutOfRangeValues(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	p := testPool(t, q, 1)

	// A handler passing a percentage instead of a fraction must not fail the job
	// against the database CHECK constraint.
	p.Register("sloppy", func(ctx context.Context, job *Job, r Reporter) error {
		if err := r.Progress(ctx, 42, "percent, not fraction"); err != nil {
			return err
		}
		return r.Progress(ctx, -1, "negative")
	})

	job, _ := q.Enqueue(ctx, "sloppy", nil, EnqueueOptions{MaxAttempts: 1})
	runPool(t, p, func() bool { return terminal(t, q, job.ID) })

	if s := mustGet(t, q, job.ID).State; s != StateSucceeded {
		t.Errorf("State = %q, want succeeded — out-of-range progress should be clamped, not fatal", s)
	}
}

// TestExpiredLeaseIsReclaimed is the crash-safety property: a worker that dies
// without releasing its job must not strand it.
func TestExpiredLeaseIsReclaimed(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	p := testPool(t, q, 1)

	job, _ := q.Enqueue(ctx, "convert", nil, EnqueueOptions{})

	// Simulate a worker that claimed the job and then vanished.
	claimed, err := p.claim(ctx, "doomed-worker")
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v", err)
	}
	if s := mustGet(t, q, job.ID).State; s != StateRunning {
		t.Fatalf("State = %q after claim, want running", s)
	}

	// Advance the clock past the lease.
	p.now = func() time.Time { return time.Now().Add(time.Hour) }

	n, err := p.reclaim(ctx)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if n != 1 {
		t.Errorf("reclaimed %d jobs, want 1", n)
	}

	after := mustGet(t, q, job.ID)
	if after.State != StateQueued {
		t.Errorf("State = %q after reclaim, want queued", after.State)
	}
	if !strings.Contains(after.LastError, "lease expired") {
		t.Errorf("LastError = %q, want it to explain the reclaim", after.LastError)
	}
}

func TestLiveLeaseIsNotReclaimed(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	p := testPool(t, q, 1)

	q.Enqueue(ctx, "convert", nil, EnqueueOptions{})
	if _, err := p.claim(ctx, "healthy-worker"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	n, err := p.reclaim(ctx)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if n != 0 {
		t.Errorf("reclaimed %d jobs while the lease is still valid, want 0", n)
	}
}

// TestShutdownReleasesWithoutBurningAttempt protects long jobs from restarts: a
// deploy in the middle of an eighty-page translation must not cost it a retry.
func TestShutdownReleasesWithoutBurningAttempt(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	p := testPool(t, q, 1)

	started := make(chan struct{})
	p.Register("slow", func(hctx context.Context, job *Job, r Reporter) error {
		close(started)
		<-hctx.Done() // block until the pool shuts down
		return hctx.Err()
	})

	job, _ := q.Enqueue(ctx, "slow", nil, EnqueueOptions{MaxAttempts: 3})

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.Run(runCtx)
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never started")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pool did not shut down")
	}

	after := mustGet(t, q, job.ID)
	if after.State != StateQueued {
		t.Errorf("State = %q after shutdown, want queued so the work is retried", after.State)
	}
	if after.Attempts != 0 {
		t.Errorf("Attempts = %d after a shutdown release, want 0 — a restart must not cost the job a retry", after.Attempts)
	}
}

func TestCancel(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()

	job, _ := q.Enqueue(ctx, "convert", nil, EnqueueOptions{})
	if err := q.Cancel(ctx, job.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if s := mustGet(t, q, job.ID).State; s != StateCancelled {
		t.Errorf("State = %q, want cancelled", s)
	}

	// Cancelling a finished job is a conflict, not a silent no-op.
	if err := q.Cancel(ctx, job.ID); err == nil {
		t.Error("cancelling an already-finished job should error")
	}
	// Cancelling something that does not exist is a not-found.
	if err := q.Cancel(ctx, "job_missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestListAndStats(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()

	a, _ := q.Enqueue(ctx, "convert", nil, EnqueueOptions{})
	q.Enqueue(ctx, "translate", nil, EnqueueOptions{})
	if err := q.Cancel(ctx, a.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	all, err := q.List(ctx, "", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("List returned %d jobs, want 2", len(all))
	}

	queued, err := q.List(ctx, StateQueued, 10)
	if err != nil {
		t.Fatalf("List(queued): %v", err)
	}
	if len(queued) != 1 {
		t.Errorf("List(queued) returned %d, want 1", len(queued))
	}

	active, err := q.Active(ctx)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if len(active) != 1 {
		t.Errorf("Active returned %d, want 1 (the cancelled job is not active)", len(active))
	}

	stats, err := q.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats[StateQueued] != 1 || stats[StateCancelled] != 1 {
		t.Errorf("Stats = %v, want one queued and one cancelled", stats)
	}
}

func TestPurgeFinishedBefore(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()

	done, _ := q.Enqueue(ctx, "convert", nil, EnqueueOptions{})
	if err := q.Cancel(ctx, done.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	pending, _ := q.Enqueue(ctx, "convert", nil, EnqueueOptions{})

	n, err := q.PurgeFinishedBefore(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("PurgeFinishedBefore: %v", err)
	}
	if n != 1 {
		t.Errorf("purged %d jobs, want 1", n)
	}
	// Pending work must never be purged.
	if _, err := q.Get(ctx, pending.ID); err != nil {
		t.Errorf("pending job was purged: %v", err)
	}
}

func TestExponentialBackoff(t *testing.T) {
	var prev time.Duration
	for attempt := 1; attempt <= 6; attempt++ {
		d := ExponentialBackoff(attempt)
		if d <= 0 {
			t.Fatalf("attempt %d: backoff = %v, want positive", attempt, d)
		}
		if d > 10*time.Minute {
			t.Errorf("attempt %d: backoff = %v, exceeds the 10 minute cap", attempt, d)
		}
		if attempt > 1 && d < prev {
			t.Errorf("attempt %d: backoff %v is shorter than the previous %v", attempt, d, prev)
		}
		prev = d
	}

	// Absurd attempt counts must not overflow into a negative or zero delay.
	for _, attempt := range []int{0, -5, 100, 1 << 20} {
		if d := ExponentialBackoff(attempt); d <= 0 || d > 10*time.Minute {
			t.Errorf("ExponentialBackoff(%d) = %v, want a sane bounded delay", attempt, d)
		}
	}

	// Jitter should make repeated calls differ, so retries do not synchronize.
	seen := map[time.Duration]bool{}
	for range 20 {
		seen[ExponentialBackoff(4)] = true
	}
	if len(seen) == 1 {
		t.Error("backoff has no jitter; a batch of failures would retry in lockstep")
	}
}

func TestStateTerminal(t *testing.T) {
	for s, want := range map[State]bool{
		StateQueued:    false,
		StateRunning:   false,
		StateSucceeded: true,
		StateFailed:    true,
		StateCancelled: true,
	} {
		if got := s.Terminal(); got != want {
			t.Errorf("%s.Terminal() = %v, want %v", s, got, want)
		}
	}
}

// --- helpers ---

// runPool runs the pool until done() reports true, then shuts it down.
func runPool(t *testing.T, p *Pool, done func() bool) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		p.Run(ctx)
	}()

	deadline := time.After(10 * time.Second)
	for !done() {
		select {
		case <-deadline:
			cancel()
			<-finished
			t.Fatal("timed out waiting for jobs to finish")
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("pool did not shut down")
	}
}

func mustGet(t *testing.T, q *Queue, jobID string) *Job {
	t.Helper()
	job, err := q.Get(context.Background(), jobID)
	if err != nil {
		t.Fatalf("Get %s: %v", jobID, err)
	}
	return job
}

func terminal(t *testing.T, q *Queue, jobID string) bool {
	t.Helper()
	job, err := q.Get(context.Background(), jobID)
	if err != nil {
		return false
	}
	return job.State.Terminal()
}
