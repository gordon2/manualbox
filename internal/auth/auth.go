// Package auth handles first-run setup, password login, and session lifetime.
//
// Two decisions shape everything here:
//
//   - Only a hash of a session token is stored. A database backup or a leaked
//     dump therefore cannot be replayed as a live session, which matters for a
//     self-hosted app whose backups end up on other people's disks.
//   - A failed login reveals nothing about which addresses are registered: the
//     error text is identical and a placeholder hash is verified when the account
//     does not exist, so the timing matches too.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/gordon2/manualbox/internal/db"
	"github.com/gordon2/manualbox/internal/db/gen"
	"github.com/gordon2/manualbox/internal/id"
)

// Defaults for session lifetime.
const (
	// DefaultSessionTTL is how long a session stays valid without use. It slides
	// forward on activity, so day-to-day use never forces a re-login.
	DefaultSessionTTL = 30 * 24 * time.Hour
	// extendAfter is how much of the TTL must elapse before a session's expiry is
	// pushed forward. Extending on every request would mean a database write per
	// request for no benefit.
	extendAfter = 24 * time.Hour

	// MinPasswordLength is deliberately modest: this is a household service
	// behind a home network, and a long minimum pushes people towards reuse.
	MinPasswordLength = 10
	// sessionTokenBytes is the entropy in a session token before encoding.
	sessionTokenBytes = 32
)

// Roles a user can hold.
const (
	RoleAdmin  = "admin"
	RoleMember = "member"
	RoleViewer = "viewer"
)

var (
	// ErrInvalidCredentials is returned for any failed login, whatever the cause.
	ErrInvalidCredentials = errors.New("email or password is incorrect")
	// ErrNoSession is returned when a token is absent, unknown, or expired.
	ErrNoSession = errors.New("not authenticated")
	// ErrSetupComplete is returned when setup is attempted on an initialized
	// instance. It exists to stop a second admin being created by anyone who
	// finds the setup endpoint later.
	ErrSetupComplete = errors.New("setup has already been completed")
	// ErrWeakPassword is returned when a password is too short.
	ErrWeakPassword = fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	// ErrInvalidEmail is returned when an email address will not parse.
	ErrInvalidEmail = errors.New("email address is not valid")
)

// User is an authenticated household member.
type User struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	DisplayName string     `json:"displayName"`
	Role        string     `json:"role"`
	CreatedAt   time.Time  `json:"createdAt"`
	LastLoginAt *time.Time `json:"lastLoginAt,omitempty"`
}

// IsAdmin reports whether the user may change instance settings.
func (u *User) IsAdmin() bool { return u != nil && u.Role == RoleAdmin }

