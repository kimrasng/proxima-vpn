import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// In production the app is served by nginx, which proxies /api, /sub,
// /metrics and /swagger to the api-server (see web/nginx.conf); the frontend
// therefore only ever uses relative paths. Mirror that here so `npm run dev`
// and `npm run preview` (the latter is what the Playwright e2e suite runs
// against) behave the same way instead of needing VITE_API_URL.
const apiTarget = process.env.VITE_API_PROXY_TARGET || "http://localhost:2053";

const apiProxy = {
  "/api": { target: apiTarget, changeOrigin: true },
  "/sub": { target: apiTarget, changeOrigin: true },
};

export default defineConfig({
  plugins: [react()],
  server: {
    port: 8080,
    strictPort: true,
    watch: process.env.DEV_DOCKER_POLL === "1" ? { usePolling: true, interval: 500 } : undefined,
    proxy: apiProxy,
  },
  preview: {
    port: 8080,
    proxy: apiProxy,
  },
  build: {
    rollupOptions: {
      output: {
        // The router is shared by every route but is large enough to push the
        // entry over Vite's 500 kB threshold. Keep it in a separate shared
        // chunk without assigning React or Cloudscape's internal modules.
        manualChunks(id) {
          if (id.includes("/node_modules/react-router/") || id.includes("/node_modules/react-router-dom/")) {
            return "router";
          }
        },
      },
    },
  },
});
