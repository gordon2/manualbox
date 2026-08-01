// Turning what the conversion stored into what a person reads.
//
// The API hands back two flat lists — blocks in reading order and figures — and
// neither is shaped like a page. This module does the shaping, separately from the
// JSX so it can be exercised on a real document's JSON without a browser.
//
// Three things are being reconstructed here, and each is reconstructed because the
// API deliberately does not carry it:
//
//   - Where a picture belongs. docs/design/conversion.md is explicit that a figure
//     is not a block and never will be, because a language-neutral figure has no
//     region to key on. So a reader merges the two lists by page and by vertical
//     position, which is what mergePage does.
//   - Which cells make a table. Every table cell is its own block and its grid
//     position travels in the prose `note` — blocks.go says so in as many words:
//     "The grid position travels in the note rather than in a field". Reading it
//     back is therefore intended, and it is the only source: `Block` has no row or
//     column field. Measured against the alternative on five real conversions,
//     grouping the same cells geometrically instead agrees with the note on only
//     23 of 38 cells of the columns manual and 0 of 112 of the Hebrew one, because
//     a cell's stored box is its *text's* extent and not the ruled cell's.
//   - Which list items make a list. Adjacency plus the marker the pipeline records.

import type { Block, Figure } from "../api/types";

/** A cell of a reconstructed table. `null` is a cell the pipeline did not recover. */
export interface TableCell {
  block: Block | null;
  colSpan: number;
}

/** One flow of content, in reading order. A list and a table span several blocks. */
export type Flow =
  | { kind: "heading"; block: Block; level: number }
  | { kind: "paragraph"; block: Block }
  | { kind: "list"; items: Array<{ block: Block; marker: string; text: string }> }
  | { kind: "contents"; entries: Array<{ block: Block; title: string; page: string }> }
  | { kind: "table"; rows: TableCell[][]; columns: number; lang: string }
  | { kind: "figure"; figure: Figure };

/** One page of the original, and everything printed on it. */
export interface ReaderPage {
  page: number;
  flows: Flow[];
}

/**
 * The `note` a table cell carries. Coupled to one `fmt.Sprintf` in
 * internal/doc/blocks.go, which is the coupling the file comment explains; a cell
 * whose note does not match is rendered as prose rather than silently dropped.
 */
const CELL_NOTE =
  /^row (\d+) of (\d+), column (\d+) of (\d+) of a ruled table(?:, spanning (\d+) of them)?$/;

/** The marker a list item opens with, as the pipeline recorded it. */
const MARKER_NOTE = /^opens with the list marker "(.+)"$/;

/**
 * Scripts written right to left, by primary subtag.
 *
 * `iw` and `in` are the retired codes for Hebrew and Indonesian; only the first is
 * relevant here, but a document tagged with it must still read correctly.
 */
const RTL_LANGS = new Set(["ar", "he", "iw", "fa", "ur", "yi", "ji", "ps", "sd", "ug", "dv", "ku"]);

/** True when this language is written right to left. */
export function isRTL(lang: string | undefined): boolean {
  if (!lang) return false;
  const primary = lang.toLowerCase().split(/[-_]/)[0] ?? "";
  return RTL_LANGS.has(primary);
}

/** "rtl" or "ltr", for a `dir` attribute. */
export function dirOf(lang: string | undefined): "rtl" | "ltr" {
  return isRTL(lang) ? "rtl" : "ltr";
}

/**
 * Everything the conversion returned, as pages of flows.
 *
 * Blocks are already in reading order within a page and pages already ascend, so
 * nothing is re-sorted except the figures being spliced in.
 */
export function readingOrder(blocks: Block[], figures: Figure[]): ReaderPage[] {
  const pages: number[] = [];
  const blocksByPage = new Map<number, Block[]>();
  const figuresByPage = new Map<number, Figure[]>();

  for (const block of blocks) {
    const list = blocksByPage.get(block.page);
    if (list) list.push(block);
    else {
      blocksByPage.set(block.page, [block]);
      pages.push(block.page);
    }
  }
  for (const figure of figures) {
    const list = figuresByPage.get(figure.page);
    if (list) list.push(figure);
    else {
      figuresByPage.set(figure.page, [figure]);
      // A page can hold pictures and no text of this language: the columns manual
      // sets page 11 as a single full-page diagram.
      if (!blocksByPage.has(figure.page)) pages.push(figure.page);
    }
  }

  pages.sort((a, b) => a - b);
  return pages.map((page) => ({
    page,
    flows: group(mergePage(blocksByPage.get(page) ?? [], figuresByPage.get(page) ?? [])),
  }));
}

