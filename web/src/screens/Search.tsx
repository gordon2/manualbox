import { useCallback, useEffect, useState } from "react";

import { api, ApiError } from "../api/client";
import type { SearchHit, SearchResults as Results } from "../api/types";
import { Alert, Button, Card } from "../ui";
import { dirOf } from "./reader-flow";

/**
 * The search box, and the hits it produced.
 *
 * # Why a submit rather than a keystroke
 *
 * docs/design/search.md measures a query at 0.2 ms through the index and 1.9 ms
 * through the scan, so searching on every keystroke would be affordable — but the
 * short-query fallback makes it dishonest. Typing `Sau` towards `Saugkraft` passes
 * through `S` and `Sa`, each of which the index cannot hold and each of which is
 * answered by a different path with a different notice. A box that changed its own
 * explanation twice per word would read as a bug. So the query is submitted, and the
 * notice describes one query the user actually asked.
 *
 * # What has to be visible
 *
 * `mode` is not decoration: `substring` means the trigram index could not represent
 * the query and a scan answered it instead, unranked and case-folding only ASCII.
 * `truncated` means these are the first hits and not the hits. `indexed` separates
 * "no manual says that" from "nothing has been converted yet". All three are stated
 * rather than left to be inferred from a short list.
 */
export function SearchBox({ query, onSearch }: { query: string; onSearch: (q: string) => void }) {
  const [draft, setDraft] = useState(query);

  return (
    <form
      role="search"
      onSubmit={(event) => {
        event.preventDefault();
        onSearch(draft.trim());
      }}
      className="flex items-end gap-2"
    >
      <label className="block flex-1">
        <span className="mb-1.5 block text-sm font-medium text-ink">Search your manuals</span>
        <input
          type="search"
          value={draft}
          onChange={(event) => {
            setDraft(event.target.value);
            // Emptying the box puts the library back, rather than leaving the hits of
            // a query that is no longer on screen.
            if (event.target.value.trim() === "") onSearch("");
          }}
          placeholder="Saugkraft, фильтр, descaling…"
          aria-label="Search every converted manual"
          className="w-full rounded-md border border-rule bg-paper-raised px-3 py-2 text-[15px] text-ink placeholder:text-ink-faint focus:border-accent focus:outline-none"
        />
      </label>
      <Button type="submit" disabled={draft.trim() === ""}>
        Search
      </Button>
    </form>
  );
}

/** The hits for one submitted query, or the reason there are none. */
export function SearchHits({
  query,
  onOpen,
  opening,
}: {
  query: string;
  /** Open the reader on the page this hit is printed on, in this hit's language. */
  onOpen: (hit: SearchHit) => void;
  /** The document being opened, so the hit that was clicked can say it is working. */
  opening: string | null;
}) {
  const [results, setResults] = useState<Results | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const run = useCallback(async () => {
    setLoading(true);
    try {
      setResults(await api.search(query));
      setError(null);
    } catch (cause) {
      setResults(null);
      setError(cause instanceof ApiError ? cause.message : "The search could not be run.");
    } finally {
      setLoading(false);
    }
  }, [query]);

  useEffect(() => {
    void run();
  }, [run]);

  if (loading && results === null && error === null) {
    return <p className="text-sm text-ink-faint">Searching…</p>;
  }
  if (error) return <Alert>{error}</Alert>;
  if (!results) return null;

  return <SearchResultsView results={results} onOpen={onOpen} opening={opening} />;
}

/**
 * One response, rendered — separated from the fetch for the same reason
 * [ReaderPages] is: it takes only data, so it can be handed a real response and
 * rendered without a browser or a server. The screenshots that checked this screen
 * were taken that way.
 */
export function SearchResultsView({
  results,
  onOpen,
  opening,
}: {
  results: Results;
  onOpen: (hit: SearchHit) => void;
  opening: string | null;
}) {
  if (results.hits.length === 0) {
    return <NothingFound results={results} />;
  }

  return (
    <div className="space-y-3">
      <Notices results={results} />
      <ul className="space-y-2">
        {results.hits.map((hit) => (
          <Hit
            key={`${hit.documentId}:${hit.lang ?? ""}:${hit.page}:${hit.regionX0}:${hit.index}`}
            hit={hit}
            onOpen={onOpen}
            busy={opening === hit.documentId}
          />
        ))}
      </ul>
    </div>
  );
}

