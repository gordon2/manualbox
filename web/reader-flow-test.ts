/**
 * The rules in reader-flow that are worth asserting rather than only looking at.
 *
 * Run from web/ with `npm test`, which is `node --test`. Node runs TypeScript directly
 * by stripping the types, so this needs no test framework, no transform and no new
 * dependency -- which is why it exists at all: this project had no JavaScript test
 * runner, and adding one to assert a rule this size would have cost more than the
 * rule.
 *
 * It sits beside reader-check.tsx rather than under src/, which is where this repo
 * already keeps the code that runs in Node: tsconfig.json includes only `src` and
 * `vite.config.ts`, so a file here is outside the browser app typecheck and can
 * import `node:test` and a `.ts` path without pulling `@types/node` into the
 * application scope and without relaxing `allowImportingTsExtensions` for every
 * file. The rule under test is in src and is typechecked; a wrong call from here
 * fails as an assertion.
 */
import assert from "node:assert/strict";
import { test } from "node:test";

import { contentsTarget, placeOnPage, type Slot } from "./src/screens/reader-flow.ts";

// The columns manual's German conversion: 68 pages exist, and this language holds
// some of them. The gaps are real -- the pages between are other languages'.
const german = new Set([1, 2, 3, 12, 14, 15, 20, 23, 52, 57, 68]);

test("a contents entry whose target this language holds becomes a link", () => {
  // The columns manual: offset 0, and page 14 is one of its German pages.
  assert.equal(contentsTarget("14", 0, german), 14);
});

test("the offset is added, and offset zero is a real answer", () => {
  // The sequential manual's offset is 6: printed 9 is PDF page 15.
  assert.equal(contentsTarget("9", 6, new Set([15])), 15);
  // The same printed number with the columns manual's offset is a different page,
  // which is what makes 0 an answer rather than the absence of one.
  assert.equal(contentsTarget("9", 0, new Set([9, 15])), 9);
});

test("a range entry links to its first page", () => {
  // "Сухая уборка ... 15 – 23" goes to 15, at offset 0. The en dash is the one the
  // splitEntry pattern admits, so both dashes are checked.
  assert.equal(contentsTarget("15 – 23", 0, german), 15);
  assert.equal(contentsTarget("15 - 23", 0, german), 15);
  // And the end of the range is not the destination even when it would be servable.
  assert.equal(contentsTarget("15 – 23", 0, new Set([15, 23])), 15);
});

test("no offset was served, so nothing is a link", () => {
  // The folios did not agree on one. Every entry stays plain text -- and this must
  // not be read as offset 0, which is why the parameter is optional rather than
  // defaulted.
  assert.equal(contentsTarget("14", undefined, german), null);
});

test("a target outside the document is not a link", () => {
  // Printed 9999 with offset 0 is no page of anything.
  assert.equal(contentsTarget("9999", 0, german), null);
  // And a printed number the offset drags below page 1.
  assert.equal(contentsTarget("3", -10, german), null);
});

test("a target this language's conversion does not hold is not a link", () => {
  // The one that fires on a real document: the columns manual prints five
  // languages' contents pages, so a German entry can name a page that is entirely
  // Russian. Page 13 exists in the PDF and is not in the German conversion.
  assert.ok(!german.has(13));
  assert.equal(contentsTarget("13", 0, german), null);
});

test("an entry with no page number is not a link", () => {
  // splitEntry returns "" where the line had no leader or nothing after it.
  assert.equal(contentsTarget("", 0, german), null);
});

// ---------------------------------------------------------------------------
// Where a picture goes: placeOnPage.
//
// Every box below is copied from a real conversion response, page and all, because
// the whole rule is a reading of real geometry and made-up coordinates would only
// test the arithmetic. The text is cut to a few words; nothing else is changed.
// ---------------------------------------------------------------------------

let seq = 0;
/** A block at a measured box. `kind` and `note` matter only where a test uses them. */
function block(
  page: number,
  x0: number,
  x1: number,
  y0: number,
  y1: number,
  text: string,
  extra: { kind?: string; note?: string; lang?: string } = {},
) {
  return {
    page,
    regionX0: 0,
    index: seq++,
    kind: (extra.kind ?? "paragraph") as never,
    text,
    lang: extra.lang ?? "de",
    x0,
    x1,
    y0,
    y1,
    lines: 1,
    chars: text.length,
    ...(extra.note === undefined ? {} : { note: extra.note }),
  };
}

/** A figure at a measured box. */
function figure(page: number, index: number, x0: number, x1: number, y0: number, y1: number) {
  return {
    page,
    index,
    x0,
    y0,
    x1,
    y1,
    ink: 100,
    textFraction: 0,
    dpi: 216,
    pixelWidth: Math.round((x1 - x0) * 2),
    pixelHeight: Math.round((y1 - y0) * 2),
    sha256: `${index}`.padStart(64, "0"),
  };
}

