import { useCallback, useEffect, useRef, useState } from "react";

import { api, ApiError, subscribeToJobs } from "../api/client";
import type { Device, Doc, Gate, LanguageRun } from "../api/types";
import { Alert, Button, Card } from "../ui";

/** One device: what it is, and the manuals belonging to it. */
export function DeviceDetail({ device, onBack }: { device: Device; onBack: () => void }) {
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
        {error ? <div className="mt-3"><Alert>{error}</Alert></div> : null}

        {documents === null ? (
          <Card className="mt-3 px-4 py-8 text-center text-sm text-ink-faint">Loading…</Card>
        ) : documents.length === 0 ? (
          <Card className="mt-3 px-4 py-8 text-center text-sm text-ink-faint">
            No documents yet. Upload the manual above.
          </Card>
        ) : (
          <ul className="mt-3 space-y-3">
            {documents.map((document) => (
              <DocumentCard key={document.id} document={document} onChanged={reload} />
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
      {error ? <div className="mt-2"><Alert>{error}</Alert></div> : null}
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

function DocumentCard({ document, onChanged }: { document: Doc; onChanged: () => void }) {
  const [gate, setGate] = useState<Gate | null>(null);

  useEffect(() => {
    if (!document.probedAt) {
      setGate(null);
      return;
    }
    api.documentGate(document.id).then(setGate).catch(() => undefined);
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
          <a
            className="ml-auto shrink-0 text-xs text-accent"
            href={api.documentContentURL(document.id)}
            target="_blank"
            rel="noreferrer"
          >
            Original
          </a>
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
        <Stat label="Pages" value={String(gate.pages)} />
        <Stat label="In your languages" value={`${gate.scopePages} (${Math.round(gate.scopeFraction * 100)}%)`} />
        <Stat label="Text layer" value={gate.hasTextLayer ? "yes" : "no — needs OCR"} />
        <Stat label="Median chars/page" value={String(gate.medianChars)} />
      </dl>

      {gate.inScope.length > 0 ? (
        <div className="mt-3">
          <h3 className="text-xs font-medium text-ink-soft">Would be processed</h3>
          <ul className="mt-1.5 space-y-1">
            {gate.inScope.map((run) => (
              <LanguageRow key={run.code} run={run} />
            ))}
          </ul>
        </div>
      ) : null}

      {gate.other.length > 0 ? (
        <div className="mt-3">
          <button
            className="text-xs text-accent"
            onClick={() => setShowOther((v) => !v)}
            aria-expanded={showOther}
          >
            {showOther ? "Hide" : "Show"} the other {gate.other.length}{" "}
            {gate.other.length === 1 ? "language" : "languages"} in this document
          </button>
          {showOther ? (
            <ul className="mt-1.5 space-y-1">
              {gate.other.map((run) => (
                <LanguageRow key={run.code} run={run} />
              ))}
            </ul>
          ) : null}
          <p className="mt-1.5 text-xs text-ink-faint">
            These are kept in the original and can be imported later without re-uploading.
          </p>
        </div>
      ) : null}

      {gate.conflicts > 0 ? (
        <p className="mt-3 text-xs text-ink-faint">
          {gate.conflicts} {gate.conflicts === 1 ? "section" : "sections"} where the document&rsquo;s
          own contents table disagrees with the pages themselves. Shown rather than guessed at.
        </p>
      ) : null}

      <div className="mt-3 flex flex-wrap items-center gap-2">
        {/* Conversion does not exist yet, so there is nothing to approve — saying
            so beats offering a button that would do nothing. */}
        <Button variant="quiet" disabled title="Conversion arrives in the next slice">
          Import{gate.inScope.length > 0 ? ` ${gate.scopePages} pages` : ""}
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

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-ink-faint">{label}</dt>
      <dd className="text-ink">{value}</dd>
    </div>
  );
}

function LanguageRow({ run }: { run: LanguageRun }) {
  return (
    <li className="flex items-baseline gap-2 text-xs">
      <span className="w-14 shrink-0 font-mono text-ink-faint">{run.code}</span>
      <span className="text-ink">{run.name}</span>
      {run.title ? <span className="truncate text-ink-faint">{run.title}</span> : null}
      <span className="ml-auto shrink-0 text-ink-faint">
        {run.pages} pp · {run.start}–{run.end}
      </span>
      {run.conflict ? (
        <span className="shrink-0 text-accent" title={run.note}>
          disputed
        </span>
      ) : null}
    </li>
  );
}
