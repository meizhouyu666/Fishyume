import {cpSync, mkdirSync, rmSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {build} from 'esbuild';

const root = new URL('../', import.meta.url);
const dist = new URL('../dist/', import.meta.url);
rmSync(dist, {recursive: true, force: true});
mkdirSync(new URL('public/', dist), {recursive: true});

await Promise.all([
  build({
    entryPoints: [fileURLToPath(new URL('../src/server.ts', import.meta.url))],
    outfile: fileURLToPath(new URL('server.js', dist)),
    bundle: true,
    platform: 'node',
    format: 'esm',
    target: 'node24',
    sourcemap: true,
    banner: {js: '#!/usr/bin/env node'},
  }),
  build({
    entryPoints: [fileURLToPath(new URL('../src/client/main.ts', import.meta.url))],
    outfile: fileURLToPath(new URL('public/app.js', dist)),
    bundle: true,
    platform: 'browser',
    format: 'esm',
    target: ['es2022'],
    sourcemap: true,
    minify: true,
  }),
]);

cpSync(new URL('../src/client/index.html', import.meta.url), new URL('public/index.html', dist));
cpSync(new URL('../src/client/styles.css', import.meta.url), new URL('public/styles.css', dist));
void root;
