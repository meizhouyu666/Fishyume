// Build the dsh-fishyume plugin artifacts:
//   lib/plugin.js  — host entry (ESM, @deepseek-ai/* external)
//   lib/client.js  — browser client entry wrapped in the DSH __ModuleLoader__
//                    client-bundle protocol (CJS closure-factory).
//
// The standalone sidecar (dist/server.js + dist/public) is produced by
// build.mjs and is unaffected.
import {build} from 'esbuild';
import {fileURLToPath} from 'node:url';
import {readFileSync} from 'node:fs';

const root = new URL('../', import.meta.url);
const packageName = JSON.parse(readFileSync(new URL('package.json', root), 'utf8')).name;

// Host entry: bundle fishyume's own code (gateway/security/engine bridge), keep
// @deepseek-ai/* and node builtins external so they resolve from the profile's
// node_modules at runtime.
await build({
  entryPoints: [fileURLToPath(new URL('../src/plugin.ts', import.meta.url))],
  outfile: fileURLToPath(new URL('../lib/plugin.js', import.meta.url)),
  bundle: true,
  platform: 'node',
  format: 'esm',
  target: 'node24',
  sourcemap: true,
  external: ['@deepseek-ai/*'],
});

// Client entry: the DSH client bundle must register itself under the package
// name via `window.__ModuleLoader__.load({ id, factory: (require) => ... })`.
// `react`/`react/jsx-runtime` are answered by the loader module table; every
// @deepseek-ai import in this file is type-only and erased, so there is no
// cross-plugin value import.
await build({
  entryPoints: [fileURLToPath(new URL('../src/client/plugin.tsx', import.meta.url))],
  outfile: fileURLToPath(new URL('../lib/client.js', import.meta.url)),
  bundle: true,
  platform: 'browser',
  format: 'cjs',
  target: ['es2022'],
  jsx: 'automatic',
  sourcemap: true,
  minify: true,
  external: ['react', 'react/jsx-runtime', 'react-dom', 'react-dom/client', '@deepseek-ai/*'],
  banner: {
    js: `window.__ModuleLoader__.load({ id: ${JSON.stringify(packageName)}, factory: (require) => {\nvar module = { exports: {} }; var exports = module.exports;`,
  },
  footer: {
    js: 'return module.exports; } });',
  },
});
