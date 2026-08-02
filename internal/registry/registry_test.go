package registry_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gordon2/manualbox/internal/db"
	"github.com/gordon2/manualbox/internal/db/gen"
	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/registry"
	"github.com/gordon2/manualbox/internal/store"
)

func newService(t *testing.T) *registry.Service {
	t.Helper()
	database, err := db.Open(context.Background(), db.Options{
		Path: filepath.Join(t.TempDir(), "registry.db"),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return registry.New(database, registry.Options{})
}

func TestDeviceRoundTrip(t *testing.T) {
	s := newService(t)
	ctx := context.Background()

	purchased := time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC)
	created, err := s.CreateDevice(ctx, registry.NewDevice{
		Name: "Dishwasher", Brand: "Bosch", Model: "SMS4H", PurchasedAt: &purchased,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.GetDevice(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Dishwasher" || got.Brand != "Bosch" {
		t.Errorf("round-tripped device = %+v", got)
	}
	if got.PurchasedAt == nil || !got.PurchasedAt.Equal(purchased) {
		t.Errorf("purchasedAt = %v, want %v", got.PurchasedAt, purchased)
	}
	if !strings.HasPrefix(got.ID, "dev_") {
		t.Errorf("id = %q, want a dev_ prefix", got.ID)
	}
}

func TestDeviceNeedsAName(t *testing.T) {
	s := newService(t)
	if _, err := s.CreateDevice(context.Background(), registry.NewDevice{}); !errors.Is(err, registry.ErrInvalid) {
		t.Errorf("error = %v, want ErrInvalid", err)
	}
}

func TestMissingDeviceIsNotFound(t *testing.T) {
	s := newService(t)
	if _, err := s.GetDevice(context.Background(), "dev_nope"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestUnsetLocationIsNullNotEmptyString(t *testing.T) {
	// An empty string satisfies no foreign key, so an unset optional reference has
	// to be stored as NULL. Getting this wrong makes device creation fail only
	// once a location table exists to violate.
	s := newService(t)
	ctx := context.Background()

	device, err := s.CreateDevice(ctx, registry.NewDevice{Name: "Kettle"})
	if err != nil {
		t.Fatalf("create device with no location: %v", err)
	}
	if device.LocationID != "" {
		t.Errorf("locationId = %q, want empty", device.LocationID)
	}

	location, err := s.CreateLocation(ctx, "Kitchen", "", "")
	if err != nil {
		t.Fatalf("create location: %v", err)
	}
	updated, err := s.UpdateDevice(ctx, device.ID, registry.NewDevice{
		Name: "Kettle", LocationID: location.ID,
	})
	if err != nil {
		t.Fatalf("assign location: %v", err)
	}
	if updated.LocationID != location.ID {
		t.Errorf("locationId = %q, want %q", updated.LocationID, location.ID)
	}
}

func TestDeletingADeviceRemovesItsDocuments(t *testing.T) {
	// Cascade is what keeps the document table from accumulating rows pointing at
	// devices that no longer exist. The blobs are deliberately left alone.
	s := newService(t)
	ctx := context.Background()

	device, err := s.CreateDevice(ctx, registry.NewDevice{Name: "Robot vacuum"})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	ref := store.Ref{SHA256: strings.Repeat("ab", 32), Size: 1024}
	if err := s.RecordBlob(ctx, ref, "application/pdf"); err != nil {
		t.Fatalf("record blob: %v", err)
	}
	document, _, err := s.CreateDocument(ctx, registry.NewDocument{
		DeviceID: device.ID, BlobSHA256: ref.SHA256, Filename: "manual.pdf",
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}

	if err := s.DeleteDevice(ctx, device.ID); err != nil {
		t.Fatalf("delete device: %v", err)
	}
	if _, err := s.GetDocument(ctx, document.ID); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("document survived its device: %v", err)
	}
}

func TestDocumentDefaultsToAManual(t *testing.T) {
	s := newService(t)
	ctx := context.Background()

	device, err := s.CreateDevice(ctx, registry.NewDevice{Name: "Oven"})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	ref := store.Ref{SHA256: strings.Repeat("cd", 32), Size: 10}
	if err := s.RecordBlob(ctx, ref, ""); err != nil {
		t.Fatalf("record blob: %v", err)
	}

	document, created, err := s.CreateDocument(ctx, registry.NewDocument{
		DeviceID: device.ID, BlobSHA256: ref.SHA256,
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	if !created {
		t.Error("first insert reported itself as a duplicate")
	}
	if document.Kind != registry.KindManual {
		t.Errorf("kind = %q, want %q", document.Kind, registry.KindManual)
	}
	if document.State != registry.StateUploaded {
		t.Errorf("state = %q, want %q", document.State, registry.StateUploaded)
	}
	if document.Probed() {
		t.Error("a freshly created document claims to have been probed")
	}
}

func TestSaveProbeReplacesRatherThanAccumulates(t *testing.T) {
	// Re-probing must converge on one answer. A run the newest probe no longer
	// believes in has to disappear, not linger beside its replacement — otherwise a
	// corrected boundary shows up as two overlapping claims.
	s := newService(t)
	ctx := context.Background()

	device, err := s.CreateDevice(ctx, registry.NewDevice{Name: "Washer"})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	ref := store.Ref{SHA256: strings.Repeat("ef", 32), Size: 10}
	if err := s.RecordBlob(ctx, ref, ""); err != nil {
		t.Fatalf("record blob: %v", err)
	}
	document, _, err := s.CreateDocument(ctx, registry.NewDocument{
		DeviceID: device.ID, BlobSHA256: ref.SHA256,
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}

	first := &doc.Result{
		Info:  doc.Info{Pages: 4},
		Pages: []doc.Page{{No: 1, Chars: 100}, {No: 2, Chars: 100}, {No: 3, Chars: 100}, {No: 4, Chars: 100}},
		Runs: []doc.Run{
			{Source: doc.SourceReconciled, Code: "EN", Lang: "en", Start: 1, End: 2},
			{Source: doc.SourceReconciled, Code: "DE", Lang: "de", Start: 3, End: 4},
		},
		HasTextLayer: true, ContentStart: 1, ContentEnd: 4,
	}
	if err := s.SaveProbe(ctx, document.ID, first, registry.StateAwaitingScope); err != nil {
		t.Fatalf("save first probe: %v", err)
	}

	// A second probe that finds one language where the first found two.
	second := &doc.Result{
		Info:  doc.Info{Pages: 4},
		Pages: first.Pages,
		Runs: []doc.Run{
			{Source: doc.SourceReconciled, Code: "EN", Lang: "en", Start: 1, End: 4},
		},
		HasTextLayer: true, ContentStart: 1, ContentEnd: 4,
	}
	if err := s.SaveProbe(ctx, document.ID, second, registry.StateAwaitingScope); err != nil {
		t.Fatalf("save second probe: %v", err)
	}

	runs, err := s.LanguageRuns(ctx, document.ID, doc.SourceReconciled)
	if err != nil {
		t.Fatalf("language runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs after re-probing, want 1: %+v", len(runs), runs)
	}
	if runs[0].Start != 1 || runs[0].End != 4 {
		t.Errorf("run = %d-%d, want 1-4", runs[0].Start, runs[0].End)
	}

	after, err := s.GetDocument(ctx, document.ID)
	if err != nil {
		t.Fatalf("get document: %v", err)
	}
	if !after.Probed() {
		t.Error("document is not marked as probed")
	}
	if after.PageCount == nil || *after.PageCount != 4 {
		t.Errorf("pageCount = %v, want 4", after.PageCount)
	}
}

func TestUnplaceableLanguageClaimsAreStored(t *testing.T) {
	// A printed index routinely names a language but points at a page that cannot
	// be right — a real manual lists Czech at a page that is Arabic. The claim is
	// still evidence worth keeping: it is how a user learns their contents table is
	// wrong. Such runs carry a start of 0, and several of them can coexist, so
	// neither the CHECK nor the primary key may reject them.
	//
	// Both mistakes were made: a CHECK of pdf_start >= 1 failed the whole probe on
	// a real document, and keying on the page alone would have collapsed every
	// unplaceable claim into one row.
	s := newService(t)
	ctx := context.Background()

	device, err := s.CreateDevice(ctx, registry.NewDevice{Name: "Vacuum"})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	ref := store.Ref{SHA256: strings.Repeat("34", 32), Size: 10}
	if err := s.RecordBlob(ctx, ref, ""); err != nil {
		t.Fatalf("record blob: %v", err)
	}
	document, _, err := s.CreateDocument(ctx, registry.NewDocument{
		DeviceID: device.ID, BlobSHA256: ref.SHA256,
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}

	res := &doc.Result{
		Info:         doc.Info{Pages: 2},
		Pages:        []doc.Page{{No: 1, Chars: 100}, {No: 2, Chars: 100}},
		HasTextLayer: true,
		ContentStart: 1, ContentEnd: 2,
		BySource: map[doc.Source][]doc.Run{
			doc.SourceIndex: {
				{Source: doc.SourceIndex, Code: "EN", Lang: "en", Start: 1, End: 2},
				// Two different languages the index named but could not place.
				{Source: doc.SourceIndex, Code: "CZ", Lang: "cs", Start: 0, End: 0,
					Note: "printed index claims page 207, which is Arabic script"},
				{Source: doc.SourceIndex, Code: "PT", Lang: "pt", Start: 0, End: 0,
					Note: "printed index claims a page this document does not print"},
			},
		},
		Runs: []doc.Run{{Source: doc.SourceReconciled, Code: "EN", Lang: "en", Start: 1, End: 2}},
	}

	if err := s.SaveProbe(ctx, document.ID, res, registry.StateAwaitingScope); err != nil {
		t.Fatalf("save probe with unplaceable claims: %v", err)
	}

	runs, err := s.LanguageRuns(ctx, document.ID, doc.SourceIndex)
	if err != nil {
		t.Fatalf("language runs: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("got %d index runs, want 3 — an unplaceable claim was dropped: %+v", len(runs), runs)
	}

	unplaceable := 0
	for i := range runs {
		if runs[i].Start == 0 {
			unplaceable++
			if runs[i].Note == "" {
				t.Errorf("%s has no boundary and no explanation", runs[i].Code)
			}
		}
	}
	if unplaceable != 2 {
		t.Errorf("got %d unplaceable claims, want 2 (CZ and PT kept separately)", unplaceable)
	}
}

func TestLanguageRunsCarryADisplayName(t *testing.T) {
	// The UI shows "Ukrainian", not "uk", and the manual's own label may be neither.
	s := newService(t)
	ctx := context.Background()

	device, _ := s.CreateDevice(ctx, registry.NewDevice{Name: "Vacuum"})
	ref := store.Ref{SHA256: strings.Repeat("12", 32), Size: 10}
	if err := s.RecordBlob(ctx, ref, ""); err != nil {
		t.Fatalf("record blob: %v", err)
	}
	document, _, err := s.CreateDocument(ctx, registry.NewDocument{
		DeviceID: device.ID, BlobSHA256: ref.SHA256,
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}

	res := &doc.Result{
		Info:  doc.Info{Pages: 2},
		Pages: []doc.Page{{No: 1, Chars: 100}, {No: 2, Chars: 100}},
		Runs: []doc.Run{
			// The document prints UA; the tag is uk.
			{Source: doc.SourceReconciled, Code: "UA", Lang: "uk", Start: 1, End: 2},
		},
		HasTextLayer: true, ContentStart: 1, ContentEnd: 2,
	}
	if err := s.SaveProbe(ctx, document.ID, res, registry.StateAwaitingScope); err != nil {
		t.Fatalf("save probe: %v", err)
	}

	runs, err := s.LanguageRuns(ctx, document.ID, doc.SourceReconciled)
	if err != nil {
		t.Fatalf("language runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
	if runs[0].Code != "UA" || runs[0].Lang != "uk" || runs[0].Name != "Ukrainian" {
		t.Errorf("run = code %q, lang %q, name %q; want UA/uk/Ukrainian",
			runs[0].Code, runs[0].Lang, runs[0].Name)
	}
	if runs[0].Pages != 2 {
		t.Errorf("pages = %d, want 2", runs[0].Pages)
	}
}

func TestAnUnplaceableClaimCoversNoPages(t *testing.T) {
	// A run with pdf_start = 0 named a language it could not place, which the schema
	// documents as a real state. Both the API and the summary query measured it as
	// pdf_end - pdf_start + 1, so GET /documents/{id}/languages?source=index reported
	// the fixture's unplaceable Arabic entry as a one-page section spanning 0-0 — a
	// section of a language the manual's contents table merely mentions.
	dir := t.TempDir()
	database, err := db.Open(context.Background(), db.Options{Path: filepath.Join(dir, "registry.db")})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	s := registry.New(database, registry.Options{})
	ctx := context.Background()

	device, err := s.CreateDevice(ctx, registry.NewDevice{Name: "Robot vacuum"})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	ref := store.Ref{SHA256: strings.Repeat("56", 32), Size: 10}
	if err := s.RecordBlob(ctx, ref, ""); err != nil {
		t.Fatalf("record blob: %v", err)
	}
	document, _, err := s.CreateDocument(ctx, registry.NewDocument{
		DeviceID: device.ID, BlobSHA256: ref.SHA256,
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}

	res := &doc.Result{
		Info:         doc.Info{Pages: 2},
		Pages:        []doc.Page{{No: 1, Chars: 100}, {No: 2, Chars: 100}},
		HasTextLayer: true,
		ContentStart: 1, ContentEnd: 2,
		BySource: map[doc.Source][]doc.Run{
			doc.SourceIndex: {
				{Source: doc.SourceIndex, Code: "EN", Lang: "en", Start: 1, End: 2},
				{Source: doc.SourceIndex, Code: "AR", Lang: "ar", Start: 0, End: 0,
					Note: "printed index claims a page this document does not print"},
			},
		},
		Runs: []doc.Run{{Source: doc.SourceReconciled, Code: "EN", Lang: "en", Start: 1, End: 2}},
	}
	if err := s.SaveProbe(ctx, document.ID, res, registry.StateAwaitingScope); err != nil {
		t.Fatalf("save probe: %v", err)
	}

	runs, err := s.LanguageRuns(ctx, document.ID, doc.SourceIndex)
	if err != nil {
		t.Fatalf("language runs: %v", err)
	}
	for i := range runs {
		want := 2
		if runs[i].Code == "AR" {
			want = 0
		}
		if runs[i].Pages != want {
			t.Errorf("%s covers %d pages, want %d", runs[i].Code, runs[i].Pages, want)
		}
	}

	// The summary query does the same arithmetic in SQL, and nothing in Go protects
	// it, so it is asserted against the database rather than through the service.
	rows, err := gen.New(database.Read()).SummarizeDocLangs(ctx, gen.SummarizeDocLangsParams{
		DocumentID: document.ID,
		Source:     string(doc.SourceIndex),
	})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d summary rows, want 2: %+v", len(rows), rows)
	}
	for i := range rows {
		want := int64(2)
		if rows[i].Code == "AR" {
			want = 0
		}
		if rows[i].Pages != want {
			t.Errorf("summary says %s covers %d pages, want %d", rows[i].Code, rows[i].Pages, want)
		}
	}
}
