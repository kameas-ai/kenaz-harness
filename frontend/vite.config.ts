import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
import path from 'node:path';

// Two-CSP build-time substitution. Production CSP forbids CDNs and disables
// connect-src; dev CSP is relaxed to permit Vite HMR.
//
// Privacy CI invariant #1 (plan §4.3) is enforced post-build by
// scripts/ci/check-csp.sh against the produced dist/index.html.
const PROD_CSP =
  "default-src 'none'; connect-src 'none'; script-src 'self'; " +
  "style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
  "font-src 'self'; base-uri 'none'; form-action 'none'; " +
  "frame-ancestors 'none'; object-src 'none'";

const DEV_CSP =
  "default-src 'none'; connect-src 'self' ws://localhost:* http://localhost:*; " +
  "script-src 'self' 'unsafe-eval' 'unsafe-inline'; " +
  "style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
  "font-src 'self' data:; base-uri 'none'; form-action 'none'; " +
  "frame-ancestors 'none'; object-src 'none'";

export default defineConfig(({ command }) => ({
  plugins: [
    vue(),
    {
      name: 'inject-csp',
      transformIndexHtml(html) {
        const csp = command === 'build' ? PROD_CSP : DEV_CSP;
        return html.replace('__CSP_PLACEHOLDER__', csp);
      },
    },
  ],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, 'src'),
    },
  },
  build: {
    target: 'es2022',
    sourcemap: false,
    rollupOptions: {
      output: {
        // Predictable chunk names for CSP / bundle-size CI.
        entryFileNames: 'assets/[name]-[hash].js',
        chunkFileNames: 'assets/[name]-[hash].js',
        assetFileNames: 'assets/[name]-[hash][extname]',
      },
    },
  },
  server: {
    port: 5173,
    strictPort: false,
  },
}));
