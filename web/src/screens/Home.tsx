import { useCallback, useEffect, useState } from "react";

import { api, ApiError, subscribeToJobs } from "../api/client";
import type { Device, Doc, Instance, Job, JobState, SearchHit, User } from "../api/types";
import { Alert, Button, Card, Wordmark } from "../ui";
import { DeviceDetail } from "./DeviceDetail";
import { Devices } from "./Devices";
import { Reader, readerLanguages, type ReaderLanguage } from "./Reader";
import { SearchBox, SearchHits } from "./Search";

/**
 * What the reader is showing, and what it goes back to.
 *
 * `backTo` is carried rather than derived, because the reader is now reached two ways:
 * from a device, and from a search hit that may belong to a device that is not open.
 * A back link reading "← Wet and dry vacuum" that returned to a list of search results
 * would be a lie about where it goes.
 */
interface Reading {
  doc: Doc;
  languages: ReaderLanguage[];
  backTo: string;
  startLang?: string | undefined;
  startPage?: number | undefined;
}

export function Home({ user, onSignedOut }: { user: User; onSignedOut: () => void }) {
  const [instance, setInstance] = useState<Instance | null>(null);
  const [jobs, setJobs] = useState<Job[]>([]);
  const [streamLive, setStreamLive] = useState(false);
  // Navigation is a single piece of state rather than a router: there are two
  // screens, and a dependency to move between them would not earn its place yet.
  const [openDevice, setOpenDevice] = useState<Device | null>(null);
  // The reader is the third, and it is deliberately held here rather than inside
  // DeviceDetail: it takes the whole page, including the space the activity list
  // occupies, and a screen cannot hide its own parent's sections. Closing it falls
  // back to the device that is still open underneath.
  const [reading, setReading] = useState<Reading | null>(null);
  // A submitted query, which is the fourth screen. It sits above the device list
  // rather than replacing it in the same slot, because search spans the household:
  // "which manual says X" is asked by someone who does not know which device to open,
  // so it cannot live inside one.
  const [query, setQuery] = useState("");
  // Following a hit needs the document and its languages, neither of which is in the
  // hit, so it is two requests and can fail while the user waits.
  const [opening, setOpening] = useState<string | null>(null);
  const [openError, setOpenError] = useState<string | null>(null);

  const reloadJobs = useCallback(async () => {
    try {
      const { jobs } = await api.jobs(undefined, 25);
      setJobs(jobs);
    } catch {
      // The stream indicator already communicates connection trouble.
    }
  }, []);

  useEffect(() => {
    api.instance().then(setInstance).catch(() => undefined);
    void reloadJobs();

    // Any event means something changed; refetch rather than patching state from
    // the event, so the list is always what the server actually holds.
    const unsubscribe = subscribeToJobs(
      () => {
        setStreamLive(true);
        void reloadJobs();
      },
      () => setStreamLive(false),
    );
    // EventSource fires no event on a successful idle connection, so assume the
    // stream is up shortly after opening and let onerror correct it.
    const timer = setTimeout(() => setStreamLive(true), 500);

    return () => {
      clearTimeout(timer);
      unsubscribe();
    };
  }, [reloadJobs]);

  /**
   * Take a hit to the page it names.
   *
   * A hit carries the ids but not the document itself, and the reader needs the
   * document for its title and page count and the gate for the languages it may ask
   * for — the same list DeviceDetail hands it. Both are fetched here rather than
   * joined into the search response, which docs/design/search.md keeps deliberately
   * flat.
   */
  async function openHit(hit: SearchHit) {
    setOpening(hit.documentId);
    setOpenError(null);
    try {
      const [{ documents }, gate] = await Promise.all([
        api.documents(hit.deviceId),
        api.documentGate(hit.documentId),
      ]);
      const doc = documents.find((candidate) => candidate.id === hit.documentId);
      if (!doc) {
        setOpenError("That manual is no longer there. Search again to see what is.");
        return;
      }
      setReading({
        doc,
        languages: readerLanguages(gate),
        backTo: `Results for “${query}”`,
        startLang: hit.lang,
        startPage: hit.page,
      });
    } catch (cause) {
      setOpenError(cause instanceof ApiError ? cause.message : "Could not open that manual.");
    } finally {
      setOpening(null);
    }
  }

  async function signOut() {
    try {
      await api.logout();
    } catch (cause) {
      // An expired session cannot be logged out of, which is fine.
      if (!(cause instanceof ApiError)) throw cause;
    }
    onSignedOut();
  }

  return (
    <div className="min-h-dvh">
      <header className="border-b border-rule">
        <div className="mx-auto flex max-w-3xl items-center justify-between gap-4 px-6 py-4">
          <Wordmark className="text-lg text-ink" />
          <div className="flex items-center gap-3">
            <span className="text-sm text-ink-soft">{user.displayName || user.email}</span>
            <Button variant="quiet" onClick={signOut}>
              Sign out
            </Button>
          </div>
        </div>
      </header>

      <main className="mx-auto max-w-3xl space-y-8 px-6 py-10">
        {reading ? (
          <Reader
            doc={reading.doc}
            backTo={reading.backTo}
            languages={reading.languages}
            startLang={reading.startLang}
            startPage={reading.startPage}
            onBack={() => setReading(null)}
          />
        ) : (
          <>
            <section className="space-y-3">
              <SearchBox query={query} onSearch={setQuery} />
              {openError ? <Alert>{openError}</Alert> : null}
              {query ? <SearchHits query={query} onOpen={openHit} opening={opening} /> : null}
            </section>

            {/* A query takes over the page. The library is still one click away — the
                box empties — and leaving the device list under 25 hits would bury it
                and the activity list both. */}
            {query ? null : openDevice ? (
              <DeviceDetail
                device={openDevice}
                onBack={() => setOpenDevice(null)}
                onRead={(doc, languages) => setReading({ doc, languages, backTo: openDevice.name })}
              />
            ) : (
              <>
                <Devices onOpen={setOpenDevice} />
                {instance ? <Capabilities instance={instance} /> : null}
              </>
            )}

            <section className={query ? "hidden" : undefined}>
              <div className="flex items-baseline justify-between">
                <h2 className="font-display text-lg text-ink">Activity</h2>
                <span className="flex items-center gap-1.5 text-xs text-ink-faint">
                  <span
                    aria-hidden
                    className={`inline-block size-1.5 rounded-full ${streamLive ? "bg-accent" : "bg-ink-faint"}`}
                  />
                  {streamLive ? "live" : "reconnecting"}
                </span>
              </div>

              {jobs.length === 0 ? (
                <Card className="mt-3 px-4 py-8 text-center text-sm text-ink-faint">
                  No background jobs yet.
                </Card>
              ) : (
                <ul className="mt-3 space-y-2">
                  {jobs.map((job) => (
                    <JobRow key={job.id} job={job} onChanged={reloadJobs} />
                  ))}
                </ul>
              )}
            </section>
          </>
        )}
      </main>
    </div>
  );
}

