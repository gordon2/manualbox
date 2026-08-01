/**
 * Build for folio-browser-check.tsx: the real reader, a real conversion, one page a
 * browser can open. Not part of `npm run build`; see the header of the .tsx.
 */
import { readFileSync } from "node:fs";

import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

const json = process.env.FOLIO_CHECK_JSON;
if (!json) throw new Error("set FOLIO_CHECK_JSON to a conversion response");

export default defineConfig({
  plugins: [react(), tailwindcss()],
  // Relative, so the built page opens over file:// without a server.
  base: "./",
  define: {
    __CONVERSION__: readFileSync(json, "utf8"),
    __DROP_OFFSET__: process.env.FOLIO_CHECK_DROP_OFFSET === "1",
    __FORCE_OFFSET__: process.env.FOLIO_CHECK_OFFSET ?? "null",
  },
  build: {
    outDir: process.env.FOLIO_CHECK_OUT ?? ".folio-check",
    emptyOutDir: true,
    rollupOptions: { input: "folio-browser-check.html" },
  },
});