/** Every flow under a slot, in the order a reader meets them. */
function flowsUnder(slots: Slot[]): string[] {
  return slots.flatMap((slot) =>
    slot.kind === "flows"
      ? slot.flows.map((flow) =>
          flow.kind === "figure"
            ? `figure#${flow.figure.index}`
            : flow.kind === "paragraph" || flow.kind === "heading"
              ? flow.block.text
              : flow.kind === "table"
                ? `table(${flow.rows.length} rows)`
                : flow.kind === "list"
                  ? `list(${flow.items.length})`
                  : `contents(${flow.entries.length})`,
        )
      : slot.columns.flatMap((c) => flowsUnder(c.slots)),
  );
}

test("a drawing printed beside its text is set beside it, and beside the whole run", () => {
  // Page 42 of the columns manual, its first printed row: one drawing in the left rail
  // and TWO paragraphs to the right of it. The unit matters — the paper is not putting
  // the picture next to a paragraph, it is putting it next to a step, and 44 of the
  // 49 such rows across that manual hold 2 or more blocks.
  const slots = placeOnPage(
    [
      block(42, 323, 584, 61, 111, "Sofern sich noch Schmutz"),
      block(42, 323, 581, 114, 164, "Bei starker Verschmutzung"),
    ],
    [figure(42, 0, 43, 288, 65, 241)],
  );
  assert.equal(slots.length, 1);
  const slot = slots[0];
  assert.equal(slot?.kind, "beside");
  if (slot?.kind !== "beside") return;
  assert.equal(slot.strip, false);
  assert.deepEqual(flowsUnder(slot.columns[0]?.slots ?? []), ["figure#0"]);
  assert.deepEqual(flowsUnder(slot.columns[1]?.slots ?? []), [
    "Sofern sich noch Schmutz",
    "Bei starker Verschmutzung",
  ]);
});

test("drawings printed in a row become a row, not a pile", () => {
  // Page 533 of the sequential manual: two drawings of one operation, printed side by
  // side under one sentence. Ordering by `y0` alone put them one above the other, which
  // is the complaint this rule exists for -- and it also got them backwards, because
  // the right-hand drawing starts higher up the page than the left-hand one.
  const slots = placeOnPage(
    [],
    [figure(533, 0, 306, 395, 151, 318), figure(533, 2, 84, 206, 194, 295)],
  );
  assert.equal(slots.length, 1);
  const slot = slots[0];
  assert.equal(slot?.kind, "beside");
  if (slot?.kind !== "beside") return;
  // A strip, so it keeps its row at any width rather than stacking.
  assert.equal(slot.strip, true);
  // Printed order, left to right: the one at x84 is read first.
  assert.deepEqual(flowsUnder(slots), ["figure#2", "figure#0"]);
});

test("two printed columns are not interleaved by height", () => {
  // Page 529 of the sequential manual. Left column x67-448, right x479-854, gutter 31
  // units wide. By `y0` the right column's step 3 came out ABOVE the left column's
  // heading, which is the "merged text" half of the report.
  const slots = placeOnPage(
    [
      block(529, 67, 247, 98, 112, "Основание промывочной панели", { lang: "ru" }),
      block(529, 67, 435, 117, 155, "Базовая станция будет", { lang: "ru" }),
      block(529, 480, 839, 93, 107, "3. Переверните промывочную", { lang: "ru" }),
      block(529, 480, 816, 105, 119, "ролика и сам ролик", { lang: "ru" }),
    ],
    [],
  );
  const slot = slots[0];
  assert.equal(slots.length, 1);
  assert.equal(slot?.kind, "beside");
  if (slot?.kind !== "beside") return;
  assert.deepEqual(flowsUnder(slot.columns[0]?.slots ?? []), [
    "Основание промывочной панели",
    "Базовая станция будет",
  ]);
  assert.deepEqual(flowsUnder(slot.columns[1]?.slots ?? []), [
    "3. Переверните промывочную",
    "ролика и сам ролик",
  ]);
});

test("a ruled table is never cut, however its cells are placed", () => {
  // Page 57 of the columns manual. Its cells sit in two vertical groups with a clear
  // 15-to-38 unit gap between them, so cutting on geometry shattered the table into
  // its 25 cells and `tables` could no longer assemble any of them. conversion.md
  // already measured why the boxes cannot be trusted for this: a cell's box is its
  // TEXT's extent, not the ruled cell's.
  const cell = (x0: number, x1: number, y0: number, y1: number, row: number, col: number) =>
    block(57, x0, x1, y0, y1, `r${row}c${col}`, {
      kind: "table",
      note: `row ${row} of 7, column ${col} of 2 of a ruled table`,
    });
  const slots = placeOnPage(
    [
      cell(36, 164, 130, 163, 2, 1),
      cell(180, 407, 130, 196, 2, 2),
      cell(36, 137, 241, 258, 4, 1),
      cell(179, 424, 241, 388, 4, 2),
    ],
    [],
  );
  // One run, no cut, and the four cells reach the reader as one two-row grid.
  assert.deepEqual(
    slots.map((s) => s.kind),
    ["flows"],
  );
  assert.deepEqual(flowsUnder(slots), ["table(2 rows)"]);
});

