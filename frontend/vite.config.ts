/// <reference types="vitest" />
import { defineConfig, type Plugin } from "vitest/config";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { TanStackRouterVite } from "@tanstack/router-plugin/vite";
import path from "node:path";

/**
 * sonner injects its stylesheet at runtime through a `<style>` element, which
 * the production Content-Security-Policy (`style-src 'self'`, set by the Go
 * server) blocks. Neutralise the injector in the production bundle; the same
 * CSS ships as a regular stylesheet via `import "sonner/dist/styles.css"` in
 * the Toaster component. Fails the build if sonner changes its internals, so
 * an upgrade cannot silently reintroduce inline styles.
 *
 * Build-only: the dev server pre-bundles dependencies (so this hook would not
 * see sonner) and is not served with the CSP anyway.
 */
function sonnerExternalStyles(): Plugin {
  const sonnerEntry = /[\\/]node_modules[\\/]sonner[\\/]dist[\\/]index\.m?js$/;
  const injector = "function __insertCSS(code) {";
  return {
    name: "kafkito:sonner-external-styles",
    apply: "build",
    enforce: "pre",
    transform: {
      filter: { id: sonnerEntry },
      handler(code, id) {
        if (!sonnerEntry.test(id)) return null;
        if (!code.includes(injector)) {
          throw new Error(
            `sonner no longer defines ${injector}; update sonnerExternalStyles (CSP style-src 'self')`,
          );
        }
        return { code: code.replace(injector, `${injector} return;`), map: null };
      },
    },
  };
}

const FRONTEND_PORT = Number(process.env.KAFKITO_FRONTEND_PORT ?? 37422);
const BACKEND_PORT = Number(process.env.KAFKITO_BACKEND_PORT ?? 37421);
const BACKEND_URL = `http://localhost:${BACKEND_PORT}`;

export default defineConfig({
  plugins: [
    TanStackRouterVite({ target: "react", autoCodeSplitting: true }),
    react(),
    tailwindcss(),
    sonnerExternalStyles(),
  ],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    port: FRONTEND_PORT,
    proxy: {
      "/api": BACKEND_URL,
      "/user-api": BACKEND_URL,
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  test: {
    environment: "happy-dom",
    globals: true,
    include: ["src/**/*.test.{ts,tsx}"],
    setupFiles: ["./vitest.setup.ts"],
  },
});
