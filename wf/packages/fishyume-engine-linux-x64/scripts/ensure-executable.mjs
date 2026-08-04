import {chmodSync} from 'node:fs';

if (process.platform !== 'linux') {
  throw new Error('fishyume-engine-linux-x64 must be packed on Linux so executable mode is preserved');
}

chmodSync(new URL('../bin/fishyume-engine', import.meta.url), 0o755);