type Item = { block: Block } | { figure: Figure };

/**
 * One page's blocks and figures in the order they are printed down the page.
 *
 * Only the figures are placed, and they are placed by their top edge against each
 * block's. Both lists are measured in the same space — a figure's box and a block's
 * box both come back in the 892-unit space of the 108 dpi render, verified against
 * the stored pixel sizes at 2.00 pixels per unit — so the comparison is direct and
 * needs no scaling.
 *
 * A tie keeps the block first, so a picture and the paragraph introducing it stay in
 * that order.
 */
export function mergePage(blocks: Block[], figures: Figure[]): Item[] {
  const sorted = [...figures].sort((a, b) => a.y0 - b.y0 || a.index - b.index);
  const out: Item[] = [];
  let f = 0;
  for (const block of blocks) {
    while (f < sorted.length && (sorted[f] as Figure).y0 < block.y0) {
      out.push({ figure: sorted[f] as Figure });
      f++;
    }
    out.push({ block });
  }
  for (; f < sorted.length; f++) out.push({ figure: sorted[f] as Figure });
  return out;
}

/**
 * Runs of list items become one list, runs of table cells become tables.
 *
 * A run is not broken by a figure that happens to sit inside its vertical span, and
 * that is measured rather than tidy-minded. Page 52 of the columns manual prints a
 * nine-row table down the left of the measure and a photograph to its right, whose
 * top edge falls between two of the rows: placing the picture strictly by height
 * split one printed table into two, one of them a single stray cell reading
 * "Fugendüse". The same happens to a two-line heading — page 14's "Trockensaugen mit
 * der DryBOX / (Zyklon-Filtertechnologie)" arrives as two level-1 blocks 24 units
 * apart, with a photograph's top edge one unit inside that gap. So a figure met while
 * a run is being consumed is held and emitted directly after it, which moves a
 * picture by at most the height of the thing it landed in.
 */
function group(items: Item[]): Flow[] {
  const flows: Flow[] = [];
  // Figures met while a run is being consumed, emitted when the run ends.
  let held: Figure[] = [];
  const release = () => {
    for (const figure of held) flows.push({ kind: "figure", figure });
    held = [];
  };

  let i = 0;
  while (i < items.length) {
    const item = items[i] as Item;
    if ("figure" in item) {
      flows.push({ kind: "figure", figure: item.figure });
      i++;
      continue;
    }
    const block = item.block;

    /** Consumes the rest of a run of blocks this predicate accepts, holding figures. */
    const run = (accepts: (b: Block) => boolean): Block[] => {
      const out: Block[] = [];
      const holding: Figure[] = [];
      let j = i;
      while (j < items.length) {
        const next = items[j] as Item;
        if ("figure" in next) {
          holding.push(next.figure);
          j++;
          continue;
        }
        if (!accepts(next.block)) break;
        // Only now are the figures passed over known to be inside the run rather
        // than after its last block.
        for (const figure of holding.splice(0)) held.push(figure);
        out.push(next.block);
        j++;
        i = j;
      }
      return out;
    };

    // A contents entry is a list item whose note says it carries a dot leader, and
    // internal/doc explains why it is not a kind of its own: BlockKind reaches a
    // database column whose CHECK lists five kinds, and a sixth costs a rebuild of
    // the table the search index is external-content over.
    if (isContentsEntry(block)) {
      const cells = run(isContentsEntry);
      flows.push({ kind: "contents", entries: cells.map(splitEntry) });
      release();
      continue;
    }

    if (block.kind === "list-item") {
      // Contents entries are list items too and are taken above this, deliberately:
      // asking about the kind first swallows them into an ordinary list, which is
      // what happened the first time this was wired and what the leader note is for.
      const cells = run((b) => b.kind === "list-item" && !isContentsEntry(b));
      flows.push({ kind: "list", items: cells.map(splitMarker) });
      release();
      continue;
    }

    if (block.kind === "table") {
      const cells = run((b) => b.kind === "table");
      for (const flow of tables(cells)) flows.push(flow);
      release();
      continue;
    }

    if (block.kind === "heading") {
      // conversion.md: a heading is level 1 or 2 and never more, so this clamps
      // rather than building a hierarchy that cannot arrive.
      const level = block.level === 2 ? 2 : 1;
      const heads = run((b) => b.kind === "heading" && (b.level === 2 ? 2 : 1) === level);
      for (const head of heads) flows.push({ kind: "heading", block: head, level });
      release();
      continue;
    }

    flows.push({ kind: "paragraph", block });
    i++;
  }
  return flows;
}

