# Contributing

## Getting set up

Go 1.25+, Node 22+. `sqlc` and `goose` are pinned in a separate `tools` module, so
there is nothing to install globally.

```sh
make web-install && make build
make check            # test + lint + typecheck; everything CI runs
./bin/manualbox doctor
```

poppler and tesseract are optional at runtime — features that need them report why
they are unavailable rather than failing. Install them to work on the document
pipeline: `brew install poppler tesseract` or
`apt install poppler-utils tesseract-ocr`.

## Conventions worth knowing before you start

These are the ones that have already caused real bugs here.

**Verify from a clean clone, not from your working tree.** The SPA is embedded with
`go:embed`, which needs `internal/frontend/dist` to be non-empty. Your machine
always has a build sitting there, so a missing committed placeholder is invisible
locally and breaks `go build` for everyone else. CI has a dedicated `clean-build`
job for exactly this. When a change adds a directory or touches embedding, clone
into a temp dir and build there.

**Anchor `.gitignore` patterns.** An unanchored `manualbox` line once matched
`cmd/manualbox/` and silently kept the whole command out of a commit. Write
`/manualbox`, not `manualbox`.

**Wrap SQLite aggregates in `CAST(... AS INTEGER)`.** Without it sqlc cannot infer
the type and emits `interface{}`, pushing a type assertion onto every caller. The
same applies to nullable parameters in `IS NULL OR =` filters — prefer two explicit
queries over one clever one.

**Choose the connection pool deliberately.** `db.Read()` for queries,
`db.Write()` for statements that modify. The writer is capped at one connection,
which is what stops handlers and background workers producing intermittent
"database is locked".

**Handlers must be idempotent.** A job can run twice: a worker may be killed after
doing its work but before recording success, and the reclaimed job runs again.

**Timestamps are integer milliseconds.** Convert only through `internal/db`'s
helpers, so the storage format stays in one place.

## Testing

`go test -race ./...` must pass, and the suite is hermetic — no network, no
external services. Tests that need a real document use `internal/fixture`, which
fetches on demand and skips unless `MANUALBOX_TEST_FIXTURES=1`.

Prefer tests that state a property rather than restating the implementation. The
valuable ones here assert things you cannot eyeball: that a tampered ciphertext
fails instead of returning altered plaintext, that a killed worker's job is
reclaimed, that a failed login is indistinguishable from an unknown account.

**Look at UI changes in a browser.** Three real bugs shipped past a green
typecheck and a clean build: a dark-mode theme block that overwrote the light
palette, a button that lost every class because `{...props}` spread after
`className`, and an API 404 that returned the HTML app.

## Never commit

- **Real documents or photos.** Manuals are copyrighted, and manualbox's own
  principle is not to redistribute them. Fixtures are manifests describing where to
  fetch a document — see `internal/fixture` and
  [docs/design/privacy.md](docs/design/privacy.md).
- **Anyone's personal data.** Test addresses use `example.com` (RFC 2606) and
  documentation IP ranges (RFC 5737). Not your own address: that is how a shared
  project starts reading as one person's private inventory.
- **Absolute paths from your machine** — they carry your OS username.
- Databases, `data/`, or `.env`.

CI enforces all of the above with a `hygiene` job. It is a grep, not a guarantee;
the judgement is still yours.

## Design decisions live in docs/design

Read the relevant one before changing that area — each explains *why*, usually with
measurements:

| | |
|---|---|
| [ingest.md](docs/design/ingest.md) | How a 560-page, 34-language manual is reduced to the 16 pages you actually read, before any model is called |
| [layouts.md](docs/design/layouts.md) | Manuals are arranged differently — sequential sections or parallel columns — and where the seam between them goes |
| [language-detection.md](docs/design/language-detection.md) | The four signals that say what language a page is in, what each one costs, and why the detector choice is deliberately still open |
| [providers.md](docs/design/providers.md) | Why a subscription CLI or a local model comes before a metered API key, and why a CLI adapter must batch a whole document |
| [privacy.md](docs/design/privacy.md) | What manualbox holds, ranked by how it actually leaks |
| [keys.md](docs/design/keys.md) | Encryption keys: choosing, storing, and recovering them |

When you make a decision that a future reader would otherwise have to re-derive,
write down the reasoning and the number that justified it. "Measured X, so Y" ages
far better than "Y".

## Commits and pull requests

Explain *why* in the commit message; the diff already shows what. If you measured
something to reach the decision, include the measurement.

Before pushing: `make check`, and a clean-clone build if the change touches
embedding, the build, or `.gitignore`.
