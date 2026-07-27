import { useCallback, useEffect, useState } from "react";

import { api, ApiError, subscribeToJobs } from "../api/client";
import type { Block, Conversion, Doc, Figure } from "../api/types";
import { Alert, Card } from "../ui";
import { dirOf, readingOrder, type Flow, type ReaderPage } from "./reader-flow";

/** One of the languages this document was converted for. */
export interface ReaderLanguage {
  lang: string;
  name: string;
}

/**
 * Reading a converted manual.
 *
 * # Direction
 *
 * This is the one screen in the app that handles right to left, and
 * docs/design/conversion.md records why it is the exception: for a new screen the
 * cost is nil, every block already carries its own language, and rewriting it later
 * would not be free. So every inline offset here is logical — `ms`, `me`, `ps`,
 * `pe`, `text-start` — and `dir` comes from the block rather than from the app.
 *
 * There is a defect underneath that this screen cannot fix, and it is stated where a
 * reader will meet it rather than left to be discovered: the stored text of a
 * right-to-left language is in *visual* order, so it renders mirrored. See
 * [DirectionWarning].
 */
export function Reader({
  doc,
  deviceName,
  languages,
  onBack,
}: {
  doc: Doc;
  deviceName: string;
  /** Empty asks for everything stored, which is already only what was charged for. */
  languages: ReaderLanguage[];
  onBack: () => void;
}) {
  const first = languages[0];
  const [lang, setLang] = useState<string | undefined>(first ? first.lang : undefined);
  const [conversion, setConversion] = useState<Conversion | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setConversion(await api.documentConversion(doc.id, lang));
      setError(null);
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : "Could not load the converted manual.");
    }
  }, [doc.id, lang]);

  useEffect(() => {
    void load();
  }, [load]);

  // A conversion in flight finishes in the background, so the reader follows the
  // same job stream the rest of the app does instead of polling. Refetching on any
  // event is what Home does, and for the same reason: the server's answer is the
  // truth, an event only says to go and ask again.
  const settled = conversion !== null && conversion.state !== "converting";
  useEffect(() => {
    if (settled) return;
    return subscribeToJobs(() => void load());
  }, [settled, load]);

  const pages = conversion ? readingOrder(conversion.blocks, conversion.figures) : [];
  const shown = languages.find((l) => l.lang === lang);

  return (
    <div className="space-y-6">
      <div>
        <button className="text-sm text-accent" onClick={onBack}>
          ← {deviceName}
        </button>
        <h1 className="mt-2 text-balance font-display text-2xl text-ink">
          {doc.filename || "Untitled document"}
        </h1>
        <p className="mt-1 text-sm text-ink-soft">{summary(doc, conversion, pages, shown)}</p>
      </div>

      {languages.length > 1 ? (
        <div className="flex flex-wrap items-center gap-1">
          {languages.map((option) => {
            const active = option.lang === lang;
            return (
              <button
                key={option.lang}
                onClick={() => setLang(option.lang)}
                aria-current={active ? "true" : undefined}
                className={`rounded-md px-2.5 py-1 text-xs ${
                  active ? "bg-accent-soft text-accent" : "text-ink-soft hover:bg-rule/50"
                }`}
              >
                {option.name}
              </button>
            );
          })}
        </div>
      ) : null}

      {error ? <Alert>{error}</Alert> : null}

      {conversion === null ? (
        error ? null : (
          <Card className="px-4 py-8 text-center text-sm text-ink-faint">Loading…</Card>
        )
      ) : conversion.state === "ready" ? (
        pages.length === 0 ? (
          <Card className="px-4 py-8 text-center text-sm text-ink-faint">
            Nothing was converted for {shown ? shown.name : "this language"}. The original is kept
            whole, so another language can be imported without re-uploading.
          </Card>
        ) : (
          <>
            {languages.some((l) => l.lang === lang && isMirrored(l.lang)) ? (
              <DirectionWarning name={shown?.name ?? lang ?? ""} />
            ) : null}
            <ReaderPages pages={pages} documentId={doc.id} />
          </>
        )
      ) : (
        <Progress conversion={conversion} />
      )}
    </div>
  );
}

