# manualbox

[![CI](https://github.com/gordon2/manualbox/actions/workflows/ci.yml/badge.svg)](https://github.com/gordon2/manualbox/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Your household's manuals — searchable, in your language, with the maintenance schedule already extracted.**

Self-hosted. Free. MIT. Single binary, SQLite, no external services required, and **no API key needed** to get real value out of it.

> ⚠️ Early development. You can create an account, add devices, and upload a manual — manualbox
> reads it locally and tells you what it contains, in which languages, before anything is
> converted or sent anywhere. **It stops there for now:** conversion, search, the reader and
> export are still to come. See the [Roadmap](#roadmap).

## Try it

With Docker — nothing to install, poppler and tesseract included:

```sh
git clone https://github.com/gordon2/manualbox.git && cd manualbox
docker compose -f deploy/docker-compose.yml up -d
# open http://localhost:7745 and create your account
```

Or from source (Go 1.25+, Node 22+):

```sh
make web-install && make build
./bin/manualbox doctor      # what is configured, and which optional tools are present
./bin/manualbox serve       # then open http://localhost:7745
```

No configuration, no API key, and no network access required. Everything lives in
one directory (`/data` in the container) — that is the whole backup.

---

## Why

Every home fills up with devices, and every device arrives with a manual. Three problems compound:

**You can't find anything.** The paper pile is unsearchable, and you need the router manual at exactly the moment the internet is down.

**The language you need is missing.** A typical appliance manual is one PDF containing the same content in fifteen languages — and yours isn't one of them.

**Maintenance is invisible.** Descale the machine every 3 months. Charge the stored battery every 90 days. Replace the filter after 200 hours. That knowledge is real, specific, and buried on page 47 of a PDF nobody opens.

Existing tools solve at most one of these. Self-hosted inventory apps treat a manual as an opaque PDF attached to a row — no text search inside it, no translation, and every maintenance schedule typed in by hand. Document managers search the text but have no idea what a device is or when it needs servicing.

And the cloud apps that did try this keep dying. **Centriq** did nearly this exact idea and shut down in January 2025: users got a short CSV-export window, had to download their photos one at a time, and lost their maintenance history when the deadline passed. That is the failure mode manualbox exists to make impossible.

## How it's different

manualbox treats a manual as **content**, not as an attachment.

Every document is converted to structured HTML — headings, paragraphs, tables, and extracted images — while the original file is preserved byte-for-byte forever. From that structured form, three things become possible that a PDF pile can't offer:

- **Multi-language booklets get split.** Per-block language detection finds the language runs in that fifteen-language PDF, keeps the one you read as canonical, and tells you which others are in there.
- **Translation is per-block and correctable.** A translated manual isn't a second document — it's the same structure with the text swapped, so images are never re-translated, a fixed term stays fixed, and bilingual side-by-side is just a view. Translate one section, not all eighty pages.
- **Maintenance schedules are extracted, not typed.** A model reads the manual and proposes the schedule — "descale every 3 months" — with a citation that deep-links to the exact paragraph it came from. You review and approve; nothing is created behind your back.

Then it gets out of the way: notifications where you already look, and a calendar feed you subscribe to from Apple or Google Calendar.

## Principles

**Own your data.** Complete JSON + files export ships in the first milestone, not as a someday promise. Originals are stored unmodified and content-addressed. If you stop using manualbox, you lose nothing.

**Works with nothing configured.** Text extraction, OCR, search, schedules, notifications, and the calendar feed all run locally with zero keys and zero network. AI features light up when *you* choose a provider.

**Prefer what you already pay for.** Translation and extraction sit behind interfaces, and the setup flow offers the free options first: a local model on your own hardware, or a CLI you have already logged into so the work counts against your existing Claude or ChatGPT plan rather than a card. A metered API key is supported and works well — it is simply not the default, because most people already pay for this once. There is no default provider and no vendor lock. See [docs/design/providers.md](docs/design/providers.md).

**Manuals are yours, not ours.** manualbox never redistributes manuals. It stores your copies for your household and links out to manufacturer pages for discovery. There is no shared manual repository, by design.

## Roadmap

| | |
|---|---|
| **M0** ✅ | Skeleton: config, SQLite + migrations, blob store, job queue, auth, API, frontend shell, Docker, CI |
| **M1** | Registry ✅, document probe and language map ✅, then conversion, reader, full-text search, **export** — see [ingest](docs/design/ingest.md) and [layouts](docs/design/layouts.md) |
| **M2** | Maintenance: schedules, battery charge cycles, service log, notifications, ICS calendar feed |
| **M3** | Translation: per-block, glossary, translation memory, side-by-side, post-editing |
| **M4** | Extraction: maintenance plans with citations, printable per-device cheat sheets, error-code lookup |

After v1.0: offline PWA, grounded chat over your own manuals, recall checks (EU Safety Gate / US CPSC), household multi-user, importers, handover-pack export.

## Development

Requires Go 1.25+ and Node 22+. `sqlc` and `goose` are pinned as Go tool dependencies — no global installs.

```sh
make dev        # run backend + frontend with live reload
make build      # build the single binary with the SPA embedded
make check      # test + lint + typecheck, everything CI runs
make doctor     # report which optional local tools are available
```

Optional external binaries, used when present and degraded gracefully when not (the Docker image includes them):

| Tool | Used for | macOS |
|---|---|---|
| `pdftotext`, `pdftoppm`, `pdfimages` (poppler) | PDF text/structure/image extraction, page rendering | `brew install poppler` |
| `tesseract` | OCR for scans and photos | `brew install tesseract tesseract-lang` |

Design decisions, with the measurements behind them, are in `docs/design/`:
[ingest](docs/design/ingest.md) · [layouts](docs/design/layouts.md) ·
[language detection](docs/design/language-detection.md) ·
[providers](docs/design/providers.md) · [privacy](docs/design/privacy.md) ·
[keys](docs/design/keys.md).

Conventions and the things that have already caused bugs here:
[CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT — see [LICENSE](LICENSE).
