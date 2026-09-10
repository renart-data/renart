import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

/** Build-time URL for public media. Docs commands run from the docs package. */
export function publicMediaUrl(path) {
  const bytes = readFileSync(resolve('public', path.replace(/^\//, '')));
  const version = createHash('sha256').update(bytes).digest('hex').slice(0, 12);
  return `${path}?v=${version}`;
}
