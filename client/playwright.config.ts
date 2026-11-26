import { defineConfig } from "@playwright/test";

// End-to-end checks in a real browser against the real server.
//
// By default this starts its own sandtable-server on 8091 with a fresh match
// that serves client/dist, so run `make build` first. Set SANDTABLE_URL to
// use a server that is already running instead (it must host a fresh match).
const external = process.env.SANDTABLE_URL;

export default defineConfig({
  testDir: "e2e",
  timeout: 180_000,
  workers: 1,
  reporter: "list",
  outputDir: "test-results/e2e",
  use: {
    baseURL: external ?? "http://127.0.0.1:8091",
    viewport: { width: 1100, height: 760 },
    screenshot: "only-on-failure",
  },
  webServer: external
    ? undefined
    : {
        command: "../bin/sandtable-server -addr 127.0.0.1:8091 -state-dir= -web ../client/dist",
        cwd: "../server",
        url: "http://127.0.0.1:8091/api/match?id=default",
        reuseExistingServer: false,
        stdout: "ignore",
        stderr: "pipe",
      },
});
