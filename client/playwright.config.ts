import { defineConfig } from "@playwright/test";

// End-to-end checks in a real browser against the real server.
//
// By default this starts its own sandtable-servers with fresh matches that
// serve client/dist (the sample map on 8091, the generated map on 8092), so
// run `make build` first. Set SANDTABLE_URL to
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
  // The sample map on 8091 for play, the generated map on 8092 for the map checks.
  webServer: external
    ? undefined
    : [
        ["sample", 8091],
        ["generated", 8092],
      ].map(([scenario, port]) => ({
        command: `../bin/sandtable-server -addr 127.0.0.1:${port} -state-dir= -web ../client/dist -scenario ${scenario}`,
        cwd: "../server",
        url: `http://127.0.0.1:${port}/api/match?id=default`,
        reuseExistingServer: false,
        stdout: "ignore" as const,
        stderr: "pipe" as const,
      })),
});
