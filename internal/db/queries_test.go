package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/gordon2/manualbox/internal/db/gen"
	"github.com/gordon2/manualbox/internal/id"
)

// These tests exercise the sqlc-generated queries against a real migrated
// database. They are the seam where a schema change and a stale generated type
// would otherwise drift apart silently, so they run the actual SQL rather than
// asserting on Go structs.

func newQueries(t *testing.T) (*gen.Queries, *DB) {
	t.Helper()
	d := open(t)
	return gen.New(d.Write()), d
}

func TestUserQueries(t *testing.T) {
	q, _ := newQueries(t)
	ctx := context.Background()

	n, err := q.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if n != 0 {
		t.Fatalf("fresh database has %d users, want 0 — first-run detection depends on this", n)
	}

	now := Now()
	created, err := q.CreateUser(ctx, gen.CreateUserParams{
		ID:           id.New(id.User),
		Email:        "Owner@Example.com",
		EmailFolded:  "owner@example.com",
		DisplayName:  "Test Owner",
		PasswordHash: "argon2id$fake",
		Role:         "admin",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if created.Email != "Owner@Example.com" {
		t.Errorf("Email = %q, want the original casing preserved for display", created.Email)
	}
	if created.LastLoginAt != nil {
		t.Error("a new user should have no last_login_at")
	}

	// Lookup is by folded address, so case differences resolve to one account.
	got, err := q.GetUserByEmail(ctx, "owner@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("GetUserByEmail returned %q, want %q", got.ID, created.ID)
	}

	// The unique index must reject a second account for the same folded address.
	_, err = q.CreateUser(ctx, gen.CreateUserParams{
		ID:           id.New(id.User),
		Email:        "OWNER@EXAMPLE.COM",
		EmailFolded:  "owner@example.com",
		PasswordHash: "x",
		Role:         "member",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err == nil {
		t.Error("a duplicate folded email must be rejected")
	}

	login := Now()
	if err := q.TouchUserLogin(ctx, gen.TouchUserLoginParams{
		LastLoginAt: &login,
		UpdatedAt:   login,
		ID:          created.ID,
	}); err != nil {
		t.Fatalf("TouchUserLogin: %v", err)
	}
	after, err := q.GetUserByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if after.LastLoginAt == nil || *after.LastLoginAt != login {
		t.Errorf("last_login_at = %v, want %d", after.LastLoginAt, login)
	}
}

func TestGetUserByEmailMissingReturnsNoRows(t *testing.T) {
	q, _ := newQueries(t)

	_, err := q.GetUserByEmail(context.Background(), "nobody@example.com")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("want sql.ErrNoRows for a missing user, got %v", err)
	}
}

func TestSessionQueries(t *testing.T) {
	q, _ := newQueries(t)
	ctx := context.Background()
	user := mustUser(t, q, "a@example.com")

	hash := []byte{0xde, 0xad, 0xbe, 0xef}
	now := Now()
	expires := Millis(time.Now().Add(time.Hour))

	sess, err := q.CreateSession(ctx, gen.CreateSessionParams{
		ID:         id.New(id.Session),
		UserID:     user.ID,
		TokenHash:  hash,
		UserAgent:  "test-agent",
		Ip:         "192.0.2.1",
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  expires,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// One query authenticates the request and returns the user.
	row, err := q.GetSessionByToken(ctx, gen.GetSessionByTokenParams{
		TokenHash: hash,
		ExpiresAt: Now(),
	})
	if err != nil {
		t.Fatalf("GetSessionByToken: %v", err)
	}
	if row.Session.ID != sess.ID {
		t.Errorf("session ID = %q, want %q", row.Session.ID, sess.ID)
	}
	if row.User.ID != user.ID {
		t.Errorf("joined user ID = %q, want %q", row.User.ID, user.ID)
	}

	// An expired session must not authenticate, and the check lives in SQL so no
	// caller can forget it.
	expiredHash := []byte{0x01, 0x02}
	past := Millis(time.Now().Add(-time.Hour))
	if _, err := q.CreateSession(ctx, gen.CreateSessionParams{
		ID: id.New(id.Session), UserID: user.ID, TokenHash: expiredHash,
		CreatedAt: past, LastSeenAt: past, ExpiresAt: past,
	}); err != nil {
		t.Fatalf("CreateSession (expired): %v", err)
	}
	if _, err := q.GetSessionByToken(ctx, gen.GetSessionByTokenParams{
		TokenHash: expiredHash, ExpiresAt: Now(),
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("an expired session must not authenticate, got err = %v", err)
	}

	// Sweeping expired sessions must remove only the expired one.
	removed, err := q.DeleteExpiredSessions(ctx, Now())
	if err != nil {
		t.Fatalf("DeleteExpiredSessions: %v", err)
	}
	if removed != 1 {
		t.Errorf("swept %d sessions, want 1", removed)
	}

	// Logging out everywhere after a password change.
	if err := q.DeleteUserSessions(ctx, user.ID); err != nil {
		t.Fatalf("DeleteUserSessions: %v", err)
	}
	list, err := q.ListUserSessions(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListUserSessions: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("%d sessions remain after DeleteUserSessions", len(list))
	}
}

func TestSettingsUpsert(t *testing.T) {
	q, _ := newQueries(t)
	ctx := context.Background()

	if err := q.SetSetting(ctx, gen.SetSettingParams{Key: "instance_id", Value: "first", UpdatedAt: Now()}); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	// Writing the same key again must update rather than conflict.
	if err := q.SetSetting(ctx, gen.SetSettingParams{Key: "instance_id", Value: "second", UpdatedAt: Now()}); err != nil {
		t.Fatalf("SetSetting (update): %v", err)
	}

	got, err := q.GetSetting(ctx, "instance_id")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if got != "second" {
		t.Errorf("value = %q, want second", got)
	}

	all, err := q.ListSettings(ctx)
	if err != nil {
		t.Fatalf("ListSettings: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("upsert created %d rows, want 1", len(all))
	}
}

func TestBlobQueriesAreContentAddressed(t *testing.T) {
	q, _ := newQueries(t)
	ctx := context.Background()
	const sum = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

	params := gen.UpsertBlobParams{Sha256: sum, SizeBytes: 1234, MediaType: "application/pdf", CreatedAt: Now()}
	if err := q.UpsertBlob(ctx, params); err != nil {
		t.Fatalf("UpsertBlob: %v", err)
	}
	// Uploading identical bytes again must be a silent no-op, which is what makes
	// re-uploading the same manual free.
	if err := q.UpsertBlob(ctx, params); err != nil {
		t.Fatalf("UpsertBlob (duplicate) should be a no-op, got: %v", err)
	}

	exists, err := q.BlobExists(ctx, sum)
	if err != nil {
		t.Fatalf("BlobExists: %v", err)
	}
	if !exists {
		t.Error("BlobExists should report the blob as present")
	}
	if missing, err := q.BlobExists(ctx, "0000000000000000000000000000000000000000000000000000000000000000"); err != nil {
		t.Fatalf("BlobExists (missing): %v", err)
	} else if missing {
		t.Error("BlobExists should be false for bytes that were never stored")
	}

	total, err := q.TotalBlobBytes(ctx)
	if err != nil {
		t.Fatalf("TotalBlobBytes: %v", err)
	}
	if total != 1234 {
		t.Errorf("total bytes = %v, want 1234 (the duplicate must not double-count)", total)
	}
}

func TestEmptyListsAreNotNil(t *testing.T) {
	q, _ := newQueries(t)

	// emit_empty_slices keeps JSON responses returning [] instead of null.
	users, err := q.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if users == nil {
		t.Error("ListUsers returned nil; expected an empty slice")
	}
}

func mustUser(t *testing.T, q *gen.Queries, email string) gen.User {
	t.Helper()
	now := Now()
	u, err := q.CreateUser(context.Background(), gen.CreateUserParams{
		ID:           id.New(id.User),
		Email:        email,
		EmailFolded:  email,
		PasswordHash: "x",
		Role:         "admin",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatalf("create user %s: %v", email, err)
	}
	return u
}
