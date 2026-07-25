# Personal data: what manualbox holds, and how to hold it

A household inventory is a more sensitive document than it first appears. Taken
together, manualbox knows what expensive things you own, which room each one is
in, when you bought them and for how much, their serial numbers, and photographs
of your home. That is simultaneously a burglary shopping list, an insurance
document, and — because device models map to known vulnerabilities — a network
reconnaissance report.

Two obligations follow. The repository must never contain anyone's real data, and
the running instance must hold it in a way that survives the ways it actually
leaks.

## The repository stays clean

This is a public repo, intended as something someone else can pick up and run. So:

- **No real documents.** Test fixtures are manifests describing where to fetch a
  document, never the document itself. See [ingest.md](ingest.md) and
  `internal/fixture`.
- **No real identities.** Test data uses `example.com` (reserved by RFC 2606),
  documentation IP ranges (`192.0.2.0/24`, `203.0.113.0/24` per RFC 5737), and
  neutral names. Not the maintainer's own address, which is how a repo starts
  reading as one person's private project.
- **No absolute paths from a developer's machine**, since those carry an operating
  system username.
- `data/`, `*.db`, and `.env` are gitignored, and CI greps for the patterns above
  so this does not erode as contributors arrive.

## Where data actually leaks

Encryption is not a plan; a threat model is. Ranked by how often each one really
happens:

| | Threat | What helps |
|---|---|---|
| 1 | **A backup escapes.** The data directory ends up in cloud storage, on a sold drive, or on a NAS share that turns out to be reachable. | Encrypting the high-harm fields with a key kept *outside* the data directory, so a copied folder is not sufficient. |
| 2 | **A user pastes logs or `doctor` output into a public issue.** | Logs carry no addresses; `doctor --redact` replaces the home directory. Both implemented. |
| 3 | **Data leaves the house to a cloud model.** A receipt photo sent to a vision model contains a name and address. | Receipts and warranties default to local-only processing; the UI states what egresses before it happens. |
| 4 | **An export is shared.** A handover pack for the buyer of a device should not disclose what you paid for it. | Redacted export as the default for sharing; full export only for your own backup. |
| 5 | **The running server is compromised.** | Application-level encryption does *not* help here — the key is on the same machine. Auth, no unnecessary exposure, and OS hardening do. |

Being explicit about (5) matters: field encryption is often adopted as though it
mitigated compromise. It does not. It mitigates (1), and it is worth doing for
exactly that reason and no other.

## Classify by harm, then treat each class differently

Rather than one blanket policy, four classes with genuinely different handling:

| Class | Examples | How it is stored |
|---|---|---|
| **Reference** | brand, model, manual text, canonical HTML, maintenance intervals | Plain. Indexed and searchable. Not really about you — a Bosch manual is a Bosch manual. |
| **Household** | locations, photos, notes, service history, usage counters | Plain in the data directory. Protected by authentication and by whatever full-disk encryption the host has. Searchable. |
| **High-harm** | **serial numbers**, receipts and invoices, purchase price | Encrypted at rest with a key held outside the data directory. Excluded from shared exports by default. Never sent to a cloud provider. |
| **Secret** | password hashes, session tokens, provider credentials | Already never stored recoverably: argon2id hashes, SHA-256 of session tokens, and a `Secret` type that redacts through fmt, slog, JSON, and YAML. |

### Serial numbers are the interesting case

They are the single most identifying field manualbox holds — the thing an insurer
wants, a thief wants, and a warranty claim needs. They also need to be *findable*:
"which of my devices is this serial on the police report?"

Encrypting them normally destroys that. The resolution is to store two derivations
and no plaintext:

- `serial_enc` — AES-GCM ciphertext, decrypted only to display on the device page.
- `serial_hmac` — a keyed HMAC, indexed. Exact-match lookup works by hashing the
  query and comparing, so search is preserved without a plaintext column.

Substring search over serials is lost. That is an acceptable trade: nobody
remembers half a serial number, they paste the whole thing.

### Where the key lives

Not in the data directory — that would defeat the entire purpose, since threat (1)
is someone copying that directory. Options, in order of preference:

1. `MANUALBOX_DATA_KEY` in the environment or a Docker secret. The key travels with
   the deployment configuration rather than the data.
2. A key file at a path given by config, outside the data directory.
3. Derived from an operator passphrase entered at startup — strongest against a
   stolen backup, but the instance cannot restart unattended, which is wrong for a
   household server that should come back after a power cut.

Option 1 is the default, but it is not the whole story: a key that only exists in
one deployment's configuration is a key you cannot restore a backup with. So the
data key is wrapped by *both* an operational key and a passphrase the user chooses,
and either opens it — the operational key for unattended boots, the passphrase for
recovering onto a machine that has neither the key nor the config.

The full design, including where each piece is stored, how to back it up, and how
to test that backup, is in **[keys.md](keys.md)**. Implemented in
`internal/keyring`.

The trade still has to be stated before anyone opts in: **lose both the passphrase
and the operational key and those fields are unrecoverable.** A household tool that
silently destroys someone's warranty records because they rebuilt a container
without the secret would be worse than not encrypting at all. So it is opt-in, the
recovery kit is printed up front, declining is a permanent and respected choice,
and everything outside the high-harm class stays readable regardless.

## Two rules for the AI providers

**Receipts and warranties never go to a cloud provider.** They are the documents
that carry a name, an address, and sometimes a card fragment. If a cloud provider
is configured and the document class is receipt or warranty, conversion falls back
to the local path (poppler and tesseract) and extraction is skipped rather than
silently uploading it. A per-document override exists; the default is the safe one.

**Say what leaves before it leaves.** The pre-flight gate already shows what a job
will cost. When the provider is remote it should also say what is being sent —
"16 pages of the English section" — because that is the moment the user can still
say no. This is another reason the local-model path ranks first in
[providers.md](providers.md): nothing egresses at all.

## Status

Implemented today: repository hygiene and the CI guard, `doctor --redact`, no
addresses in logs (with a test that fails if one reappears), the `Secret` type,
argon2id password hashing, session tokens stored only as hashes, and the keyring
itself — envelope encryption, the searchable index for serial numbers, and the
refusal to remove the last unlock method.

Deferred until there is actually a receipt or a serial number to hold: wiring the
keyring into the schema, the `manualbox keys` commands, redacted exports, and the
local-only rule for receipts. Those land with M1 and M2.
