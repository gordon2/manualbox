package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/gordon2/manualbox/internal/db"
	"github.com/gordon2/manualbox/internal/logging"
)

const goodPassword = "correct horse battery staple"

func newService(t *testing.T) *Service {
	t.Helper()
	d, err := db.Open(context.Background(), db.Options{Path: filepath.Join(t.TempDir(), "auth.db")})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return New(d, Options{Logger: logging.Discard()})
}

// setup creates the first admin and returns it.
func setup(t *testing.T, s *Service) *User {
	t.Helper()
	u, err := s.Setup(context.Background(), "owner@example.com", goodPassword, "Test Owner")
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	return u
}

// --- password hashing ---

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword(goodPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	// The plaintext must not appear anywhere in the encoded hash.
	if strings.Contains(hash, goodPassword) {
		t.Fatal("encoded hash contains the plaintext password")
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("hash = %q, want a PHC argon2id encoding", hash)
	}

	ok, needsRehash, err := VerifyPassword(hash, goodPassword)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("the correct password did not verify")
	}
	if needsRehash {
		t.Error("a freshly created hash should not need rehashing")
	}

	if ok, _, _ := VerifyPassword(hash, goodPassword+"x"); ok {
		t.Error("a wrong password verified")
	}
	if ok, _, _ := VerifyPassword(hash, ""); ok {
		t.Error("an empty password verified")
	}
}

func TestHashesAreSaltedUniquely(t *testing.T) {
	// Identical passwords must produce different hashes, or a stolen database
	// reveals which accounts share a password.
	seen := map[string]bool{}
	for range 5 {
		h, err := HashPassword(goodPassword)
		if err != nil {
			t.Fatalf("HashPassword: %v", err)
		}
		if seen[h] {
			t.Fatal("two hashes of the same password are identical; the salt is not random")
		}
		seen[h] = true

		if ok, _, _ := VerifyPassword(h, goodPassword); !ok {
			t.Error("independently salted hash failed to verify")
		}
	}
}

func TestHashPasswordRejectsEmpty(t *testing.T) {
	if _, err := HashPassword(""); err == nil {
		t.Error("HashPassword(\"\") should fail")
	}
}

func TestVerifyPasswordRejectsMalformedHashes(t *testing.T) {
	for _, bad := range []string{
		"",
		"not-a-hash",
		"$argon2id$",
		"$bcrypt$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA",         // wrong algorithm
		"$argon2id$v=99$m=19456,t=2,p=1$c2FsdA$aGFzaA",       // wrong version
		"$argon2id$v=19$m=0,t=0,p=0$c2FsdA$aGFzaA",           // zero parameters
		"$argon2id$v=19$m=19456,t=2,p=1$!!!notbase64$aGFzaA", // bad salt
		"$argon2id$v=19$m=19456,t=2,p=1$c2FsdA$!!!notbase64", // bad digest
		"$argon2id$v=19$garbage$c2FsdA$aGFzaA",               // unreadable params
		"$argon2id$v=19$m=19456,t=2,p=1$$aGFzaA",             // empty salt
	} {
		ok, _, err := VerifyPassword(bad, goodPassword)
		if ok {
			t.Errorf("VerifyPassword(%q) returned ok", bad)
		}
		if err == nil {
			t.Errorf("VerifyPassword(%q) should report an error", bad)
		} else if !errors.Is(err, ErrInvalidHash) {
			t.Errorf("VerifyPassword(%q) error = %v, want ErrInvalidHash", bad, err)
		}
	}
}

func TestVerifyPasswordDetectsWeakerParameters(t *testing.T) {
	// A hash stored under lower cost settings should be flagged for upgrade, so
	// raising the parameters later actually takes effect on next login.
	weak := "$argon2id$v=19$m=1024,t=1,p=1$" +
		mustHashWithParams(t, goodPassword, 1024, 1, 1)

	ok, needsRehash, err := VerifyPassword(weak, goodPassword)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Fatal("a valid weak hash should still verify")
	}
	if !needsRehash {
		t.Error("a hash with weaker parameters should be flagged for rehashing")
	}
}

// --- setup ---