test("below the top level, prose is not set beside prose", () => {
  // Page 52 of the columns manual. Its own gutter is at x585-604, and INSIDE the left
  // column two runs of German sit 22 units apart at x305/x327 -- wide enough to look
  // like a gutter and not one: "Parkettreinigungsdüse" is a caption within the same
  // measure, not a column beside "Die nachfolgenden Unterkapitel". Without the guard
  // the page came apart into stacks of fragments; with it the page is cut once.
  const slots = placeOnPage(
    [
      block(52, 43, 305, 62, 127, "Die nachfolgenden Unterkapitel"),
      block(52, 327, 577, 66, 137, "Parkettreinigungsdüse + Micro"),
      block(52, 43, 98, 137, 154, "Trockensaugen"),
      block(52, 604, 866, 62, 484, "Sollten auf den gereinigten"),
    ],
    [],
  );
  assert.equal(slots.length, 1);
  const slot = slots[0];
  assert.equal(slot?.kind, "beside");
  if (slot?.kind !== "beside") return;
  // The left column is ONE run: the 22-unit gap inside it was not cut.
  assert.deepEqual(
    slot.columns.map((c) => c.slots.map((s) => s.kind)),
    [["flows"], ["flows"]],
  );
  assert.deepEqual(flowsUnder(slot.columns[0]?.slots ?? []), [
    "Die nachfolgenden Unterkapitel",
    "Parkettreinigungsdüse + Micro",
    "Trockensaugen",
  ]);
});

test("a page the paper did not divide is left as one run", () => {
  // Page 521 of the sequential manual: a full-bleed callout diagram. Its labels reach
  // across the drawings they annotate -- "ИК-камера ... Вентиляционное отверстие" runs
  // x266-539, straight over the 469-484 gap that would otherwise read as a gutter --
  // so no empty vertical band survives and the page must come out exactly as it did
  // before this rule existed. It is the page the report named first, and it is the one
  // the rule has to leave alone.
  const slots = placeOnPage(
    [
      block(521, 66, 250, 43, 76, "Обзор изделия", { lang: "ru" }),
      block(521, 266, 469, 158, 172, "Вспомогательная светодиодная подсветка", { lang: "ru" }),
      block(521, 266, 539, 178, 228, "ИК-камера на основе ИИ Вентиляционное", { lang: "ru" }),
      block(521, 754, 876, 116, 161, "кнопку в течение 3 секунд", { lang: "ru" }),
    ],
    [figure(521, 0, 484, 748, 96, 278), figure(521, 1, 66, 397, 117, 364)],
  );
  assert.deepEqual(
    slots.map((s) => s.kind),
    ["flows"],
  );
});

test("a right-to-left page reads its first column on the right", () => {
  // The DOM order is the reading order, and `dir` on the row does the laying out, so
  // the rightmost column has to come FIRST in the markup. Hebrew boxes are in the same
  // left-origin space as everything else -- the language decides the order, not the
  // coordinates.
  const slots = placeOnPage(
    [
      block(1, 60, 300, 100, 140, "left on the page", { lang: "he" }),
      block(1, 400, 640, 100, 140, "right on the page", { lang: "he" }),
    ],
    [],
  );
  assert.deepEqual(flowsUnder(slots), ["right on the page", "left on the page"]);
  // And the same geometry in a left-to-right language reads the other way round.
  const ltr = placeOnPage(
    [
      block(1, 60, 300, 100, 140, "left on the page", { lang: "de" }),
      block(1, 400, 640, 100, 140, "right on the page", { lang: "de" }),
    ],
    [],
  );
  assert.deepEqual(flowsUnder(ltr), ["left on the page", "right on the page"]);
});

test("a strip of drawings still knows which way the page reads", () => {
  // The case that has no text to read a direction off. A strip holds only pictures, so
  // asking its content which way it goes returns nothing -- and the first version of
  // this did exactly that, defaulting to left-to-right and laying the columns out
  // against the logical order it had just put them in. The direction is a fact about
  // the page, so it travels on the slot.
  const rtl = placeOnPage(
    [block(1, 60, 640, 40, 60, "כותרת", { lang: "he" })],
    [figure(1, 0, 60, 300, 100, 260), figure(1, 1, 400, 640, 100, 260)],
  );
  const strip = rtl.find((s) => s.kind === "beside");
  assert.equal(strip?.kind, "beside");
  if (strip?.kind !== "beside") return;
  assert.equal(strip.strip, true);
  assert.equal(strip.rtl, true);
  // Logical order: the picture at x400 is the right-hand one, so it is read first.
  assert.deepEqual(flowsUnder([strip]), ["figure#1", "figure#0"]);
});