/**
 * The marker a list item opens with, and the item's text without it.
 *
 * The marker is only lifted out when whitespace follows it, and that guard is not
 * theoretical. 12 of the columns manual's 113 list items read "*) modellabhängig",
 * where the recorded marker is `*` and removing it leaves a stray ") ". All ten of
 * the Hebrew section's items are worse: `-'א רויא` records the marker `-`, which is
 * the *last* character of the figure reference "איור א'-" and only looks like a
 * leading marker because the stored text is in visual order. Both keep their text
 * whole and print no marker of their own — a marker is never invented, so an item
 * whose marker cannot be lifted out simply shows the line as it is printed.
 */
export function splitMarker(block: Block): { block: Block; marker: string; text: string } {
  const match = MARKER_NOTE.exec(block.note ?? "");
  const marker = match?.[1] ?? "";
  const text = block.text;
  if (!marker || !text.startsWith(marker)) return { block, marker: "", text };
  const rest = text.slice(marker.length);
  if (rest !== "" && !/^\s/.test(rest)) return { block, marker: "", text };
  return { block, marker, text: rest.trimStart() };
}

/** The note internal/doc writes on one entry of a printed table of contents. */
const CONTENTS_NOTE = "a dot leader of ";

/** Whether a block is one entry of a printed table of contents. */
function isContentsEntry(block: Block): boolean {
  return block.kind === "list-item" && (block.note ?? "").startsWith(CONTENTS_NOTE);
}

/**
 * A contents entry split into the title and the page it points at.
 *
 * The dot leader is the document's own typesetting and is dropped from the DOM
 * rather than rendered: a row of literal periods read by a screen reader is noise,
 * and the leader is drawn with a rule instead. The dots are still in the block's
 * text, which is what search and the coverage check see, so nothing is lost — this
 * is presentation only.
 *
 * A line that does not split — no leader of four or more, or nothing after it —
 * keeps its whole text as the title and shows no page number, rather than guessing.
 * internal/doc will not classify such a line as an entry in the first place, so this
 * is the belt to that brace.
 */
export function splitEntry(block: Block): { block: Block; title: string; page: string } {
  const match = /^(.*?)[\s.]*\.{4,}\s*([\d\s\u2013\u2014-]*\d)\s*$/.exec(block.text);
  const title = match?.[1];
  const page = match?.[2];
  if (title === undefined || page === undefined) {
    return { block, title: block.text.trim(), page: "" };
  }
  return { block, title: title.trim(), page: page.replace(/\s+/g, " ").trim() };
}

/**
 * The page of the PDF a contents entry points at, or null where it must not be a
 * link at all.
 *
 * `printed` is what splitEntry pulled off the line -- the number the paper prints --
 * and `folioOffset` is the document's one constant, from the conversion response.
 * The map is `pdf = printed + offset`; internal/registry's folio.go carries the
 * measurement behind that being one constant per document and the rule for when
 * there is no honest answer.
 *
 * A range entry links to its first page. "Сухая уборка ... 15 - 23" goes to 15,
 * because that is where the section starts and where a reader following the entry
 * expects to arrive; the end of the range is a fact about the section's length, not
 * a second destination.
 *
 * Three things make it not a link, and each falls back to plain text rather than a
 * link that goes nowhere:
 *
 *   - No offset was served. The folios did not agree on one, so there is nothing to
 *     add. `folioOffset` is optional for exactly this reason and must never be
 *     defaulted to 0 -- the columns manual's real offset IS 0.
 *   - The line carries no page number, or none this can read.
 *   - The target is not a page this language's conversion holds. That is not a
 *     defensive check: the columns manual prints five languages' contents pages, so
 *     a German reader's entry can point at a page that is entirely Russian. The same
 *     check covers a target outside the document altogether -- `pages` holds only
 *     pages that exist and were converted, so page 0 and page 9999 fail it for the
 *     same reason and need no separate test.
 */
