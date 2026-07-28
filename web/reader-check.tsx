/**
 * Render the reader to HTML and read what comes out.
 *
 * There is no browser automation on this machine, so this is the substitute: hand
 * the real screen a real document's conversion JSON, render it with
 * react-dom/server, and print the text and structure a person should see. It caught
 * three things a green typecheck did not — a list marker printed twice, a table's
 * columns mirrored the wrong way, and a figure landing after the paragraph that
 * introduces it.
 *
 * Usage, from web/:
 *   npx vite build --ssr reader-check.tsx --outDir .reader-check --logLevel error
 *   node .reader-check/reader-check.js <conversion.json> [--html] [--pages 2,14,57]
 *
 * The JSON is whatever `GET /api/v1/documents/{id}/conversion?lang=de` returned. It
 * is not committed: it is a real manual's text.
 */
import { renderToStaticMarkup } from "react-dom/server";

import type { Conversion, Doc } from "./src/api/types";
import { readingOrder } from "./src/screens/reader-flow";
import { Reader, ReaderPages } from "./src/screens/Reader";

const args = process.argv.slice(2);
const path = args.find((a) => !a.startsWith("--"));
if (!path) {
  console.error("usage: node reader-check.js <conversion.json> [--html] [--pages 2,14]");
  process.exit(2);
}
const only = (() => {
  const i = args.indexOf("--pages");
  if (i < 0) return null;
  const list = args[i + 1];
  if (!list) return null;
  return new Set(list.split(",").map((n) => Number(n)));
})();

const fs = await import("node:fs");
const conversion = JSON.parse(fs.readFileSync(path, "utf8")) as Conversion;

if (args.includes("--shell")) {
  // The screen around the document: the way back, the title, the language chips and
  // the state a document that is not ready shows instead of content. Effects do not
  // run in a server render, so this is the first paint, before the fetch returns.
  const doc = {
    id: "doc_example",
    deviceId: "dev_example",
    blobSha256: "0".repeat(64),
    filename: "wet-and-dry-vacuum.pdf",
    kind: "manual",
    state: "ready",
    pageCount: 68,
    createdAt: "",
    updatedAt: "",
  } satisfies Doc;
  console.log(
    renderToStaticMarkup(
      <Reader
        doc={doc}
        backTo="Wet and dry vacuum"
        languages={[
          { lang: "de", name: "German" },
          { lang: "uk", name: "Ukrainian" },
        ]}
        onBack={() => undefined}
      />,
    )
      .replace(/></g, ">\n<")
      .replace(/ class="[^"]*"/g, ""),
  );
  process.exit(0);
}

const started = performance.now();
const pages = readingOrder(conversion.blocks, conversion.figures);
const ordered = performance.now();
const shown = only ? pages.filter((p) => only.has(p.page)) : pages;
const html = renderToStaticMarkup(<ReaderPages pages={shown} documentId="doc_example" />);
const rendered = performance.now();

console.error(
  [
    `${conversion.blocks.length} blocks and ${conversion.figures.length} figures`,
    `lang=${JSON.stringify(conversion.lang)} state=${conversion.state}`,
    `${pages.length} pages, ${shown.length} rendered`,
    `readingOrder ${(ordered - started).toFixed(1)} ms`,
    `renderToStaticMarkup ${(rendered - ordered).toFixed(1)} ms`,
    `${html.length.toLocaleString()} bytes of HTML`,
    `${(html.match(/<[a-z]/g) ?? []).length.toLocaleString()} elements`,
    `${(html.match(/<img /g) ?? []).length} images`,
    `${(html.match(/dir="rtl"/g) ?? []).length} right-to-left elements`,
  ].join("\n"),
);

if (args.includes("--html")) {
  console.log(html);
} else {
  // The text a person would see, with the structure named in the margin. Indentation
  // is the nesting: a table's cells sit under their row.
  console.log(outline(html));
}

/** The rendered HTML as an indented outline of what is on the page. */
function outline(markup: string): string {
  const lines: string[] = [];
  const tag =
    /<(\/?)(article|section|div|h2|h3|p|ul|li|table|tbody|tr|th|td|img|span|figure)\b([^>]*)>/g;
  let cursor = 0;
  let depth = 0;
  const stack: string[] = [];
  let pending: string[] = [];

  const flush = () => {
    const text = pending.join("").replace(/\s+/g, " ").trim();
    pending = [];
    if (!text) return;
    const owner = stack[stack.length - 1] ?? "";
    lines.push(`${"  ".repeat(depth)}${label(owner)}${decode(text)}`);
  };

  for (let m = tag.exec(markup); m; m = tag.exec(markup)) {
    pending.push(markup.slice(cursor, m.index));
    cursor = m.index + m[0].length;
    const [, closing, name, attrs = ""] = m;
    if (name === "img") {
      flush();
      const alt = /alt="([^"]*)"/.exec(attrs)?.[1] ?? "";
      const width = /width="(\d+)"/.exec(attrs)?.[1] ?? "?";
      const height = /height="(\d+)"/.exec(attrs)?.[1] ?? "?";
      lines.push(`${"  ".repeat(depth)}[image ${width}x${height}] ${decode(alt)}`);
      continue;
    }
    if (closing) {
      flush();
      stack.pop();
      depth = Math.max(0, depth - 1);
      continue;
    }
    flush();
    const dir = /dir="(rtl|ltr)"/.exec(attrs)?.[1];
    const span = /colspan="(\d+)"/i.exec(attrs)?.[1];
    // A container that holds no text of its own would otherwise be invisible, and
    // two tables printed side by side arriving as one is exactly the mistake this
    // check exists to catch.
    if (name === "table" || name === "tr" || name === "ul" || name === "figure") {
      lines.push(`${"  ".repeat(depth)}<${name}${dir === "rtl" ? " dir=rtl" : ""}>`);
    }
    stack.push(name + (dir === "rtl" ? " rtl" : "") + (span ? ` span=${span}` : ""));
    depth++;
  }
  pending.push(markup.slice(cursor));
  flush();
  return lines.join("\n");
}

function label(owner: string): string {
  if (!owner) return "";
  const [name, ...rest] = owner.split(" ");
  const extra = rest.length > 0 ? ` ${rest.join(" ")}` : "";
  return `${(name ?? "").padEnd(5)}${extra ? extra.padEnd(8) : "        "} `;
}

function decode(s: string): string {
  return s
    .replace(/&quot;/g, '"')
    .replace(/&#x27;/g, "'")
    .replace(/&amp;/g, "&")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&rsquo;/g, "’");
}
