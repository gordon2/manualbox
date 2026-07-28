/**
 * Render the search screen to HTML and look at it.
 *
 * The screen is behind a session, so the way to see it is to render it the way it
 * first paints: hand SearchResultsView a real `GET /search` response and print the
 * markup. Screenshot the result with a headless browser and the compiled stylesheet
 * to see what a person sees.
 *
 * Usage, from web/:
 *   npx vite build --ssr search-check.tsx --outDir .search-check --logLevel error
 *   node .search-check/search-check.js <results.json> [<results.json> …]
 *
 * The JSON is whatever `GET /api/v1/search?q=…` returned. It is not committed: it is
 * a real manual's text.
 */
import { renderToStaticMarkup } from "react-dom/server";

import type { SearchResults } from "./src/api/types";
import { SearchBox, SearchResultsView } from "./src/screens/Search";

const paths = process.argv.slice(2).filter((a) => !a.startsWith("--"));
if (paths.length === 0) {
  console.error("usage: node search-check.js <results.json> [<results.json> …]");
  process.exit(2);
}

const fs = await import("node:fs");

const sections = paths.map((path) => {
  const results = JSON.parse(fs.readFileSync(path, "utf8")) as SearchResults;
  return renderToStaticMarkup(
    <main className="mx-auto max-w-3xl space-y-8 px-6 py-10">
      <section className="space-y-3">
        <SearchBox query={results.query} onSearch={() => undefined} />
        <SearchResultsView results={results} onOpen={() => undefined} opening={null} />
      </section>
    </main>,
  );
});

console.log(sections.join('\n<hr class="border-rule" />\n'));
