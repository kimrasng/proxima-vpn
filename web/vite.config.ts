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
    proxy: apiProxy,
  },
  preview: {
    port: 8080,
    proxy: apiProxy,
  },
});
