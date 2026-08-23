import {copyFile, mkdir} from 'node:fs/promises';

const source = new URL('../../docs/examples/repository-hardening.yaml', import.meta.url);
const destination = new URL('../dist/examples/repository-hardening.yaml', import.meta.url);

await mkdir(new URL('../dist/examples/', import.meta.url), {recursive: true});
await copyFile(source, destination);

