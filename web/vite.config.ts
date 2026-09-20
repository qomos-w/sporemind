import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { fileURLToPath, URL } from "node:url";

export default defineConfig({
  envDir: "..",
  define: {
    __BUILD_TYPE__: JSON.stringify(process.env.VITE_BUILD_TYPE ?? 'release'),
    __BUILD_FLAVOR__: JSON.stringify(process.env.VITE_BUILD_FLAVOR ?? 'default'),
    __BUILD_VERSION__: JSON.stringify(process.env.VITE_BUILD_VERSION ?? '0.0.0'),
    __WAILS_PRODUCTION__: JSON.stringify(process.env.VITE_WAILS_PRODUCTION === 'true'),
  },
  plugins: [react()],
  resolve: {
    alias: [
      {
        find: "@qomos/sporemind-theme/tokens.css",
        replacement: fileURLToPath(new URL("../theme/src/tokens.css", import.meta.url)),
      },
      {
        find: "@qomos/sporemind-shell",
        replacement: fileURLToPath(new URL("../shell/src/index.ts", import.meta.url)),
      },
      {
        find: "@qomos/sporemind-theme",
        replacement: fileURLToPath(new URL("../theme/src/index.ts", import.meta.url)),
      },
      {
        find: "@qomos/gospore-client",
        replacement: fileURLToPath(new URL("./vendor/gospore-client/src/index.ts", import.meta.url)),
      },
      {
        find: "@qomos/spore-ts/registry",
        replacement: fileURLToPath(new URL("./vendor/spore-ts/src/registry.ts", import.meta.url)),
      },
      {
        find: "@qomos/spore-ts/callables",
        replacement: fileURLToPath(new URL("./vendor/spore-ts/src/callables.ts", import.meta.url)),
      },
      {
        find: "@qomos/spore-ts",
        replacement: fileURLToPath(new URL("./vendor/spore-ts/src/index.ts", import.meta.url)),
      },
    ],
  },
  server: {
    host: "127.0.0.1",
    port: 5558,
    strictPort: true,
    allowedHosts: ["wails.localhost", "localhost", "127.0.0.1"],
    fs: {
      allow: [".."],
    },
    hmr: {
      host: "127.0.0.1",
      port: 5558,
    },
    proxy: {
      "/api": {
        target: process.env.VITE_DEV_API_TARGET ?? "http://127.0.0.1:18080",
        changeOrigin: true,
      },
      "/ws": {
        target: process.env.VITE_DEV_API_TARGET ?? "http://127.0.0.1:18080",
        changeOrigin: true,
        ws: true,
      },
    },
  },
  build: {
    outDir: "../pkg/web/dist",
    rollupOptions: {
      input: {
        main: fileURLToPath(new URL("./index.html", import.meta.url)),
        "plugin-bridge-client": fileURLToPath(new URL("./src/plugin-bridge-client.ts", import.meta.url)),
      },
      output: {
        entryFileNames: "assets/[name].js",
        chunkFileNames: "assets/[name].js",
        assetFileNames: "assets/[name][extname]",
        manualChunks(id) {
          const norm = id.replace(/\\/g, "/");
          if (norm.includes("@qomos/sporemind-shell") || norm.includes("/sporemind/shell/")) {
            return "shell";
          }
          if (norm.includes("@qomos/sporemind-theme") || norm.includes("/sporemind/theme/")) {
            return "theme";
          }
        },
      },
    },
  },
  optimizeDeps: {
    exclude: ["@qomos/sporemind-theme", "@qomos/sporemind-shell", "@qomos/gospore-client", "@qomos/spore-ts"],
  },
});