export function contentsTarget(
  printed: string,
  folioOffset: number | undefined,
  pages: ReadonlySet<number>,
): number | null {
  if (folioOffset === undefined) return null;
  // The first run of digits: a range entry links to where the section starts.
  const first = /\d+/.exec(printed);
  if (!first) return null;
  const target = Number(first[0]) + folioOffset;
  return pages.has(target) ? target : null;
}

/**
 * A run of adjacent table cells, as one flow per printed table.
 *
 * A new table starts where the row number stops advancing. That is what separates
 * the two troubleshooting tables printed side by side on page 57 of the columns
 * manual: the second opens with its row 1 directly after the first's row 7. Doing it
 * by geometry instead would have to tell two tables apart by their cells' text
 * boxes, which do not line up with the ruled columns at all.
 */
function tables(cells: Block[]): Flow[] {
  const flows: Flow[] = [];
  let current: Array<{ block: Block; row: number; col: number; span: number }> = [];
  let columns = 0;
  let prevRow = 0;
  let prevCol = 0;
  let lang = "";

  const flush = () => {
    if (current.length === 0) return;
    flows.push({ kind: "table", rows: grid(current, columns, lang), columns, lang });
    current = [];
  };

  for (const block of cells) {
    const match = CELL_NOTE.exec(block.note ?? "");
    if (!match) {
      // Not a cell this reader can place. Shown as prose rather than dropped: the
      // text is real and losing it silently is the one failure that must not happen.
      flush();
      flows.push({ kind: "paragraph", block });
      prevRow = 0;
      prevCol = 0;
      continue;
    }
    const row = Number(match[1]);
    const col = Number(match[3]);
    const cols = Number(match[4]);
    const span = match[5] ? Number(match[5]) : 1;
    if (
      current.length > 0 &&
      (cols !== columns || row < prevRow || (row === prevRow && col <= prevCol))
    ) {
      flush();
    }
    columns = cols;
    lang = block.lang ?? "";
    current.push({ block, row, col, span });
    prevRow = row;
    prevCol = col;
  }
  flush();
  return flows;
}

/**
 * The cells of one table as rows of a rectangular grid.
 *
 * Rows the pipeline recovered no cell for are absent rather than blank: page 52 of
 * the columns manual returns rows 1-6 and 9 of a nine-row table, and conversion.md
 * records why — a vertically merged cell is dropped by the row walk. Printing two
 * empty rows would be inventing evidence of something that is simply not there.
 *
 * A right-to-left table has its cells emitted in reverse column order, because
 * column 1 of the ruled grid is the leftmost and under `dir="rtl"` the first cell in
 * the markup is laid out on the right. Checked against the Hebrew section: the
 * header row's column 3 is "part" and column 1 is "replacement period", which is the
 * order the page prints them in when read from the right.
 */
function grid(
  cells: Array<{ block: Block; row: number; col: number; span: number }>,
  columns: number,
  lang: string,
): TableCell[][] {
  const byRow = new Map<number, Array<{ block: Block; col: number; span: number }>>();
  const order: number[] = [];
  for (const cell of cells) {
    const row = byRow.get(cell.row);
    if (row) row.push(cell);
    else {
      byRow.set(cell.row, [cell]);
      order.push(cell.row);
    }
  }

  const rtl = isRTL(lang);
  return order.map((rowNumber) => {
    const slots: TableCell[] = [];
    const placed = byRow.get(rowNumber) ?? [];
    let col = 1;
    while (col <= columns) {
      const cell = placed.find((c) => c.col === col);
      if (cell) {
        const span = Math.max(1, Math.min(cell.span, columns - col + 1));
        slots.push({ block: cell.block, colSpan: span });
        col += span;
      } else {
        slots.push({ block: null, colSpan: 1 });
        col++;
      }
    }
    return rtl ? slots.reverse() : slots;
  });
}