func TestNeedsSetupAndSetup(t *testing.T) {
	s := newService(t)
	ctx := context.Background()

	needs, err := s.NeedsSetup(ctx)
	if err != nil {
		t.Fatalf("NeedsSetup: %v", err)
	}
	if !needs {
		t.Fatal("a fresh instance should need setup")
	}

	user := setup(t, s)
	if user.Role != RoleAdmin {
		t.Errorf("Role = %q, want admin — the first user must be able to administer the instance", user.Role)
	}
	if !user.IsAdmin() {
		t.Error("IsAdmin should be true for the first user")
	}
	if user.DisplayName != "Test Owner" {
		t.Errorf("DisplayName = %q", user.DisplayName)
	}

	needs, err = s.NeedsSetup(ctx)
	if err != nil {
		t.Fatalf("NeedsSetup: %v", err)
	}
	if needs {
		t.Error("setup should be complete after the first user")
	}
}

// TestSetupCannotRunTwice matters because the setup endpoint stays routable: if it
// were not guarded, anyone reaching it later could mint themselves an admin.
func TestSetupCannotRunTwice(t *testing.T) {
	s := newService(t)
	setup(t, s)

	_, err := s.Setup(context.Background(), "attacker@example.com", goodPassword, "Someone Else")
	if !errors.Is(err, ErrSetupComplete) {
		t.Errorf("second Setup error = %v, want ErrSetupComplete", err)
	}
}

func TestSetupValidatesInput(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name, email, password string
		wantErr               error
	}{
		{"short password", "a@example.com", "short", ErrWeakPassword},
		{"empty password", "a@example.com", "", ErrWeakPassword},
		{"empty email", "", goodPassword, ErrInvalidEmail},
		{"malformed email", "not-an-email", goodPassword, ErrInvalidEmail},
		{"email without domain", "user@", goodPassword, ErrInvalidEmail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newService(t)
			_, err := s.Setup(ctx, tc.email, tc.password, "")
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("error = %v, want %v", err, tc.wantErr)
			}
			// A rejected setup must leave the instance still needing setup.
			if needs, _ := s.NeedsSetup(ctx); !needs {
				t.Error("a failed setup should not have created a user")
			}
		})
	}
}

func TestSetupDefaultsDisplayNameToEmail(t *testing.T) {
	s := newService(t)
	u, err := s.Setup(context.Background(), "solo@example.com", goodPassword, "")
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if u.DisplayName != "solo@example.com" {
		t.Errorf("DisplayName = %q, want the email as a fallback", u.DisplayName)
	}
}

// --- login ---

func TestLoginSuccess(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	created := setup(t, s)

	token, user, err := s.Login(ctx, "owner@example.com", goodPassword, "test-agent", "192.0.2.1")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if user.ID != created.ID {
		t.Errorf("user ID = %q, want %q", user.ID, created.ID)
	}
	if len(token) < 40 {
		t.Errorf("token is only %d characters; expected a high-entropy value", len(token))
	}

	gotUser, sess, err := s.Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if gotUser.ID != created.ID {
		t.Errorf("authenticated as %q, want %q", gotUser.ID, created.ID)
	}
	if sess.ExpiresAt.Before(time.Now()) {
		t.Error("a new session is already expired")
	}
}

func TestLoginIsCaseInsensitiveOnEmail(t *testing.T) {
	s := newService(t)
	setup(t, s)

	// Someone typing their address with different capitalisation should get in.
	if _, _, err := s.Login(context.Background(), "OWNER@Example.COM", goodPassword, "", ""); err != nil {
		t.Errorf("login with differently-cased email failed: %v", err)
	}
}

// TestLoginDoesNotRevealAccountExistence is a privacy property: the error for an
// unknown address and a wrong password must be indistinguishable.
func TestLoginDoesNotRevealAccountExistence(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	setup(t, s)

	_, _, unknownErr := s.Login(ctx, "nobody@example.com", goodPassword, "", "")
	_, _, wrongErr := s.Login(ctx, "owner@example.com", "wrong-password-entirely", "", "")

	if !errors.Is(unknownErr, ErrInvalidCredentials) {
		t.Errorf("unknown account error = %v, want ErrInvalidCredentials", unknownErr)
	}
	if !errors.Is(wrongErr, ErrInvalidCredentials) {
		t.Errorf("wrong password error = %v, want ErrInvalidCredentials", wrongErr)
	}
	if unknownErr.Error() != wrongErr.Error() {
		t.Errorf("error messages differ and leak account existence:\n unknown: %v\n   wrong: %v", unknownErr, wrongErr)
	}
}

