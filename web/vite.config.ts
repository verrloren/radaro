import { defineConfig, type ProxyOptions } from "vite";
import react from "@vitejs/plugin-react";

const API = "http://127.0.0.1:8042";

// The Go server rejects POST/DELETE whose Origin doesn't match its host,
// so present proxied requests as same-origin with the backend.
const proxy: ProxyOptions = {
  target: API,
  changeOrigin: true,
  headers: { origin: API },
};

export default defineConfig({
  plugins: [react()],
  base: "/",
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  server: {
    proxy: {
      "/api": proxy,
      "/health": proxy,
    },
  },
});
