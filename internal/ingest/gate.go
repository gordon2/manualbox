package ingest

import (
	"context"
	"fmt"

	"github.com/gordon2/manualbox/internal/doc"
	"github.com/gordon2/manualbox/internal/registry"
)

// Gate is the pre-flight question: what manualbox is holding, what it would
// process, and what that would cost — asked before anything is spent.
//
// It is built entirely from stored probe results, so it survives a restart and
// costs nothing to render. Re-probing a document to answer "what is in this?"
// would defeat the purpose of having probed it.
type Gate struct {
	DocumentID string `json:"documentId"`
	DeviceID   string `json:"deviceId"`
	Filename   string `json:"filename,omitempty"`
	Kind       string `json:"kind"`
	State      string `json:"state"`
	Probed     bool   `json:"probed"`

	Pages        int  `json:"pages"`
	Encrypted    bool `json:"encrypted"`
	HasTextLayer bool `json:"hasTextLayer"`
	MedianChars  int  `json:"medianChars"`

	// Household is the configured reading languages, echoed back so the UI can
	// explain why a section is in or out of scope.
	Household []string `json:"household"`

	// InScope are the document's languages the household reads.
	InScope []registry.LanguageRun `json:"inScope"`
	// Other are the languages present that the household does not read. They are
	// listed, never discarded: the original is kept whole, so importing one later
	// is a button rather than a re-upload.
	Other []registry.LanguageRun `json:"other"`

	ScopePages    int     `json:"scopePages"`
	ScopeFraction float64 `json:"scopeFraction"`

	// Conflicts is how many runs the signals disagreed about. Surfaced rather than
	// resolved silently.
	Conflicts int `json:"conflicts"`
	// UnlabelledPages is how many content pages no signal could name.
	UnlabelledPages int `json:"unlabelledPages"`

	// RequiresApproval reports whether the document exceeds ingest.max_pages_auto
	// and so may not be processed without the user saying yes.
	RequiresApproval bool `json:"requiresApproval"`
	MaxPagesAuto     int  `json:"maxPagesAuto"`

	// Cost is what processing the scope would cost. It is deliberately not a
	// guess: see [CostEstimate].
	Cost CostEstimate `json:"cost"`

	// Summary is a one-line human description of the situation.
	Summary string `json:"summary"`
}

// CostEstimate is what the scope would cost to process.
//
// When no AI provider is configured there is no honest number to show. A token
// count depends on a specific model's tokeniser, and a currency figure depends on
// a billing mode — a metered key bills money, a subscription draws down a rolling
// window, a local model costs nothing at all. Inventing a figure from a character
// count would be the kind of estimate that turns out wrong by a factor of two, so
// Available stays false and Reason says why. See docs/design/providers.md.
type CostEstimate struct {
	Available bool `json:"available"`
	// Chars is measured, free, and always present: the extracted character count
	// of the pages in scope. It is a real quantity rather than a prediction.
	Chars  int    `json:"chars"`
	Reason string `json:"reason,omitempty"`
}