func TestLoginFailureIssuesNoSession(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	setup(t, s)

	token, user, err := s.Login(ctx, "owner@example.com", "wrong", "", "")
	if err == nil {
		t.Fatal("login with a wrong password should fail")
	}
	if token != "" || user != nil {
		t.Error("a failed login must not return a token or a user")
	}
}

// --- sessions ---

func TestAuthenticateRejectsBadTokens(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	setup(t, s)

	for _, tc := range []struct{ name, token string }{
		{"empty", ""},
		{"unknown", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{"garbage", "!!!!"},
	} {
		if _, _, err := s.Authenticate(ctx, tc.token); !errors.Is(err, ErrNoSession) {
			t.Errorf("Authenticate(%s) error = %v, want ErrNoSession", tc.name, err)
		}
	}
}

// TestRawTokenIsNotStored is the reason a leaked backup cannot be replayed.
func TestRawTokenIsNotStored(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	setup(t, s)

	token, _, err := s.Login(ctx, "owner@example.com", goodPassword, "", "")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	// Scan the whole sessions table for the raw token in any column.
	rows, err := s.db.Read().QueryContext(ctx, `SELECT id, user_id, hex(token_hash), user_agent, ip FROM sessions`)
	if err != nil {
		t.Fatalf("query sessions: %v", err)
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		found = true
		var sessID, userID, hash, ua, ip string
		if err := rows.Scan(&sessID, &userID, &hash, &ua, &ip); err != nil {
			t.Fatalf("scan: %v", err)
		}
		for _, col := range []string{sessID, userID, hash, ua, ip} {
			if strings.Contains(col, token) {
				t.Error("the raw session token is stored in the database")
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if !found {
		t.Fatal("no session row was created")
	}
}

func TestExpiredSessionDoesNotAuthenticate(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	setup(t, s)

	token, _, err := s.Login(ctx, "owner@example.com", goodPassword, "", "")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	// Jump past the session lifetime.
	s.now = func() time.Time { return time.Now().Add(DefaultSessionTTL + time.Hour) }

	if _, _, err := s.Authenticate(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Errorf("an expired session authenticated; error = %v", err)
	}
}

func TestSessionExpirySlidesForward(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	setup(t, s)

	token, _, err := s.Login(ctx, "owner@example.com", goodPassword, "", "")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	_, before, err := s.Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	// Come back after more than the extension threshold but before expiry: daily
	// use should never force a re-login.
	s.now = func() time.Time { return time.Now().Add(extendAfter + time.Hour) }

	_, after, err := s.Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("Authenticate after a gap: %v", err)
	}
	if !after.ExpiresAt.After(before.ExpiresAt) {
		t.Errorf("expiry did not slide forward: before %v, after %v", before.ExpiresAt, after.ExpiresAt)
	}
}

func TestLogout(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	setup(t, s)

	token, _, err := s.Login(ctx, "owner@example.com", goodPassword, "", "")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if err := s.Logout(ctx, token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, _, err := s.Authenticate(ctx, token); !errors.Is(err, ErrNoSession) {
		t.Errorf("session still valid after logout; error = %v", err)
	}

	// Logging out twice, or with no token, is harmless.
	if err := s.Logout(ctx, token); err != nil {
		t.Errorf("second Logout: %v", err)
	}
	if err := s.Logout(ctx, ""); err != nil {
		t.Errorf("Logout with an empty token: %v", err)
	}
}

func TestSessionsAreIndependent(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	setup(t, s)

	phone, _, err := s.Login(ctx, "owner@example.com", goodPassword, "phone", "")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	laptop, _, err := s.Login(ctx, "owner@example.com", goodPassword, "laptop", "")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if phone == laptop {
		t.Fatal("two logins produced the same token")
	}

	// Signing out on one device must not sign out the other.
	if err := s.Logout(ctx, phone); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, _, err := s.Authenticate(ctx, laptop); err != nil {
		t.Errorf("logging out one device invalidated another: %v", err)
	}
}

func TestSweepExpiredSessions(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	setup(t, s)

	if _, _, err := s.Login(ctx, "owner@example.com", goodPassword, "", ""); err != nil {
		t.Fatalf("Login: %v", err)
	}

	// Nothing to sweep yet.
	if n, err := s.SweepExpiredSessions(ctx); err != nil || n != 0 {
		t.Errorf("sweep removed %d sessions (err %v), want 0", n, err)
	}

	s.now = func() time.Time { return time.Now().Add(DefaultSessionTTL + time.Hour) }
	if n, err := s.SweepExpiredSessions(ctx); err != nil || n != 1 {
		t.Errorf("sweep removed %d sessions (err %v), want 1", n, err)
	}
}

// --- password change ---

func TestChangePasswordRevokesOtherSessions(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	user := setup(t, s)

	stolen, _, err := s.Login(ctx, "owner@example.com", goodPassword, "attacker", "")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	const newPassword = "an entirely different passphrase"
	if err := s.ChangePassword(ctx, user.ID, goodPassword, newPassword); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	// Changing a password is what someone does when they suspect a compromise, so
	// every existing session must die.
	if _, _, err := s.Authenticate(ctx, stolen); !errors.Is(err, ErrNoSession) {
		t.Errorf("a pre-existing session survived a password change; error = %v", err)
	}

	if _, _, err := s.Login(ctx, "owner@example.com", newPassword, "", ""); err != nil {
		t.Errorf("login with the new password failed: %v", err)
	}
	if _, _, err := s.Login(ctx, "owner@example.com", goodPassword, "", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Error("the old password still works after a change")
	}
}

func TestChangePasswordRequiresCurrentPassword(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	user := setup(t, s)

	err := s.ChangePassword(ctx, user.ID, "not-the-current-password", "a brand new passphrase")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("error = %v, want ErrInvalidCredentials", err)
	}
	// The original password must still work.
	if _, _, err := s.Login(ctx, "owner@example.com", goodPassword, "", ""); err != nil {
		t.Errorf("original password stopped working after a rejected change: %v", err)
	}
}

func TestChangePasswordRejectsWeakNewPassword(t *testing.T) {
	s := newService(t)
	user := setup(t, s)

	if err := s.ChangePassword(context.Background(), user.ID, goodPassword, "short"); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("error = %v, want ErrWeakPassword", err)
	}
}

func TestValidatePassword(t *testing.T) {
	if err := ValidatePassword(strings.Repeat("a", MinPasswordLength)); err != nil {
		t.Errorf("a password at the minimum length should pass: %v", err)
	}
	if err := ValidatePassword(strings.Repeat("a", MinPasswordLength-1)); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("error = %v, want ErrWeakPassword", err)
	}
	// Length is counted in runes, so a short passphrase of multi-byte characters
	// is not accepted just because its byte length is large.
	if err := ValidatePassword("паролі"); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("a 6-rune password should be rejected, got %v", err)
	}
}