/**
 * One hit: which manual, and where.
 *
 * The device's name leads, because that is what a household calls the thing — the
 * filename is often a model number or a download's digest, and it is kept underneath
 * for the case where one device has several manuals. Both come from the response;
 * search.md specifies that every hit joins `documents` and `devices` for exactly this
 * reason.
 *
 * `dir` is per hit and taken from the hit's own language, and every inline offset here
 * is logical, so a Hebrew snippet needs no rework when extraction stops storing
 * right-to-left text in visual order. That defect is upstream and this screen cannot
 * repair it either; the reader states it where a reader meets it.
 */
function Hit({
  hit,
  onOpen,
  busy,
}: {
  hit: SearchHit;
  onOpen: (hit: SearchHit) => void;
  busy: boolean;
}) {
  return (
    <li>
      <Card className="overflow-hidden">
        <button
          type="button"
          onClick={() => onOpen(hit)}
          disabled={busy}
          className="block w-full px-4 py-3 text-start hover:bg-rule/30 disabled:cursor-progress"
        >
          <div className="flex items-baseline gap-3">
            <span className="truncate text-sm font-medium text-ink">{hit.deviceName}</span>
            <span className="shrink-0 text-sm tabular-nums text-accent">page {hit.page}</span>
            {hit.name ? <span className="shrink-0 text-xs text-ink-faint">{hit.name}</span> : null}
            <span className="ms-auto shrink-0 text-xs text-ink-faint">{label(hit)}</span>
          </div>

          {/* The snippet is printed exactly as it arrived. The server already marks
              its own elision — a 484-character block comes back as
              "...ische Saugkraftregulierung ... auf MA..." — so adding an ellipsis of
              this screen's own put two in a row, which a screenshot showed and a
              typecheck could not. */}
          <p
            dir={dirOf(hit.lang)}
            className="mt-1.5 line-clamp-3 text-pretty text-start text-[15px] leading-relaxed text-ink"
          >
            {hit.snippet}
          </p>

          <p className="mt-1.5 truncate text-xs text-ink-faint">
            {hit.filename || "Untitled document"}
            {hit.state !== "ready" ? ` · ${hit.state.replace("_", " ")}` : ""}
          </p>
        </button>
      </Card>
    </li>
  );
}

/** What kind of thing matched, in the words the reader will see it in. */
function label(hit: SearchHit): string {
  switch (hit.kind) {
    case "list-item":
      return "list";
    case "table":
      return "table cell";
    default:
      return hit.kind;
  }
}

/**
 * How the query was answered, and whether the list is complete.
 *
 * Both notices are unconditional facts about this response rather than warnings, so
 * they are set as quiet prose. `substring` gets the warn colour because it changes
 * what the results mean: they are unranked, and case folding on that path is ASCII
 * only, so a two-letter Cyrillic query is case-sensitive.
 */
function Notices({ results }: { results: Results }) {
  return (
    <div className="space-y-1.5">
      <p className="text-sm text-ink-faint">
        {results.truncated ? "The first " : ""}
        {results.hits.length.toLocaleString()} {results.hits.length === 1 ? "result" : "results"}
        {results.mode === "index" ? ", best match first" : ""}
      </p>

      {results.mode === "substring" ? (
        <p className="text-pretty text-sm text-warn">
          Part of &ldquo;{results.query}&rdquo; is shorter than three characters, which the index
          cannot hold, so every stored block was scanned instead. These hits are in no particular
          order, and outside the Latin alphabet the match is case-sensitive.
        </p>
      ) : null}

      {results.truncated ? (
        <p className="text-pretty text-sm text-ink-faint">
          Cut off at {results.limit.toLocaleString()}: these are the first hits, not all of them.
          Add a word to narrow the search.
        </p>
      ) : null}
    </div>
  );
}

/**
 * Nothing matched — which is two different situations.
 *
 * `indexed` is in the response only when the hits are empty, and it is the number of
 * blocks there were to search. Zero means nothing has been converted yet, which is
 * not a search failure and has a different next step.
 */
function NothingFound({ results }: { results: Results }) {
  if (results.indexed === 0) {
    return (
      <Card className="px-4 py-8 text-center text-sm text-ink-faint">
        No manual has been converted yet, so there is nothing to search. Upload one to a device and
        approve what to import.
      </Card>
    );
  }
  return (
    <Card className="px-4 py-8 text-center text-sm text-ink-faint">
      No manual contains &ldquo;{results.query}&rdquo;
      {results.indexed === undefined
        ? "."
        : `, across the ${results.indexed.toLocaleString()} passages that were searched.`}{" "}
      Try a shorter word: a match is on any part of a word, so <em>filter</em> also finds{" "}
      <em>Luftfilter</em>.
    </Card>
  );
}
