import { useCallback, useEffect, useState } from "react";

import { api, ApiError, subscribeToJobs } from "../api/client";
import type { Block, Conversion, Doc, Figure, Gate } from "../api/types";
import { Alert, Card } from "../ui";
import {
  contentsTarget,
  dirOf,
  readingOrder,
  type Flow,
  type ReaderPage,
  type Slot,
} from "./reader-flow";

/** One of the languages this document was converted for. */
export interface ReaderLanguage {
  lang: string;
  name: string;
}

/**
 * The languages a reader may ask for, biggest first.
 *
 * The gate's in-scope list is exactly what approving converted — approve takes no
 * language argument for that reason — so it is the right list wherever the reader is
 * opened from. Biggest first so the reader opens on the language most of the document
 * is in rather than on whichever sorts first.
 */
export function readerLanguages(gate: Gate): ReaderLanguage[] {
  return [...gate.inScope]
    .sort((a, b) => b.chars - a.chars || a.name.localeCompare(b.name))
    .map((run) => ({ lang: run.lang, name: run.name }));
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
 * The defect this screen used to have to apologise for is fixed underneath it: the
 * stored text of a right-to-left language was in *visual* order and rendered
 * mirrored, and internal/doc now puts it back into the order it is written in. What
 * is left is Arabic letter shaping, which no part of this pipeline can do anything
 * about, and it is stated where a reader will meet it — see [ShapingWarning].
 */
export function Reader({
  doc,
  backTo,
  languages,
  startLang,
  startPage,
  onBack,
}: {
  doc: Doc;
  /** What going back returns to, named: a device, or the results that led here. */
  backTo: string;
  /** Empty asks for everything stored, which is already only what was charged for. */
  languages: ReaderLanguage[];
  /**
   * Which language to open in, when something already knows. A search hit does: the
   * matching text is in one language, and opening on the biggest one instead would
   * show a page that does not contain what was searched for.
   */
  startLang?: string | undefined;
  /** Which page to open on. A page of the original, not an index into the pages shown. */
  startPage?: number | undefined;
  onBack: () => void;
}) {
  const first = languages[0];
  const [lang, setLang] = useState<string | undefined>(
    startLang ?? (first ? first.lang : undefined),
  );
  const [conversion, setConversion] = useState<Conversion | null>(null);
  const [error, setError] = useState<string | null>(null);
  // Which page the reader is currently opened at. Seeded from startPage and then
  // owned here, because following a contents entry is the same act as following a
  // search hit and has to move the same marker; the prop only says where to begin.
  //
  // The effect keeps the two in step if the prop ever changes under a mounted
  // reader. Today it cannot -- Home unmounts this screen on the way back to the
  // results -- so without it nothing would break yet; it is here because a state
  // seeded from a prop and never resynchronised is a bug waiting for the first
  // caller that keeps the screen mounted, and that caller would see the marker
  // silently ignore where it was told to go.
  const [openedPage, setOpenedPage] = useState<number | undefined>(startPage);
  useEffect(() => {
    setOpenedPage(startPage);
  }, [startPage]);

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

  // What a contents entry needs to become a link. `pages` is what THIS language's
  // conversion actually holds, which is the check that matters: the columns manual
  // prints five languages' contents, so a German entry can name a page that is
  // entirely Russian.
  const jump: ContentsJump = {
    folioOffset: conversion?.folioOffset,
    pages: new Set(pages.map((page) => page.page)),
    onJump: setOpenedPage,
  };

  return (
    <div className="space-y-6">
      <div>
        <button className="text-sm text-accent" onClick={onBack}>
          ← {backTo}
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
            {languages.some((l) => l.lang === lang && isUnshaped(l.lang)) ? (
              <ShapingWarning name={shown?.name ?? lang ?? ""} />
            ) : null}
            {openedPage !== undefined && !pages.some((page) => page.page === openedPage) ? (
              // Following a hit lands on a page in the hit's own language. Switching
              // language afterwards can leave that page behind entirely, and a reader
              // who scrolled nowhere deserves to know why rather than assume a bug.
              <p className="text-sm text-ink-faint">
                Page {openedPage} has nothing in {shown ? shown.name : "this language"}.
              </p>
            ) : null}
            <ReaderPages pages={pages} documentId={doc.id} startPage={openedPage} jump={jump} />
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
/**
 * What is still wrong with an Arabic-script language, now that the order is right.
 *
 * This used to say the text below reads backwards, which was true and is not any
 * more: internal/doc puts a right-to-left line back into the order it is written in,
 * and the verifier that measured the defect went from 8,120 reversed words to none.
 * Saying so anyway would be worse than saying nothing.
 *
 * What is left is real but narrower and belongs to Arabic alone. The letters arrive
 * in their isolated forms rather than joined — `السالمة` where the page prints
 * `السلامة` — because the document's font maps its glyphs that way, and pdftotext,
 * which is checked against for everything else here, reads them identically. It is
 * not an ordering fault and nothing in the pipeline can join them.
 *
 * Hebrew gets no warning now, because there is nothing left to warn a reader about.
 */
function ShapingWarning({ name }: { name: string }) {
  return (
    <div className="rounded-md border border-warn/30 px-3.5 py-3 text-sm text-warn">
      {name} is written right to left, and it reads in the right order here. Its letters, though,
      arrive one by one instead of joined up, because that is how this document&rsquo;s font maps
      them — a second, independent reader of the same file sees exactly the same thing. The words
      are right; the letter shapes are not.
    </div>
  );
}

/**
 * Whether this language's letters are known to arrive unshaped.
 *
 * The Arabic script, not every right-to-left one: Hebrew does not join its letters,
 * so it has nothing to lose this way.
 */
function isUnshaped(lang: string): boolean {
  const base = (lang || "").split("-")[0]?.toLowerCase() ?? "";
  return ["ar", "fa", "ur", "ps", "sd", "ug", "ku"].includes(base);
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
 * Separate because this is the part worth rendering without a fetch: hand it a real
 * document's blocks and read the HTML that comes out, or point a headless browser at
 * that HTML and look at the page. It takes only data, which is what makes both
 * possible — the screen around it is behind a session.
 *
 * (An earlier version of this comment said there is no browser automation on this
 * machine. There is: Chrome is installed, screenshots headlessly with `--screenshot`,
 * and can be driven over the DevTools protocol with no dependency at all.)
 */
export function ReaderPages({
  pages,
  documentId,
  startPage,
  jump,
}: {
  pages: ReaderPage[];
  documentId: string;
  /** The page to open on, marked and scrolled to. */
  startPage?: number | undefined;
  /** Absent: contents entries print their page number as plain text. */
  jump?: ContentsJump | undefined;
}) {
  return (
    <article className="space-y-8">
      {pages.map((page) => (
        <PageView
          key={page.page}
          page={page}
          documentId={documentId}
          opened={page.page === startPage}
          jump={jump}
        />
      ))}
    </article>
  );
}

/**
 * What a printed contents entry needs before its page number can be a link.
 *
 * Carried as one object through three levels rather than three props, and passed
 * rather than put in a context, so that rendering [ReaderPages] on its own -- which
 * is how this screen is looked at, see the note above it -- can turn linking on and
 * off explicitly instead of inheriting whatever a provider happened to hold.
 */
export interface ContentsJump {
  /** The document's one offset. Absent where the folios agreed on none. */
  folioOffset?: number | undefined;
  /** The pages this language's conversion holds, which is what a target must be in. */
  pages: ReadonlySet<number>;
  onJump: (page: number) => void;
}

/**
 * One page of the original: a marker, then everything printed on it.
 *
 * `opened` is the page a search hit sent the reader to. It scrolls itself into view
 * through a callback ref rather than an effect looking the element up by id, so the
 * scroll happens exactly when that page's element exists — the conversion arrives
 * asynchronously, and an effect keyed on anything else would run before it. Figures
 * carry their stored width and height, so nothing below reflows afterwards and the
 * page does not drift back out of view.
 */
function PageView({
  page,
  documentId,
  opened,
  jump,
}: {
  page: ReaderPage;
  documentId: string;
  opened: boolean;
  jump?: ContentsJump | undefined;
}) {
  const scrollHere = useCallback((node: HTMLElement | null) => {
    node?.scrollIntoView({ block: "start" });
  }, []);

  return (
    <section
      ref={opened ? scrollHere : undefined}
      aria-current={opened ? "location" : undefined}
      className="scroll-mt-6"
    >
      <div className="flex items-center gap-3">
        <span
          className={`shrink-0 text-xs tabular-nums ${opened ? "text-accent" : "text-ink-faint"}`}
        >
          page {page.page}
          {opened ? " · opened here" : ""}
        </span>
        <span aria-hidden className="h-px flex-1 bg-rule" />
      </div>
      <div className="mt-4 space-y-4">
        <SlotsView slots={page.slots} documentId={documentId} jump={jump} />
      </div>
    </section>
  );
}

/**
 * A page's content, stacked or set beside itself as the paper set it.
 *
 * # What "beside" costs, and what happens when it cannot be paid
 *
 * Two printed columns need roughly twice the measure of one, and below about 40
 * characters a column of prose stops being readable. So a printed column stacks below
 * `md` — in reading order, which is the DOM order [placeOnPage] already put it in, so
 * the fallback is the same content read down the page and never a scramble. That is
 * the deliberate answer to a narrow viewport: the picture goes back to sitting above
 * or below its text, which is where the sequential manual prints it anyway.
 *
 * A strip of drawings has no breakpoint, because a drawing has no measure to lose: it
 * keeps its row for as long as the row fits and wraps when it does not. Measured on
 * page 533 in Chrome — at 1265 px the three strips are each one row, at 390 px each has
 * wrapped to one drawing per line, and the page's own scrollWidth equals the viewport at
 * both, so nothing is ever cut off sideways.
 *
 * # Direction
 *
 * `dir` is on the flex row rather than on each child, and the children are already in
 * logical order, so a right-to-left document reads its first column on the right with
 * no second rule and no physical property anywhere. Same reason every offset on this
 * screen is `ms`/`me` rather than `ml`/`mr`.
 *
 * It comes off the slot rather than off the content under it. Reading it back from the
 * first block found was the first version and it is wrong for the case that has no
 * blocks at all: a strip of drawings would have defaulted to left-to-right and undone
 * the logical ordering it was given.
 */
function SlotsView({
  slots,
  documentId,
  jump,
}: {
  slots: Slot[];
  documentId: string;
  jump?: ContentsJump | undefined;
}) {
  return (
    <>
      {slots.map((slot, i) =>
        slot.kind === "flows" ? (
          slot.flows.map((flow, j) => (
            <FlowView key={`${i}-${j}`} flow={flow} documentId={documentId} jump={jump} />
          ))
        ) : (
          <div
            key={i}
            dir={slot.rtl ? "rtl" : "ltr"}
            className={
              slot.strip
                ? "flex flex-wrap items-start gap-3"
                : "flex flex-col gap-4 md:flex-row md:items-start md:gap-6"
            }
          >
            {slot.columns.map((column, j) => (
              <div
                key={j}
                // A printed column takes the paper's own proportion of the measure, so
                // a rail of drawings beside a wider text column stays the narrower of
                // the two; `basis-0` lets the ratio decide rather than the content.
                //
                // A strip does not, and that was worth a second look at page 530: three
                // drawings of one step, grown to their printed shares, sat with gaps
                // between them because a small drawing does not fill the share of the
                // page its whitespace is part of. Sized to themselves they sit together,
                // and their sizes are already in the paper's proportion to each other —
                // both come from the same crop.
                style={slot.strip ? undefined : { flexGrow: column.width, flexBasis: 0 }}
                className="min-w-0 space-y-4"
              >
                <SlotsView slots={column.slots} documentId={documentId} jump={jump} />
              </div>
            ))}
          </div>
        ),
      )}
    </>
  );
}

function FlowView({
  flow,
  documentId,
  jump,
}: {
  flow: Flow;
  documentId: string;
  jump?: ContentsJump | undefined;
}) {
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

    case "contents":
      return <ContentsView flow={flow} jump={jump} />;

    case "table":
      return <TableView flow={flow} />;

    case "figure":
      return <FigureView figure={flow.figure} documentId={documentId} />;
  }
}

/**
 * A printed table of contents, as the list of entries it is.
 *
 * It arrived as one run-together paragraph of dot leaders until internal/doc learned
 * to give each printed line its own block — 17 entries on the columns manual's
 * contents page, glued into one because consecutive entries sit at exactly the line
 * pitch and the paragraph rule has nothing else to separate them by.
 *
 * The leader is drawn with a rule rather than with the document's own periods: a row
 * of literal dots is noise to a screen reader, and the dots are still in the block's
 * text where search and the coverage check can see them.
 *
 * The number the paper prints is what is shown, always, and it is what is read out:
 * a reader who is holding the manual is looking for that number, and replacing it
 * with the PDF's own would help nobody. Where the mapping exists the same number
 * becomes a button that opens the PDF page it means -- see [contentsTarget] for the
 * three cases that stay plain text, of which "this language's conversion does not
 * hold that page" is the one that actually fires on a real document.
 *
 * A button rather than an anchor: there is no router and no URL for a page, so an
 * `href` would either be a lie or a hash this app does not read back. What happens
 * is a state change, which is what a button means.
 */
function ContentsView({
  flow,
  jump,
}: {
  flow: Extract<Flow, { kind: "contents" }>;
  jump?: ContentsJump | undefined;
}) {
  return (
    <ul className="max-w-prose space-y-1">
      {flow.entries.map((entry, i) => {
        const target = jump ? contentsTarget(entry.page, jump.folioOffset, jump.pages) : null;
        return (
          <li
            key={i}
            dir={dirOf(entry.block.lang)}
            className="flex items-baseline gap-2 text-[15px] leading-relaxed text-ink"
          >
            <span className="text-pretty text-start">{entry.title}</span>
            <span aria-hidden className="mb-1 h-px flex-1 bg-rule" />
            {entry.page ? (
              target !== null && jump ? (
                <button
                  type="button"
                  onClick={() => jump.onJump(target)}
                  // The printed number is the label; the PDF page it opens is said
                  // separately, because the two differ and a reader deserves to
                  // know which one they are about to be taken to.
                  aria-label={`Page ${entry.page}, page ${target} of the original`}
                  className="shrink-0 rounded-sm tabular-nums text-accent underline decoration-accent/40 underline-offset-2 hover:decoration-accent"
                >
                  {entry.page}
                </button>
              ) : (
                <span className="shrink-0 tabular-nums text-ink-soft">{entry.page}</span>
              )
            ) : null}
          </li>
        );
      })}
    </ul>
  );
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
