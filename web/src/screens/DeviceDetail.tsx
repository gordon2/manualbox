import { useCallback, useEffect, useRef, useState } from "react";

import { api, ApiError, subscribeToJobs } from "../api/client";
import type { Device, Doc, Gate, GateLanguage } from "../api/types";
import { Alert, Button, Card } from "../ui";
import type { ReaderLanguage } from "./Reader";

/** One device: what it is, and the manuals belonging to it. */
export function DeviceDetail({
  device,
  onBack,
  onRead,
}: {
  device: Device;
  onBack: () => void;
  /** Open the reader. The languages come from the gate, which is already loaded here. */
  onRead: (doc: Doc, languages: ReaderLanguage[]) => void;
}) {
  const [documents, setDocuments] = useState<Doc[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const reload = useCallback(async () => {
    try {
      const { documents } = await api.documents(device.id);
      setDocuments(documents);
      setError(null);
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : "Could not load the documents.");
    }
  }, [device.id]);

  useEffect(() => {
    void reload();
    // Probing runs in the background, so the page follows the job stream rather
    // than polling: a finished probe should appear without a refresh.
    return subscribeToJobs(() => void reload());
  }, [reload]);

  return (
    <div className="space-y-8">
      <div>
        <button className="text-sm text-accent" onClick={onBack}>
          ← All devices
        </button>
        <h1 className="mt-2 font-display text-2xl text-ink">{device.name}</h1>
        {device.brand || device.model ? (
          <p className="mt-1 text-sm text-ink-soft">
            {[device.brand, device.model].filter(Boolean).join(" ")}
          </p>
        ) : null}
      </div>

      <Upload deviceId={device.id} onUploaded={reload} />

      <section>
        <h2 className="font-display text-lg text-ink">Documents</h2>
        {error ? (
          <div className="mt-3">
            <Alert>{error}</Alert>
          </div>
        ) : null}

        {documents === null ? (
          <Card className="mt-3 px-4 py-8 text-center text-sm text-ink-faint">Loading…</Card>
        ) : documents.length === 0 ? (
          <Card className="mt-3 px-4 py-8 text-center text-sm text-ink-faint">
            No documents yet. Upload the manual above.
          </Card>
        ) : (
          <ul className="mt-3 space-y-3">
            {documents.map((document) => (
              <DocumentCard
                key={document.id}
                document={document}
                onChanged={reload}
                onRead={onRead}
              />
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

function Upload({ deviceId, onUploaded }: { deviceId: string; onUploaded: () => void }) {
  const input = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);

  async function send(file: File) {
    setBusy(true);
    setError(null);
    setNote(null);
    try {
      const { duplicate } = await api.uploadDocument(deviceId, file);
      setNote(
        duplicate
          ? "You already had this exact file, so nothing was duplicated."
          : "Uploaded. Reading it now — this costs nothing.",
      );
      onUploaded();
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : "The upload failed.");
    } finally {
      setBusy(false);
      if (input.current) input.current.value = "";
    }
  }

  return (
    <section>
      <h2 className="font-display text-lg text-ink">Add a manual</h2>
      <p className="mt-1 max-w-prose text-sm text-ink-soft">
        The file is stored unchanged and read locally to work out what it contains. Nothing is sent
        anywhere and nothing is spent until you say so.
      </p>
      <div className="mt-3 flex items-center gap-3">
        <input
          ref={input}
          type="file"
          accept="application/pdf,.pdf"
          disabled={busy}
          onChange={(event) => {
            const file = event.target.files?.[0];
            if (file) void send(file);
          }}
          className="block w-full cursor-pointer rounded-md border border-rule bg-paper-raised px-3 py-2 text-sm text-ink-soft file:mr-3 file:rounded file:border-0 file:bg-rule/60 file:px-3 file:py-1.5 file:text-sm file:text-ink"
        />
      </div>
      {busy ? <p className="mt-2 text-xs text-ink-faint">Uploading…</p> : null}
      {note ? <p className="mt-2 text-xs text-ink-faint">{note}</p> : null}
      {error ? (
        <div className="mt-2">
          <Alert>{error}</Alert>
        </div>
      ) : null}
    </section>
  );
}

const stateLabels: Record<Doc["state"], string> = {
  uploaded: "queued to read",
  probing: "reading…",
  awaiting_scope: "waiting for you",
  declined: "not processed",
  converting: "converting…",
  ready: "ready",
  failed: "failed",
};

function DocumentCard({
  document,
  onChanged,
  onRead,
}: {
  document: Doc;
  onChanged: () => void;
  onRead: (doc: Doc, languages: ReaderLanguage[]) => void;
}) {
  const [gate, setGate] = useState<Gate | null>(null);

  // Which languages the reader may ask for. The gate's in-scope list is exactly what
  // approving converted — approve takes no language argument for that reason — so it
  // is the right list, and it costs no extra request because the gate is already here.
  //
  // Ordered biggest first, the same way the gate lists them, so the reader opens on
  // the language most of the document is in rather than on whichever sorts first.
  const languages: ReaderLanguage[] = gate
    ? bySize(gate.inScope).map((run) => ({ lang: run.lang, name: run.name }))
    : [];

  useEffect(() => {
    if (!document.probedAt) {
      setGate(null);
      return;
    }
    api
      .documentGate(document.id)
      .then(setGate)
      .catch(() => undefined);
  }, [document.id, document.probedAt, document.state]);

  return (
    <li>
      <Card className="p-4">
        <div className="flex items-center gap-3">
          <span className="truncate text-sm font-medium text-ink">
            {document.filename || "Untitled document"}
          </span>
          <span
            className={`shrink-0 text-xs ${
              document.state === "failed"
                ? "text-danger"
                : document.state === "awaiting_scope"
                  ? "text-accent"
                  : "text-ink-faint"
            }`}
          >
            {stateLabels[document.state]}
          </span>
          <div className="ml-auto flex shrink-0 items-center gap-3">
            {/* Only a converted document has a reader. `converting` gets one too,
                because the reader is where the progress belongs once you have asked
                for it — and it fills in by itself when the job finishes. */}
            {document.state === "ready" || document.state === "converting" ? (
              <button className="text-xs text-accent" onClick={() => onRead(document, languages)}>
                Read
              </button>
            ) : null}
            <a
              className="text-xs text-accent"
              href={api.documentContentURL(document.id)}
              target="_blank"
              rel="noreferrer"
            >
              Original
            </a>
          </div>
        </div>

        {document.lastError ? (
          <p className="mt-2 text-xs text-danger">{document.lastError}</p>
        ) : null}

        {gate ? <GatePanel gate={gate} onChanged={onChanged} /> : null}
      </Card>
    </li>
  );
}

/**
 * The pre-flight gate. It states what the document is, what would be processed,
 * and what that costs, before anything is spent — and it lists the languages that
 * are *not* being processed, because the original is kept whole and importing one
 * later must be a button rather than a re-upload.
 */
function GatePanel({ gate, onChanged }: { gate: Gate; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);
  const [showOther, setShowOther] = useState(false);

  const inScope = bySize(gate.inScope);
  const other = bySize(gate.other);

  async function decline() {
    setBusy(true);
    try {
      await api.declineDocument(gate.documentId);
      onChanged();
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mt-3 border-t border-rule pt-3">
      <p className="text-[15px] leading-relaxed text-ink">{gate.summary}</p>

      <dl className="mt-3 grid grid-cols-2 gap-x-4 gap-y-1.5 text-xs sm:grid-cols-4">
        <Stat label="Pages" value={count(gate.pages)} />
        {/* Characters lead and pages are context: this row used to read "52 (76%)"
            by pages for a document of which the household reads 40% of the text. */}
        <Stat
          label="In your languages"
          value={`${count(gate.scopeChars)} chars · ${percentOfText(gate.scopeCharFraction)}`}
          note={`on ${count(gate.scopePages)} of ${count(gate.pages)} pages`}
        />
        <Stat label="Text layer" value={gate.hasTextLayer ? "yes" : "no — needs OCR"} />
        <Stat label="Median chars/page" value={count(gate.medianChars)} />
      </dl>

      {inScope.length > 0 ? (
        <div className="mt-3">
          <h3 className="text-xs font-medium text-ink-soft">Would be processed</h3>
          <ul className="mt-1.5 space-y-2">
            {inScope.map((run) => (
              <LanguageRow key={run.code} run={run} />
            ))}
          </ul>
        </div>
      ) : null}

      {other.length > 0 ? (
        <div className="mt-3">
          <p className="text-xs text-ink-soft">Also present: {nameList(other, 6)}</p>
          <button
            className="mt-1 text-xs text-accent"
            onClick={() => setShowOther((v) => !v)}
            aria-expanded={showOther}
          >
            {showOther ? "Hide" : "Show"} how much of the text{" "}
            {other.length === 1 ? "it takes" : "each takes"}
          </button>
          {showOther ? (
            <ul className="mt-1.5 space-y-2">
              {other.map((run) => (
                <LanguageRow key={run.code} run={run} />
              ))}
            </ul>
          ) : null}
          <p className="mt-1.5 text-xs text-ink-faint">
            These are kept in the original and can be imported later without re-uploading.
          </p>
        </div>
      ) : null}

      {gate.unlabelledPages > 0 ? (
        <p className="mt-3 text-xs text-ink-faint">
          {count(gate.unlabelledPages)}{" "}
          {gate.unlabelledPages === 1 ? "page carries" : "pages carry"} text that no signal could
          name, so {gate.unlabelledPages === 1 ? "it counts" : "they count"} towards none of the
          languages above.
        </p>
      ) : null}

      {gate.conflicts > 0 ? (
        <p className="mt-3 text-xs text-ink-faint">
          {count(gate.conflicts)} {gate.conflicts === 1 ? "section" : "sections"} where the
          document&rsquo;s own contents table disagrees with the pages themselves. Shown rather than
          guessed at.
        </p>
      ) : null}

      <div className="mt-3 flex flex-wrap items-center gap-2">
        {/* Conversion does not exist yet, so there is nothing to approve — saying
            so beats offering a button that would do nothing. It counts characters
            because "Import 52 pages" is the misleading unit the rest of this panel
            stopped using: cost.chars is the same measured quantity as scopeChars,
            carried on the struct a caller asks about spending. */}
        <Button variant="quiet" disabled title="Conversion arrives in the next slice">
          Import{gate.inScope.length > 0 ? ` ${count(gate.cost.chars)} characters` : ""}
        </Button>
        {gate.state !== "declined" ? (
          <Button variant="quiet" onClick={decline} disabled={busy}>
            Not now
          </Button>
        ) : null}
        <span className="text-xs text-ink-faint">
          {gate.cost.available ? null : gate.cost.reason}
        </span>
      </div>
    </div>
  );
}

function Stat({ label, value, note }: { label: string; value: string; note?: string }) {
  return (
    <div>
      <dt className="text-ink-faint">{label}</dt>
      <dd className="tabular-nums text-ink">{value}</dd>
      {note ? <dd className="tabular-nums text-ink-faint">{note}</dd> : null}
    </div>
  );
}

/**
 * One of the document's languages, measured in characters first.
 *
 * The old row read "26 pp · 2–62", which on a manual laid out in parallel columns
 * says "26 pages of German" where German is a fifth of each of those 26 pages. So
 * the characters and the share lead, the pages are a locator underneath, and
 * sharesPages decides which sentence describes them.
 */
function LanguageRow({ run }: { run: GateLanguage }) {
  // A language below 1% of the text is not a peer of one at 20%, and the manuals
  // measured here have one: 289 characters of Finnish read out of a single table
  // cell. It is shown rather than filtered — that would need the threshold
  // regions.md refused — but shown at a precision, and in an emphasis, that says
  // what it is.
  const negligible = run.share > 0 && run.share < 0.01;
  return (
    <li className="text-xs">
      <div className="flex items-baseline gap-2">
        <span className={negligible ? "text-ink-faint" : "text-ink"}>{run.name}</span>
        <span className="shrink-0 font-mono text-ink-faint">{run.code}</span>
        {run.conflict ? (
          <span className="shrink-0 text-accent" title={run.note}>
            disputed
          </span>
        ) : null}
        <span className="ml-auto shrink-0 tabular-nums text-ink-soft">
          {count(run.chars)} chars · {percentOfText(run.share)} of the text
        </span>
      </div>
      <p className="mt-0.5 text-ink-faint">
        {/* The title is the section name the manual prints in its own contents
            table. Only the printed index can supply it, and only some documents
            have one. */}
        {run.title ? <span className="text-ink-soft">&ldquo;{run.title}&rdquo; · </span> : null}
        {placement(run)}
      </p>
    </li>
  );
}

/**
 * Where a language sits, worded by whether it owns its pages.
 *
 * The two layouts must not read the same. A sequential manual's section has its
 * pages to itself and a span is the whole truth about it; a column on a parallel
 * manual's page shares that page with four other languages, and "26 pages" without
 * that said is the misreading this screen existed to cause.
 */
function placement(run: GateLanguage): string {
  if (run.pages === 0 || run.start === 0) return "no pages could be placed";
  if (run.sharesPages) {
    // One shared page can be named, and "1 page, sharing each" is not English.
    if (run.pages === 1) return `appears on page ${count(run.start)}, shared with other languages`;
    return `appears on ${count(run.pages)} pages, sharing each with other languages`;
  }
  if (run.start === run.end) return `page ${count(run.start)}, all its own`;
  return `pages ${count(run.start)}–${count(run.end)}, all its own`;
}

/** Biggest first: a list headlined by size but ordered by page number invites the
 *  289-character language to be read as one of the real ones. */
function bySize(langs: GateLanguage[]): GateLanguage[] {
  return [...langs].sort((a, b) => b.chars - a.chars || a.name.localeCompare(b.name));
}

/** Names up to limit languages; a 34-language manual gets a count for the rest. */
function nameList(langs: GateLanguage[], limit: number): string {
  const names = langs.slice(0, limit).map((l) => l.name);
  const rest = langs.length - names.length;
  return rest > 0 ? `${names.join(", ")}, and ${count(rest)} more` : names.join(", ");
}

/** 47641 → "47,641", in the reader's own locale. */
function count(n: number): string {
  return n.toLocaleString();
}

/**
 * A share of the document's text as a percentage. Below 1% it keeps a decimal,
 * because 289 characters of 240,622 rounding to "0%" reads as a bug, and rounding
 * up to "1%" would overstate it by an order of magnitude.
 */
function percentOfText(share: number): string {
  const pct = 100 * share;
  if (pct > 0 && pct < 0.05) return "under 0.1%";
  const digits = pct > 0 && pct < 1 ? 1 : 0;
  return `${pct.toLocaleString(undefined, {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  })}%`;
}