/** What is being read, in one line under the title. */
function summary(
  doc: Doc,
  conversion: Conversion | null,
  pages: ReaderPage[],
  shown: ReaderLanguage | undefined,
): string {
  const parts: string[] = [];
  if (shown) parts.push(shown.name);
  if (conversion && conversion.state === "ready" && pages.length > 0) {
    const first = pages[0] as ReaderPage;
    const last = pages[pages.length - 1] as ReaderPage;
    const span =
      first.page === last.page ? `page ${first.page}` : `pages ${first.page}–${last.page}`;
    parts.push(
      `${pages.length.toLocaleString()} of ${(doc.pageCount ?? 0).toLocaleString()} pages, ${span} of the original`,
    );
  }
  return parts.join(" · ");
}

/**
 * The one thing this screen cannot render correctly, said out loud.
 *
 * `pdftohtml -xml` returns a right-to-left line in visual order — the Hebrew heading
 * of the sequential manual comes back as the exact reverse of its logical string,
 * confirmed codepoint by codepoint — while `pdftotext` on the same page returns it
 * logically, wrapped in bidi controls. So the letters of every stored Hebrew and
 * Arabic block are in the wrong order before this screen ever sees them, and no
 * `dir` can undo that: the bidi algorithm reorders a strong run whichever base
 * direction it is given.
 *
 * Reversing the string here was rejected. It would silently mangle the Latin words
 * and digits real manuals mix in, and it would double-reverse the day the pipeline is
 * fixed. The fix belongs where the text is read.
 */
function DirectionWarning({ name }: { name: string }) {
  return (
    <div className="rounded-md border border-warn/30 px-3.5 py-3 text-sm text-warn">
      {name} is written right to left, and the text below reads backwards. The tool that reads a
      PDF&rsquo;s layout returns a right-to-left line in the order it is printed rather than the
      order it is read, and that is stored as it arrived. The letters, not this page, are what is
      out of order — fixing it belongs where the document is read.
    </div>
  );
}

/** Whether this language's stored text is known to arrive mirrored. */
function isMirrored(lang: string): boolean {
  return dirOf(lang) === "rtl";
}

/** What is happening to a document that has no reader yet. */
function Progress({ conversion }: { conversion: Conversion }) {
  if (conversion.state === "failed") {
    return (
      <Alert>{conversion.lastError || "The conversion failed and no reason was recorded."}</Alert>
    );
  }
  const messages: Record<string, string> = {
    uploaded: "This document is queued to be read. Nothing has been converted yet.",
    probing: "Reading the document to find out what is in it. This costs nothing.",
    awaiting_scope:
      "Nothing has been converted yet. The gate on the device page is waiting for you to say what to import.",
    declined: "This document was not processed, so there is nothing to read.",
    converting: "Converting the languages you asked for. This page will fill in when it finishes.",
  };
  return (
    <Card className="px-4 py-8 text-center text-sm text-ink-faint">
      {messages[conversion.state] ?? "There is nothing to read yet."}
    </Card>
  );
}

/**
 * The converted document itself, separated from the screen around it.
 *
 * Separate because this is the part worth rendering without a browser: there is no
 * browser automation on this machine, so the closest thing to looking at the page is
 * handing this a real document's blocks and reading the HTML that comes out. It takes
 * only data and needs no fetch, which is what makes that possible.
 */
export function ReaderPages({ pages, documentId }: { pages: ReaderPage[]; documentId: string }) {
  return (
    <article className="space-y-8">
      {pages.map((page) => (
        <PageView key={page.page} page={page} documentId={documentId} />
      ))}
    </article>
  );
}

/** One page of the original: a marker, then everything printed on it. */
function PageView({ page, documentId }: { page: ReaderPage; documentId: string }) {
  return (
    <section>
      <div className="flex items-center gap-3">
        <span className="shrink-0 text-xs tabular-nums text-ink-faint">page {page.page}</span>
        <span aria-hidden className="h-px flex-1 bg-rule" />
      </div>
      <div className="mt-4 space-y-4">
        {page.flows.map((flow, i) => (
          <FlowView key={i} flow={flow} documentId={documentId} />
        ))}
      </div>
    </section>
  );
}

