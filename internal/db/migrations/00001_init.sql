-- M0 foundation: identity, sessions, settings, the blob index, and the job queue.
--
-- Conventions used throughout this schema:
--
--   * Primary keys are prefixed ULIDs (see internal/id). They sort
--     chronologically, so inserts append to the B-tree rather than scattering.
--   * Timestamps are INTEGER Unix milliseconds, UTC. Integers avoid any
--     dependence on driver-side date parsing and sort correctly. To read one in
--     a sqlite3 shell: SELECT datetime(created_at / 1000, 'unixepoch').
--   * Tables are STRICT so a type error is rejected at write time instead of
--     silently storing the wrong thing.

-- +goose Up

-- Household members. The first user is created by the first-run setup flow.
CREATE TABLE users (
    id            TEXT    PRIMARY KEY,
    -- email as entered, for display; email_folded is the lookup key so that
    -- addresses differing only in case cannot both be registered.
    email         TEXT    NOT NULL,
    email_folded  TEXT    NOT NULL UNIQUE,
    display_name  TEXT    NOT NULL DEFAULT '',
    password_hash TEXT    NOT NULL,
    role          TEXT    NOT NULL DEFAULT 'admin'
                          CHECK (role IN ('admin', 'member', 'viewer')),
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    last_login_at INTEGER
) STRICT;

-- Login sessions. Only a hash of the bearer token is stored, so a database
-- dump or backup leak cannot be replayed as a live session.
CREATE TABLE sessions (
    id           TEXT    PRIMARY KEY,
    user_id      TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   BLOB    NOT NULL UNIQUE,
    user_agent   TEXT    NOT NULL DEFAULT '',
    ip           TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL
) STRICT;

CREATE INDEX sessions_user_idx    ON sessions(user_id);
CREATE INDEX sessions_expires_idx ON sessions(expires_at);

-- Instance state that is not user-facing configuration: the cookie signing key,
-- the instance identifier, the ICS feed token. Configuration proper lives in
-- the config file and environment, never here.
CREATE TABLE settings (
    key        TEXT    PRIMARY KEY,
    value      TEXT    NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

-- Index of the content-addressed blob store on disk. The bytes live under
-- <data_dir>/blobs; this table is the metadata and the deduplication key.
CREATE TABLE blobs (
    sha256     TEXT    PRIMARY KEY,
    size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
    media_type TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
) STRICT;

-- Background work: conversion, OCR, language segmentation, translation,
-- extraction. Long work never runs inside a request, and the queue lives in the
-- database so a restart mid-job loses nothing.
CREATE TABLE jobs (
    id            TEXT    PRIMARY KEY,
    kind          TEXT    NOT NULL,
    payload       TEXT    NOT NULL DEFAULT '{}',
    state         TEXT    NOT NULL DEFAULT 'queued'
                          CHECK (state IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
    priority      INTEGER NOT NULL DEFAULT 0,

    -- Progress for the UI's activity view, 0.0 to 1.0 plus a human note.
    progress      REAL    NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 1),
    progress_note TEXT    NOT NULL DEFAULT '',

    attempts      INTEGER NOT NULL DEFAULT 0,
    max_attempts  INTEGER NOT NULL DEFAULT 3,
    last_error    TEXT    NOT NULL DEFAULT '',

    -- Cost accounting, so a paid translation can be reported per job and
    -- totalled per document. Stored in millionths of a currency unit to keep
    -- money out of floating point.
    tokens_in     INTEGER NOT NULL DEFAULT 0,
    tokens_out    INTEGER NOT NULL DEFAULT 0,
    cost_micros   INTEGER NOT NULL DEFAULT 0,

    -- run_after supports retry backoff and future scheduling.
    run_after     INTEGER NOT NULL,
    -- lease_until is set while a worker holds the job. A worker that dies
    -- without releasing it has the job reclaimed once the lease expires, which
    -- is what makes the queue crash-safe.
    lease_until   INTEGER,
    worker        TEXT    NOT NULL DEFAULT '',

    -- dedupe_key prevents queuing the same logical work twice while an earlier
    -- copy is still pending. NULL opts out.
    dedupe_key    TEXT,

    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    started_at    INTEGER,
    finished_at   INTEGER
) STRICT;

-- Claim query: find the highest-priority runnable job. Column order matches the
-- WHERE/ORDER BY of ClaimJob.
CREATE INDEX jobs_claim_idx ON jobs(state, run_after, priority DESC, id);

-- Activity view: most recent jobs, optionally filtered by state.
CREATE INDEX jobs_recent_idx ON jobs(created_at DESC);

-- Reclaim query: find running jobs whose lease has expired.
CREATE INDEX jobs_lease_idx ON jobs(lease_until) WHERE lease_until IS NOT NULL;

-- At most one pending job per dedupe key. Partial so that finished jobs do not
-- block re-running the same work later.
CREATE UNIQUE INDEX jobs_dedupe_idx ON jobs(dedupe_key)
    WHERE dedupe_key IS NOT NULL AND state IN ('queued', 'running');

-- +goose Down
DROP TABLE jobs;
DROP TABLE blobs;
DROP TABLE settings;
DROP TABLE sessions;
DROP TABLE users;