/** Capabilities makes it obvious why a feature is unavailable, rather than
 *  offering a button that would always fail. */
function Capabilities({ instance }: { instance: Instance }) {
  const rows: Array<{ label: string; on: boolean; note: string }> = [
    {
      label: "Read PDFs",
      on: instance.capabilities.convert && instance.externalTools["pdftotext"]?.available === true,
      note:
        instance.externalTools["pdftotext"]?.available === false
          ? `needs poppler — ${instance.externalTools["pdftotext"]?.install ?? ""}`
          : instance.providers["convert"]?.kind ?? "",
    },
    {
      label: "OCR scans",
      on: instance.capabilities.ocr && instance.externalTools["tesseract"]?.available === true,
      note:
        instance.externalTools["tesseract"]?.available === false
          ? `needs tesseract — ${instance.externalTools["tesseract"]?.install ?? ""}`
          : instance.providers["ocr"]?.kind ?? "",
    },
    {
      label: "Translate",
      on: instance.capabilities.translate,
      note: instance.capabilities.translate
        ? `${instance.providers["translate"]?.kind ?? ""} ${instance.providers["translate"]?.model ?? ""}`.trim()
        : "no provider configured",
    },
    {
      label: "Extract maintenance",
      on: instance.capabilities.extract,
      note: instance.capabilities.extract
        ? `${instance.providers["extract"]?.kind ?? ""} ${instance.providers["extract"]?.model ?? ""}`.trim()
        : "no provider configured",
    },
  ];

  return (
    <section>
      <h2 className="font-display text-lg text-ink">This instance</h2>
      <p className="mt-1 text-sm text-ink-soft">
        Reading languages: {instance.languages.join(", ")} &middot; version {instance.version}
      </p>
      <Card className="mt-3 divide-y divide-rule">
        {rows.map((row) => (
          <div key={row.label} className="flex items-center gap-3 px-4 py-3">
            <span
              aria-hidden
              className={`inline-block size-2 shrink-0 rounded-full ${row.on ? "bg-accent" : "bg-ink-faint/50"}`}
            />
            <span className="text-sm font-medium text-ink">{row.label}</span>
            <span className="ml-auto truncate text-xs text-ink-faint">{row.note}</span>
          </div>
        ))}
      </Card>
    </section>
  );
}

const stateStyles: Record<JobState, string> = {
  queued: "text-ink-faint",
  running: "text-accent",
  succeeded: "text-ink-soft",
  failed: "text-danger",
  cancelled: "text-ink-faint",
};

function JobRow({ job, onChanged }: { job: Job; onChanged: () => void }) {
  const [cancelling, setCancelling] = useState(false);
  const active = job.state === "queued" || job.state === "running";

  async function cancel() {
    setCancelling(true);
    try {
      await api.cancelJob(job.id);
    } catch {
      // Already finished; the refetch will show the real state.
    } finally {
      setCancelling(false);
      onChanged();
    }
  }

  return (
    <li>
      <Card className="px-4 py-3">
        <div className="flex items-center gap-3">
          <span className="font-mono text-sm text-ink">{job.kind}</span>
          <span className={`text-xs ${stateStyles[job.state]}`}>{job.state}</span>
          {job.attempts > 1 ? (
            <span className="text-xs text-ink-faint">attempt {job.attempts}</span>
          ) : null}
          <div className="ml-auto flex items-center gap-3">
            {job.costMicros > 0 ? (
              <span className="text-xs text-ink-faint" title="Provider cost for this job">
                {(job.costMicros / 1_000_000).toFixed(4)}
              </span>
            ) : null}
            {active ? (
              <Button variant="quiet" onClick={cancel} disabled={cancelling}>
                Cancel
              </Button>
            ) : null}
          </div>
        </div>

        {job.state === "running" ? (
          <div className="mt-2.5">
            <div className="h-1 overflow-hidden rounded-full bg-rule">
              <div
                className="h-full bg-accent transition-[width] duration-300"
                style={{ width: `${Math.round(job.progress * 100)}%` }}
              />
            </div>
            {job.progressNote ? (
              <p className="mt-1.5 text-xs text-ink-faint">{job.progressNote}</p>
            ) : null}
          </div>
        ) : null}

        {job.lastError ? <p className="mt-2 text-xs text-danger">{job.lastError}</p> : null}
      </Card>
    </li>
  );
}
