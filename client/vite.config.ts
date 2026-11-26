import { defineConfig } from "vitest/config";

// The Go server listens on 8080. In development Vite serves the client on 5173
// and forwards the WebSocket and the HTTP API to it.
const server = process.env.SANDTABLE_SERVER ?? "127.0.0.1:8080";

export default defineConfig({
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      "/ws": { target: `ws://${server}`, ws: true },
      "/api": { target: `http://${server}` },
    },
    // The repository lives on a Windows drive under WSL, where inotify does not
    // see changes. Polling is slower but works everywhere.
    watch: { usePolling: true, interval: 300 },
  },
  test: {
    include: ["src/**/*.test.ts"],
    environment: "node",
  },
});