// mustHashWithParams builds the salt$digest tail of a PHC hash under explicit
// parameters, for testing verification of legacy cost settings.
func mustHashWithParams(t *testing.T, password string, memory, iterations uint32, lanes uint8) string {
	t.Helper()
	salt := []byte("0123456789abcdef")
	digest := argon2.IDKey([]byte(password), salt, iterations, memory, lanes, argonKeyLen)
	enc := base64.RawStdEncoding
	return enc.EncodeToString(salt) + "$" + enc.EncodeToString(digest)
}

// TestFailedLoginDoesNotLogTheAddress guards a leak path that is easy to
// reintroduce: logs get pasted into bug reports and shipped to log services, so a
// failed login must not record who was being attacked in plaintext.
func TestFailedLoginDoesNotLogTheAddress(t *testing.T) {
	var buf strings.Builder
	d, err := db.Open(context.Background(), db.Options{Path: filepath.Join(t.TempDir(), "auth.db")})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	s := New(d, Options{Logger: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	setup(t, s)

	if _, _, err := s.Login(context.Background(), "owner@example.com", "wrong password entirely", "", "203.0.113.7"); err == nil {
		t.Fatal("expected the login to fail")
	}

	logged := buf.String()
	if logged == "" {
		t.Fatal("nothing was logged; the test cannot verify anything")
	}
	for _, secret := range []string{"owner@example.com", "owner", "example.com"} {
		if strings.Contains(logged, secret) {
			t.Errorf("the log contains %q:\n%s", secret, logged)
		}
	}
	// It must still be diagnosable: same account, same fingerprint.
	if !strings.Contains(logged, fingerprint("owner@example.com")) {
		t.Errorf("the log should carry a stable account fingerprint:\n%s", logged)
	}
	if !strings.Contains(logged, "203.0.113.7") {
		t.Errorf("the log should keep the source IP for diagnosis:\n%s", logged)
	}
}
