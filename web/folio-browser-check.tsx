/**
 * Look at a contents entry in a real browser, and click it.
 *
 * reader-check.tsx renders the reader to static markup, which is enough to read what
 * is on the page but cannot answer the question a link raises: does clicking it move
 * the reader. So this mounts the REAL [Reader] -- not a copy of its wiring -- in a
 * real browser, with a real document's conversion JSON standing in for the network,
 * and lets Chrome click.
 *
 * `fetch` is replaced before mounting rather than the component being given props it
 * does not have, so what runs is the same code path the app runs: Reader asks the api
 * client, the client asks fetch, and the conversion arrives with whatever
 * `folioOffset` the server actually sent -- including none, which is a case worth
 * looking at.
 *
 * Usage, from web/:
 *   npx vite build --config folio-browser-check.vite.ts
 *   open .folio-check/index.html
 *
 * The conversion JSON is inlined at build time from FOLIO_CHECK_JSON. It is a real
 * manual's text and is never committed; .folio-check is build output.
 */
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import type { Conversion, Doc } from "./src/api/types";
import { Reader } from "./src/screens/Reader";
import "./src/index.css";

// Inlined by the vite config's `define`.
declare const __CONVERSION__: Conversion;
declare const __DROP_OFFSET__: boolean;
declare const __FORCE_OFFSET__: number | null;

const conversion: Conversion = JSON.parse(JSON.stringify(__CONVERSION__));
if (__DROP_OFFSET__) {
  // The document whose folios agreed on nothing. Every entry must fall back to
  // plain text, and the number must still be readable.
  delete conversion.folioOffset;
} else if (__FORCE_OFFSET__ !== null) {
  // An offset that lands most entries on pages this language does not hold. The
  // columns manual does not do this to itself -- each language's contents page
  // prints its own folios, which are its own pages -- so it is forced here to see
  // what a reader meets when a target is not servable.
  conversion.folioOffset = __FORCE_OFFSET__;
}

window.fetch = (async () =>
  new Response(JSON.stringify(conversion), {
    status: 200,
    headers: { "content-type": "application/json" },
  })) as typeof fetch;

const doc: Doc = {
  id: "doc_example",
  deviceId: "dev_example",
  blobSha256: "0".repeat(64),
  filename: "wet-and-dry-vacuum.pdf",
  kind: "manual",
  state: "ready",
  pageCount: 68,
  createdAt: "",
  updatedAt: "",
};

const root = document.getElementById("root");
if (!root) throw new Error("no #root");
createRoot(root).render(
  <StrictMode>
    <div className="mx-auto max-w-3xl p-6">
      <Reader
        doc={doc}
        backTo="Wet and dry vacuum"
        languages={[
          { lang: "de", name: "German" },
          { lang: "uk", name: "Ukrainian" },
        ]}
        startLang="de"
        onBack={() => undefined}
      />
    </div>
  </StrictMode>,
);
