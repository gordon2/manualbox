import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    // Build straight into the Go package that embeds it, so `make build`
    // produces one binary containing the UI.
    outDir: "../internal/frontend/dist/app",
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    // In development the SPA is served by Vite and the API by the Go binary on
    // 7745. Proxying keeps them same-origin, so session cookies and the CSRF
    // origin check behave exactly as they do in production.
    proxy: {
      "/api": {
        target: "http://localhost:7745",
        changeOrigin: false,
      },
    },
  },
});