function FlowView({ flow, documentId }: { flow: Flow; documentId: string }) {
  switch (flow.kind) {
    case "heading":
      return <Heading block={flow.block} level={flow.level} />;

    case "paragraph":
      return (
        <p
          dir={dirOf(flow.block.lang)}
          className="max-w-prose text-pretty text-start text-[15px] leading-relaxed text-ink"
        >
          {flow.block.text}
        </p>
      );

    case "list":
      return (
        <ul className="max-w-prose space-y-1.5">
          {flow.items.map((item, i) => (
            <li
              key={i}
              dir={dirOf(item.block.lang)}
              className="flex gap-2 text-start text-[15px] leading-relaxed text-ink"
            >
              {/* The document's own marker, kept rather than replaced: its numbers
                  restart and skip, and a CSS counter would renumber them silently.
                  An item whose marker could not be separated shows no marker at all
                  instead of an invented bullet — see splitMarker. */}
              {item.marker ? (
                <span className="shrink-0 tabular-nums text-ink-faint">{item.marker}</span>
              ) : null}
              <span className="text-pretty">{item.text}</span>
            </li>
          ))}
        </ul>
      );

    case "table":
      return <TableView flow={flow} />;

    case "figure":
      return <FigureView figure={flow.figure} documentId={documentId} />;
  }
}

/**
 * A heading, at one of the two levels there are.
 *
 * conversion.md: the level is 1 or 2 and never more. Level 1 takes the display serif
 * the rest of the app uses for headings; level 2 stays in the body face and is
 * separated by weight, because a manual's subheading is usually a whole instruction
 * ("Öffnen Sie den Gehäusedeckel.") and setting a sentence in a serif display size
 * reads as prose that happens to be large.
 */
function Heading({ block, level }: { block: Block; level: number }) {
  const dir = dirOf(block.lang);
  if (level === 1) {
    return (
      <h2
        dir={dir}
        className="max-w-prose text-balance pt-2 text-start font-display text-xl text-ink"
      >
        {block.text}
      </h2>
    );
  }
  return (
    <h3
      dir={dir}
      className="max-w-prose text-balance text-start text-[15px] font-semibold text-ink"
    >
      {block.text}
    </h3>
  );
}

/**
 * A table, as the ruled grid it is printed as.
 *
 * A row whose single cell spans every column is the section label the page prints
 * across the top of a group of rows — "Allgemein (alle Funktionen)" on page 57 — so it
 * is marked up as a header for that group rather than as another data cell.
 */
function TableView({ flow }: { flow: Extract<Flow, { kind: "table" }> }) {
  return (
    <div className="overflow-x-auto">
      <table dir={dirOf(flow.lang)} className="w-full border-collapse text-sm">
        <tbody>
          {flow.rows.map((cells, r) => (
            <tr key={r}>
              {cells.map((cell, c) =>
                cell.colSpan === flow.columns ? (
                  <th
                    key={c}
                    scope="colgroup"
                    colSpan={cell.colSpan}
                    className="border border-rule px-3 py-2 text-start align-top font-semibold text-ink"
                  >
                    {cell.block ? cell.block.text : null}
                  </th>
                ) : (
                  <td
                    key={c}
                    colSpan={cell.colSpan}
                    className="border border-rule px-3 py-2 text-start align-top text-ink"
                  >
                    {cell.block ? cell.block.text : null}
                  </td>
                ),
              )}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/**
 * One illustration.
 *
 * The stored size is given as `width` and `height` so the page does not reflow as
 * pictures arrive, and the image is capped at the measure rather than shown at its
 * rendered 216 dpi size. `loading="lazy"` is what keeps a heavily illustrated section
 * cheap: the sequential manual's Russian is 81 figures, and none of them is fetched
 * until it is near the viewport.
 */
function FigureView({ figure, documentId }: { figure: Figure; documentId: string }) {
  return (
    <figure>
      <img
        src={api.documentFigureURL(documentId, figure.sha256)}
        width={figure.pixelWidth}
        height={figure.pixelHeight}
        loading="lazy"
        decoding="async"
        alt={`Illustration printed on page ${figure.page} of the original`}
        className="h-auto max-w-full rounded border border-rule"
      />
    </figure>
  );
}
