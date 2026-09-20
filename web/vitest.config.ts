import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import { fileURLToPath } from 'url'

export default defineConfig({
  plugins: [react()],
  // Mirror vite.config.ts compile-time defines so suites importing
  // src/config/buildConfig.ts run outside a vite build.
  define: {
    __BUILD_TYPE__: JSON.stringify(process.env.VITE_BUILD_TYPE ?? 'release'),
    __BUILD_FLAVOR__: JSON.stringify(process.env.VITE_BUILD_FLAVOR ?? 'default'),
    __BUILD_VERSION__: JSON.stringify(process.env.VITE_BUILD_VERSION ?? '0.0.0'),
    __WAILS_PRODUCTION__: JSON.stringify(process.env.VITE_WAILS_PRODUCTION === 'true'),
  },
  server: { fs: { allow: ['..'], deny: ['.env', '.env.*', '*.{crt,pem}'] } },
  // Mirror the @qomos aliases from vite.config.ts. Without them vitest
  // resolves the node_modules copies, which ship compiled .js next to .ts
  // sources; extensionless imports inside those copies fall through to Node
  // ESM resolution (externalized deps) and fail with "Cannot find module".
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
        replacement: fileURLToPath(new URL("../../gospore/web-client/src/index.ts", import.meta.url)),
      },
      {
        find: "@qomos/spore-ts/registry",
        replacement: fileURLToPath(new URL("../../spore/ts/src/registry.ts", import.meta.url)),
      },
      {
        find: "@qomos/spore-ts/callables",
        replacement: fileURLToPath(new URL("../../spore/ts/src/callables.ts", import.meta.url)),
      },
      {
        find: "@qomos/spore-ts",
        replacement: fileURLToPath(new URL("../../spore/ts/src/index.ts", import.meta.url)),
      },
    ],
  },
  test: {
    environment: 'happy-dom',
    globals: true,
    include: ['src/**/*.{test,spec}.{ts,tsx}'],
  },
})