// Session is a login session.
type Session struct {
	ID         string    `json:"id"`
	UserID     string    `json:"userId"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// Service implements authentication against the database.
type Service struct {
	db  *db.DB
	log *slog.Logger
	ttl time.Duration
	now func() time.Time
}

// Options configures [New].
type Options struct {
	// SessionTTL overrides [DefaultSessionTTL].
	SessionTTL time.Duration
	Logger     *slog.Logger
}

// New returns a Service.
func New(d *db.DB, opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.SessionTTL <= 0 {
		opts.SessionTTL = DefaultSessionTTL
	}
	return &Service{db: d, log: opts.Logger, ttl: opts.SessionTTL, now: time.Now}
}

// NeedsSetup reports whether the instance has no users yet, which is what the
// frontend uses to decide between showing setup and showing login.
func (s *Service) NeedsSetup(ctx context.Context) (bool, error) {
	n, err := gen.New(s.db.Read()).CountUsers(ctx)
	if err != nil {
		return false, fmt.Errorf("auth: count users: %w", err)
	}
	return n == 0, nil
}

// Setup creates the first administrator. It fails once any user exists.
func (s *Service) Setup(ctx context.Context, email, password, displayName string) (*User, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return nil, err
	}
	if err := ValidatePassword(password); err != nil {
		return nil, err
	}

	needs, err := s.NeedsSetup(ctx)
	if err != nil {
		return nil, err
	}
	if !needs {
		return nil, ErrSetupComplete
	}

	hash, err := HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("auth: hash password: %w", err)
	}
	if displayName == "" {
		displayName = email
	}

	now := db.Millis(s.now())
	row, err := gen.New(s.db.Write()).CreateUser(ctx, gen.CreateUserParams{
		ID:           id.New(id.User),
		Email:        email,
		EmailFolded:  strings.ToLower(email),
		DisplayName:  displayName,
		PasswordHash: hash,
		Role:         RoleAdmin,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		return nil, fmt.Errorf("auth: create first user: %w", err)
	}

	s.log.Info("first-run setup completed", "user", row.ID, "email", row.Email)
	return userFromRow(row), nil
}

// Login verifies credentials and returns a new session token.
//
// The returned token is the only time the raw value exists; the database keeps
// just its hash. Callers must hand it to the client immediately.
//
// TODO(M1): add per-address and per-IP throttling. Argon2id at the parameters in
// password.go costs tens of milliseconds and 19 MiB per attempt, which bounds
// online guessing to roughly a dozen tries a second and makes a distributed
// attack expensive — but it is not a substitute for a lockout, and an instance
// exposed to the internet with a weak password is still reachable.
func (s *Service) Login(ctx context.Context, email, password, userAgent, ip string) (token string, user *User, err error) {
	folded := strings.ToLower(strings.TrimSpace(email))

	row, lookupErr := gen.New(s.db.Read()).GetUserByEmail(ctx, folded)
	if lookupErr != nil {
		if errors.Is(lookupErr, sql.ErrNoRows) {
			// Verify against a placeholder so a missing account costs the same time
			// as a wrong password, then return the same error either way.
			_, _, _ = VerifyPassword(dummyHash, password)
			return "", nil, ErrInvalidCredentials
		}
		return "", nil, fmt.Errorf("auth: look up user: %w", lookupErr)
	}

	ok, needsRehash, err := VerifyPassword(row.PasswordHash, password)
	if err != nil {
		// A corrupt stored hash is an operational problem, not a hint to give the
		// person typing the password.
		s.log.Error("stored password hash is unreadable", "user", row.ID, "error", err)
		return "", nil, ErrInvalidCredentials
	}
	if !ok {
		s.log.Warn("failed login attempt", "email", folded, "ip", ip)
		return "", nil, ErrInvalidCredentials
	}

	// Opportunistically upgrade a hash created under weaker parameters, now that
	// the plaintext is in hand and known correct.
	if needsRehash {
		if upgraded, hashErr := HashPassword(password); hashErr == nil {
			if err := gen.New(s.db.Write()).UpdateUserPassword(ctx, gen.UpdateUserPasswordParams{
				PasswordHash: upgraded,
				UpdatedAt:    db.Millis(s.now()),
				ID:           row.ID,
			}); err != nil {
				s.log.Warn("rehashing password failed", "user", row.ID, "error", err)
			}
		}
	}

	token, _, err = s.createSession(ctx, row.ID, userAgent, ip)
	if err != nil {
		return "", nil, err
	}

	loginAt := db.Millis(s.now())
	if err := gen.New(s.db.Write()).TouchUserLogin(ctx, gen.TouchUserLoginParams{
		LastLoginAt: &loginAt,
		UpdatedAt:   loginAt,
		ID:          row.ID,
	}); err != nil {
		// Not worth failing a good login over.
		s.log.Warn("recording last login failed", "user", row.ID, "error", err)
	}

	s.log.Info("login", "user", row.ID, "ip", ip)
	return token, userFromRow(row), nil
}

// createSession issues a session and returns the raw token.
func (s *Service) createSession(ctx context.Context, userID, userAgent, ip string) (string, *Session, error) {
	raw := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("auth: generate session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))

	now := s.now()
	row, err := gen.New(s.db.Write()).CreateSession(ctx, gen.CreateSessionParams{
		ID:         id.New(id.Session),
		UserID:     userID,
		TokenHash:  sum[:],
		UserAgent:  truncate(userAgent, 512),
		Ip:         ip,
		CreatedAt:  db.Millis(now),
		LastSeenAt: db.Millis(now),
		ExpiresAt:  db.Millis(now.Add(s.ttl)),
	})
	if err != nil {
		return "", nil, fmt.Errorf("auth: create session: %w", err)
	}
	return token, sessionFromRow(row), nil
}

// Authenticate resolves a session token to its user, sliding the expiry forward
// when the session has been in use for a while.
func (s *Service) Authenticate(ctx context.Context, token string) (*User, *Session, error) {
	if token == "" {
		return nil, nil, ErrNoSession
	}
	sum := sha256.Sum256([]byte(token))

	now := s.now()
	row, err := gen.New(s.db.Read()).GetSessionByToken(ctx, gen.GetSessionByTokenParams{
		TokenHash: sum[:],
		// Expiry is filtered in SQL, so an expired session can never be treated
		// as valid by a caller that forgets to check.
		ExpiresAt: db.Millis(now),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrNoSession
		}
		return nil, nil, fmt.Errorf("auth: look up session: %w", err)
	}

	sess := sessionFromRow(row.Session)
	user := userFromRow(row.User)

	if now.Sub(sess.LastSeenAt) > extendAfter {
		expires := db.Millis(now.Add(s.ttl))
		if err := gen.New(s.db.Write()).ExtendSession(ctx, gen.ExtendSessionParams{
			LastSeenAt: db.Millis(now),
			ExpiresAt:  expires,
			ID:         sess.ID,
		}); err != nil {
			s.log.Warn("extending session failed", "session", sess.ID, "error", err)
		} else {
			sess.LastSeenAt, sess.ExpiresAt = now, db.Time(expires)
		}
	}

	return user, sess, nil
}

// Logout invalidates one session token.
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(token))
	if err := gen.New(s.db.Write()).DeleteSessionByToken(ctx, sum[:]); err != nil {
		return fmt.Errorf("auth: delete session: %w", err)
	}
	return nil
}

// ChangePassword updates a user's password and invalidates every other session,
// which is the behaviour someone expects after suspecting a compromise.
func (s *Service) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) error {
	if err := ValidatePassword(newPassword); err != nil {
		return err
	}

	row, err := gen.New(s.db.Read()).GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoSession
		}
		return fmt.Errorf("auth: look up user: %w", err)
	}

	ok, _, err := VerifyPassword(row.PasswordHash, currentPassword)
	if err != nil || !ok {
		return ErrInvalidCredentials
	}

	hash, err := HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("auth: hash password: %w", err)
	}

	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)
		if err := q.UpdateUserPassword(ctx, gen.UpdateUserPasswordParams{
			PasswordHash: hash,
			UpdatedAt:    db.Millis(s.now()),
			ID:           userID,
		}); err != nil {
			return fmt.Errorf("auth: update password: %w", err)
		}
		if err := q.DeleteUserSessions(ctx, userID); err != nil {
			return fmt.Errorf("auth: revoke sessions: %w", err)
		}
		return nil
	})
}

// SweepExpiredSessions deletes sessions past their expiry, and is safe to run on
// a timer.
func (s *Service) SweepExpiredSessions(ctx context.Context) (int64, error) {
	n, err := gen.New(s.db.Write()).DeleteExpiredSessions(ctx, db.Millis(s.now()))
	if err != nil {
		return 0, fmt.Errorf("auth: sweep sessions: %w", err)
	}
	if n > 0 {
		s.log.Debug("swept expired sessions", "count", n)
	}
	return n, nil
}

// ValidatePassword checks a password against the minimum policy.
func ValidatePassword(password string) error {
	if len([]rune(password)) < MinPasswordLength {
		return ErrWeakPassword
	}
	return nil
}

// normalizeEmail trims and validates an address, returning it as entered so
// display keeps the original casing.
func normalizeEmail(email string) (string, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return "", ErrInvalidEmail
	}
	addr, err := mail.ParseAddress(email)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrInvalidEmail, email)
	}
	return addr.Address, nil
}

func userFromRow(r gen.User) *User {
	return &User{
		ID:          r.ID,
		Email:       r.Email,
		DisplayName: r.DisplayName,
		Role:        r.Role,
		CreatedAt:   db.Time(r.CreatedAt),
		LastLoginAt: db.TimePtr(r.LastLoginAt),
	}
}

func sessionFromRow(r gen.Session) *Session {
	return &Session{
		ID:         r.ID,
		UserID:     r.UserID,
		CreatedAt:  db.Time(r.CreatedAt),
		LastSeenAt: db.Time(r.LastSeenAt),
		ExpiresAt:  db.Time(r.ExpiresAt),
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
