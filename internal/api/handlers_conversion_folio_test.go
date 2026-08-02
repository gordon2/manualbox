package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/registry"
	"github.com/gordon2/manualbox/internal/store"
)

// folioDoc stores a document whose pages print the given folios, converted and
// ready, and returns its id. A nil entry is a page that prints no folio.
func (h *harness) folioDoc(t *testing.T, digest string, folios []*int) string {
	t.Helper()
	ctx := context.Background()

	device, err := h.registry.CreateDevice(ctx, registry.NewDevice{Name: "Vacuum " + digest})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	ref := store.Ref{SHA256: strings.Repeat(digest, 32), Size: 10}
	if err := h.registry.RecordBlob(ctx, ref, "application/pdf"); err != nil {
		t.Fatalf("record blob: %v", err)
	}
	document, _, err := h.registry.CreateDocument(ctx, registry.NewDocument{
		DeviceID: device.ID, BlobSHA256: ref.SHA256, Filename: "manual.pdf",
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}

	pages := make([]doc.Page, 0, len(folios))
	for i, folio := range folios {
		pages = append(pages, doc.Page{No: i + 1, Chars: 100, Script: "Latin", Folio: folio})
	}
	res := &doc.Result{Info: doc.Info{Pages: len(folios)}, Pages: pages}
	if err := h.registry.SaveProbe(ctx, document.ID, res, registry.StateAwaitingScope); err != nil {
		t.Fatalf("save probe: %v", err)
	}
	if err := h.registry.SaveConversion(ctx, document.ID,
		[]doc.Block{para(1, "de", "Den Ausblasfilter tauschen.")}, nil, nil,
		registry.StateReady); err != nil {
		t.Fatalf("save conversion: %v", err)
	}
	return document.ID
}

func folioPtr(n int) *int { return &n }

// TestConversionServesFolioOffsetOrOmitsIt is the one contract on this field that a
// client cannot recover from getting wrong: absent and zero are different answers.
//
// A manual whose page 1 is its cover has a real offset of 0, and the columns manual
// measured here is exactly that. If the response defaulted to 0 where the folios
// agreed on nothing, every contents entry of an unmappable document would become a
// link to the wrong page -- so this asserts the key's PRESENCE, not just its value,
// which is why the body is decoded into a map rather than into the response struct.
func TestConversionServesFolioOffsetOrOmitsIt(t *testing.T) {
	h := newHarness(t)
	h.completeSetup(t)

	// Offset 6 on 8 of 8 pages: printed 1 is PDF page 7.
	offset6 := make([]*int, 0, 8)
	for i := range 8 {
		offset6 = append(offset6, folioPtr(i+1-6))
	}
	// Offset 0 on 8 of 8. The value that must not be confusable with absence.
	offset0 := make([]*int, 0, 8)
	for i := range 8 {
		offset0 = append(offset0, folioPtr(i+1))
	}
	// Folios restarting halfway: four at offset 0 and four at offset 4. Neither
	// holds a majority, so there is no answer to give.
	restarting := []*int{
		folioPtr(1), folioPtr(2), folioPtr(3), folioPtr(4),
		folioPtr(1), folioPtr(2), folioPtr(3), folioPtr(4),
	}
	// Nothing prints a folio at all.
	none := []*int{nil, nil, nil, nil, nil, nil, nil, nil}

	tests := []struct {
		name      string
		digest    string
		folios    []*int
		wantKey   bool
		wantValue float64
	}{
		{"a document whose front matter is six pages", "a", offset6, true, 6},
		{"a document whose page 1 is its cover", "b", offset0, true, 0},
		{"folios that restart halfway", "c", restarting, false, 0},
		{"no page prints a folio", "d", none, false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := h.folioDoc(t, tt.digest, tt.folios)
			res := h.do(t, "GET", "/api/v1/documents/"+id+"/conversion?lang=de", nil)
			defer func() { _ = res.Body.Close() }()

			var body map[string]any
			if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			got, present := body["folioOffset"]
			if present != tt.wantKey {
				t.Fatalf("folioOffset present = %v, want %v (body keys: %v).\n"+
					"Absent and zero are different answers here: absent means the folios "+
					"agreed on no offset, and a client that reads a missing field as 0 "+
					"links every contents entry to the wrong page.",
					present, tt.wantKey, keysOf(body))
			}
			if tt.wantKey && got != tt.wantValue {
				t.Fatalf("folioOffset = %v, want %v", got, tt.wantValue)
			}
		})
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
