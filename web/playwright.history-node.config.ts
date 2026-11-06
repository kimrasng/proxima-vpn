import { defineConfig } from "@playwright/test";
import historyConfig from "./playwright.history.config";

// B5 owns this preview and output; never reuse another lane's server.
export default defineConfig(historyConfig, {
  testMatch: /connection-history\.node-scope\.spec\.ts/,
  outputDir: "test-results/history-node",
  use: { baseURL: "http://127.0.0.1:4184" },
});
