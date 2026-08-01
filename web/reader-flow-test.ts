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

import { contentsTarget } from "./src/screens/reader-flow.ts";

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
