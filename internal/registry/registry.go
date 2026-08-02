// Package registry is the household inventory: where things are, what they are,
// and which documents belong to them.
//
// It deliberately holds no serial numbers and no purchase prices. Those are the
// highest-harm fields manualbox will store, they must be encrypted with a key
// kept outside the data directory, and the keyring is not wired into the schema
// yet. Adding them in the clear now would mean migrating real user data later.
// See docs/design/privacy.md.
package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/gordon2/manualbox/internal/db"
	"github.com/gordon2/manualbox/internal/db/gen"
	"github.com/gordon2/manualbox/internal/id"
)

var (
	// ErrNotFound is returned when an entity does not exist.
	ErrNotFound = errors.New("not found")
	// ErrInvalid is returned when input fails validation.
	ErrInvalid = errors.New("invalid")
)

// Service reads and writes the registry.
type Service struct {
	db  *db.DB
	log *slog.Logger
	now func() time.Time
}

// Options configures [New].
type Options struct {
	Logger *slog.Logger
	// Now overrides the clock, for tests.
	Now func() time.Time
}

// New returns a registry service.
func New(d *db.DB, opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Service{db: d, log: opts.Logger, now: opts.Now}
}

// --- locations ---

// Location is a place something is kept. Locations nest, so "Kitchen" can sit
// under "House".
type Location struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	ParentID  string    `json:"parentId,omitempty"`
	Notes     string    `json:"notes,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// CreateLocation adds a location.
func (s *Service) CreateLocation(ctx context.Context, name, parentID, notes string) (*Location, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: a location needs a name", ErrInvalid)
	}
	now := db.Millis(s.now())
	row, err := gen.New(s.db.Write()).CreateLocation(ctx, gen.CreateLocationParams{
		ID:        id.New(id.Location),
		Name:      name,
		ParentID:  nullString(parentID),
		Notes:     notes,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		return nil, fmt.Errorf("registry: create location: %w", err)
	}
	return locationFrom(row), nil
}

// ListLocations returns every location, by name.
func (s *Service) ListLocations(ctx context.Context) ([]Location, error) {
	rows, err := gen.New(s.db.Read()).ListLocations(ctx)
	if err != nil {
		return nil, fmt.Errorf("registry: list locations: %w", err)
	}
	out := make([]Location, 0, len(rows))
	for _, r := range rows {
		out = append(out, *locationFrom(r))
	}
	return out, nil
}

// --- devices ---

// Device is a thing the household owns.
type Device struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Brand       string     `json:"brand,omitempty"`
	Model       string     `json:"model,omitempty"`
	Category    string     `json:"category,omitempty"`
	LocationID  string     `json:"locationId,omitempty"`
	Notes       string     `json:"notes,omitempty"`
	PurchasedAt *time.Time `json:"purchasedAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// NewDevice is the input to [Service.CreateDevice].
type NewDevice struct {
	Name        string
	Brand       string
	Model       string
	Category    string
	LocationID  string
	Notes       string
	PurchasedAt *time.Time
}

// CreateDevice adds a device.
func (s *Service) CreateDevice(ctx context.Context, in NewDevice) (*Device, error) {
	if in.Name == "" {
		return nil, fmt.Errorf("%w: a device needs a name", ErrInvalid)
	}
	now := db.Millis(s.now())
	row, err := gen.New(s.db.Write()).CreateDevice(ctx, gen.CreateDeviceParams{
		ID:          id.New(id.Device),
		Name:        in.Name,
		Brand:       in.Brand,
		Model:       in.Model,
		Category:    in.Category,
		LocationID:  nullString(in.LocationID),
		Notes:       in.Notes,
		PurchasedAt: millisPtr(in.PurchasedAt),
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		return nil, fmt.Errorf("registry: create device: %w", err)
	}
	return deviceFrom(row), nil
}

// GetDevice returns one device.
func (s *Service) GetDevice(ctx context.Context, deviceID string) (*Device, error) {
	row, err := gen.New(s.db.Read()).GetDevice(ctx, deviceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: device %s", ErrNotFound, deviceID)
		}
		return nil, fmt.Errorf("registry: get device: %w", err)
	}
	return deviceFrom(row), nil
}

// ListDevices returns every device, by name.
func (s *Service) ListDevices(ctx context.Context) ([]Device, error) {
	rows, err := gen.New(s.db.Read()).ListDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("registry: list devices: %w", err)
	}
	out := make([]Device, 0, len(rows))
	for i := range rows {
		out = append(out, *deviceFrom(rows[i]))
	}
	return out, nil
}

// UpdateDevice replaces a device's editable fields.
func (s *Service) UpdateDevice(ctx context.Context, deviceID string, in NewDevice) (*Device, error) {
	if in.Name == "" {
		return nil, fmt.Errorf("%w: a device needs a name", ErrInvalid)
	}
	row, err := gen.New(s.db.Write()).UpdateDevice(ctx, gen.UpdateDeviceParams{
		Name:        in.Name,
		Brand:       in.Brand,
		Model:       in.Model,
		Category:    in.Category,
		LocationID:  nullString(in.LocationID),
		Notes:       in.Notes,
		PurchasedAt: millisPtr(in.PurchasedAt),
		UpdatedAt:   db.Millis(s.now()),
		ID:          deviceID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: device %s", ErrNotFound, deviceID)
		}
		return nil, fmt.Errorf("registry: update device: %w", err)
	}
	return deviceFrom(row), nil
}

// DeleteDevice removes a device and, by cascade, its documents' rows. The blobs
// themselves are left alone: another device may reference the same bytes, and an
// original is the one thing that must never be lost by accident.
func (s *Service) DeleteDevice(ctx context.Context, deviceID string) error {
	if err := gen.New(s.db.Write()).DeleteDevice(ctx, deviceID); err != nil {
		return fmt.Errorf("registry: delete device: %w", err)
	}
	return nil
}

// --- conversions ---

func locationFrom(r gen.Location) *Location {
	return &Location{
		ID:        r.ID,
		Name:      r.Name,
		ParentID:  derefString(r.ParentID),
		Notes:     r.Notes,
		CreatedAt: db.Time(r.CreatedAt),
		UpdatedAt: db.Time(r.UpdatedAt),
	}
}

func deviceFrom(r gen.Device) *Device {
	return &Device{
		ID:          r.ID,
		Name:        r.Name,
		Brand:       r.Brand,
		Model:       r.Model,
		Category:    r.Category,
		LocationID:  derefString(r.LocationID),
		Notes:       r.Notes,
		PurchasedAt: db.TimePtr(r.PurchasedAt),
		CreatedAt:   db.Time(r.CreatedAt),
		UpdatedAt:   db.Time(r.UpdatedAt),
	}
}

// nullString maps "" to NULL, so an unset optional reference is stored as absent
// rather than as an empty string that satisfies no foreign key.
func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func millisPtr(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	ms := db.Millis(*t)
	return &ms
}