// Gate assembles the pre-flight answer for a document.
func (s *Service) Gate(ctx context.Context, documentID string) (*Gate, error) {
	document, err := s.registry.GetDocument(ctx, documentID)
	if err != nil {
		return nil, err
	}

	g := &Gate{
		DocumentID:   document.ID,
		DeviceID:     document.DeviceID,
		Filename:     document.Filename,
		Kind:         document.Kind,
		State:        document.State,
		Probed:       document.Probed(),
		Household:    s.cfg.Content.Languages,
		MaxPagesAuto: s.cfg.Ingest.MaxPagesAuto,
		InScope:      []registry.LanguageRun{},
		Other:        []registry.LanguageRun{},
	}

	if document.PageCount != nil {
		g.Pages = *document.PageCount
	}
	if document.Encrypted != nil {
		g.Encrypted = *document.Encrypted
	}
	if document.HasTextLayer != nil {
		g.HasTextLayer = *document.HasTextLayer
	}
	if document.MedianCharsPerPage != nil {
		g.MedianChars = *document.MedianCharsPerPage
	}
	g.RequiresApproval = g.Pages > s.cfg.Ingest.MaxPagesAuto

	if !g.Probed {
		g.Summary = "Not yet read."
		g.Cost.Reason = "the document has not been read yet"
		return g, nil
	}

	runs, err := s.registry.LanguageRuns(ctx, document.ID, doc.SourceReconciled)
	if err != nil {
		return nil, err
	}

	// Collapse runs to one entry per language, keeping the most specific label.
	type acc struct {
		run   registry.LanguageRun
		pages int
	}
	order := make([]string, 0, len(runs))
	byLang := make(map[string]*acc, len(runs))
	for i := range runs {
		r := &runs[i]
		if r.Conflict {
			g.Conflicts++
		}
		key := doc.BaseLanguage(r.Lang)
		if key == "" {
			key = r.Code
		}
		a, ok := byLang[key]
		if !ok {
			byLang[key] = &acc{run: *r, pages: r.Pages}
			order = append(order, key)
			continue
		}
		a.pages += r.Pages
		if len(r.Lang) > len(a.run.Lang) {
			a.run = *r
		}
		if r.Start < a.run.Start {
			a.run.Start = r.Start
		}
		if r.End > a.run.End {
			a.run.End = r.End
		}
	}

	for _, key := range order {
		a := byLang[key]
		entry := a.run
		entry.Pages = a.pages
		if _, reads := doc.MatchesAny(entry.Lang, s.cfg.Content.Languages); reads {
			g.InScope = append(g.InScope, entry)
			g.ScopePages += entry.Pages
		} else {
			g.Other = append(g.Other, entry)
		}
	}

	if g.Pages > 0 {
		g.ScopeFraction = float64(g.ScopePages) / float64(g.Pages)
	}

	g.Cost = s.costEstimate()
	g.Summary = g.summarize()
	return g, nil
}

// costEstimate reports what is known about cost, and admits what is not.
func (s *Service) costEstimate() CostEstimate {
	est := CostEstimate{}
	switch {
	case !s.cfg.Providers.Translate.Enabled() && !s.cfg.Providers.Extract.Enabled():
		est.Reason = "no AI provider is configured, so nothing would be sent anywhere"
	default:
		// A provider exists but no adapter is implemented yet, so there is still no
		// tokeniser to count with. Saying so beats printing a character-derived
		// guess that a real count would contradict.
		est.Reason = "a token estimate needs the configured provider's own tokeniser, which is not wired up yet"
	}
	return est
}

// summarize renders the sentence the gate leads with.
func (g *Gate) summarize() string {
	switch {
	case g.Encrypted:
		return "This document is password-protected, so it cannot be read. The original is stored unchanged."
	case !g.HasTextLayer:
		return fmt.Sprintf("%d pages with no text layer — this is a scan, and reading it needs OCR.", g.Pages)
	case len(g.InScope) == 0 && len(g.Other) > 0:
		return fmt.Sprintf("%d pages in %d languages, none of them yours. %s",
			g.Pages, len(g.Other)+len(g.InScope), listLanguages(g.Other, 4))
	case len(g.InScope) == 0:
		return fmt.Sprintf("%d pages, but no language could be identified.", g.Pages)
	}

	total := len(g.InScope) + len(g.Other)
	if total == 1 {
		return fmt.Sprintf("%d pages in %s.", g.Pages, g.InScope[0].Name)
	}
	return fmt.Sprintf("This manual contains %d languages across %d pages. Yours are %d of them — %d pages, %.0f%% of the document.",
		total, g.Pages, len(g.InScope), g.ScopePages, 100*g.ScopeFraction)
}

// listLanguages names up to limit languages for a human-readable sentence.
func listLanguages(runs []registry.LanguageRun, limit int) string {
	if len(runs) == 0 {
		return ""
	}
	names := make([]string, 0, limit)
	for i := range runs {
		if i == limit {
			return fmt.Sprintf("It has %s and %d more.", joinWords(names), len(runs)-limit)
		}
		names = append(names, runs[i].Name)
	}
	return fmt.Sprintf("It has %s.", joinWords(names))
}

func joinWords(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	case 2:
		return words[0] + " and " + words[1]
	}
	out := ""
	for i, w := range words[:len(words)-1] {
		if i > 0 {
			out += ", "
		}
		out += w
	}
	return out + " and " + words[len(words)-1]
}

// Decline records that the user does not want this document processed. The
// original is kept: declining is a decision about processing, never about storage.
func (s *Service) Decline(ctx context.Context, documentID string) error {
	return s.registry.SetDocumentState(ctx, documentID, registry.StateDeclined, "")
}
