-- name: EnqueueJob :one
INSERT INTO jobs (
    id, kind, payload, state, priority, max_attempts, dedupe_key,
    run_after, created_at, updated_at
) VALUES (?, ?, ?, 'queued', ?, ?, ?, ?, ?, ?)
RETURNING *;

-- ClaimNextJob atomically takes the highest-priority runnable job.
--
-- The UPDATE ... WHERE id = (SELECT ...) shape is what makes the claim atomic:
-- selecting a candidate and then updating it in two statements would let two
-- workers pick the same row. Writes all go through a single-connection pool as
-- well, but this query is correct regardless of that, which matters if a second
-- process ever attaches to the same database.
--
-- attempts is incremented on claim rather than on failure, so a worker that dies
-- hard still burns an attempt and a poison job cannot be retried forever.
-- name: ClaimNextJob :one
UPDATE jobs
SET state       = 'running',
    worker      = ?,
    lease_until = ?,
    attempts    = attempts + 1,
    started_at  = coalesce(started_at, ?),
    updated_at  = ?
WHERE id = (
    -- Aliased so the subquery's columns are unambiguous against the outer UPDATE.
    SELECT j.id FROM jobs AS j
    WHERE j.state = 'queued' AND j.run_after <= ?
    ORDER BY j.priority DESC, j.id
    LIMIT 1
)
RETURNING *;

-- name: UpdateJobProgress :exec
UPDATE jobs
SET progress = ?, progress_note = ?, updated_at = ?
WHERE id = ? AND state = 'running';

-- ExtendLease is the heartbeat for long-running work: translating an eighty-page
-- manual can outlast any sensible lease, so a live worker renews it rather than
-- being reclaimed mid-job.
-- name: ExtendLease :exec
UPDATE jobs
SET lease_until = ?, updated_at = ?
WHERE id = ? AND state = 'running';

-- name: RecordJobUsage :exec
UPDATE jobs
SET tokens_in   = tokens_in + ?,
    tokens_out  = tokens_out + ?,
    cost_micros = cost_micros + ?,
    updated_at  = ?
WHERE id = ?;

-- name: CompleteJob :exec
UPDATE jobs
SET state = 'succeeded', progress = 1.0, progress_note = ?,
    lease_until = NULL, worker = '', finished_at = ?, updated_at = ?
WHERE id = ?;

-- ReleaseJob returns a job to the queue without counting the attempt, used when a
-- worker is shutting down rather than failing. Without the decrement, every
-- restart or deploy would burn one of a job's retries and a long-running
-- conversion could exhaust max_attempts through no fault of its own.
-- name: ReleaseJob :exec
UPDATE jobs
SET state = 'queued',
    attempts = max(attempts - 1, 0),
    run_after = ?, lease_until = NULL, worker = '', updated_at = ?
WHERE id = ? AND state = 'running';

-- RetryJob returns a failed attempt to the queue with a backoff delay.
-- name: RetryJob :exec
UPDATE jobs
SET state = 'queued', last_error = ?, run_after = ?,
    lease_until = NULL, worker = '', updated_at = ?
WHERE id = ?;

-- name: FailJob :exec
UPDATE jobs
SET state = 'failed', last_error = ?,
    lease_until = NULL, worker = '', finished_at = ?, updated_at = ?
WHERE id = ?;

-- name: CancelJob :execrows
UPDATE jobs
SET state = 'cancelled', lease_until = NULL, worker = '', finished_at = ?, updated_at = ?
WHERE id = ? AND state IN ('queued', 'running');

-- ReclaimExpiredLeases returns jobs whose worker died to the queue. This is what
-- makes the queue crash-safe: a killed process loses no work, it is simply
-- picked up again once the lease lapses.
-- name: ReclaimExpiredLeases :execrows
UPDATE jobs
SET state = 'queued', lease_until = NULL, worker = '',
    last_error = 'worker lease expired; job reclaimed',
    updated_at = ?
WHERE state = 'running' AND lease_until IS NOT NULL AND lease_until < ?;

-- name: GetJob :one
SELECT * FROM jobs WHERE id = ?;

-- GetPendingJobByDedupeKey finds the job currently holding a dedupe key, so a
-- rejected duplicate insert can return the existing job instead of an error.
-- name: GetPendingJobByDedupeKey :one
SELECT * FROM jobs
WHERE dedupe_key = ? AND state IN ('queued', 'running')
LIMIT 1;

-- Two separate queries rather than one with an optional filter: sqlc cannot infer
-- the type of a nullable parameter in an "IS NULL OR =" clause and degrades the
-- parameter to interface{}, pushing a type assertion onto the caller.
-- name: ListJobs :many
SELECT * FROM jobs
ORDER BY created_at DESC
LIMIT ?;

-- name: ListJobsByState :many
SELECT * FROM jobs
WHERE state = ?
ORDER BY created_at DESC
LIMIT ?;

-- name: ListActiveJobs :many
SELECT * FROM jobs
WHERE state IN ('queued', 'running')
ORDER BY priority DESC, created_at;

-- name: CountJobsByState :many
SELECT state, CAST(count(*) AS INTEGER) AS count
FROM jobs
GROUP BY state;

-- DeleteFinishedJobsBefore keeps the activity history from growing without bound.
-- name: DeleteFinishedJobsBefore :execrows
DELETE FROM jobs
WHERE state IN ('succeeded', 'failed', 'cancelled') AND finished_at IS NOT NULL AND finished_at < ?;
