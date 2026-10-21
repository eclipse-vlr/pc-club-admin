import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm, stat } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { createStagingEnv } from './init-staging.mjs';

const directory = await mkdtemp(path.join(os.tmpdir(), 'pcclub-staging-config-'));
try {
  const file = path.join(directory, '.env.staging');
  await createStagingEnv(file, 'a'.repeat(40));
  const original = await readFile(file, 'utf8');
  const values = Object.fromEntries(original.split('\n').filter(line => line && !line.startsWith('#')).map(line => line.split('=')));
  assert.equal(values.STAGING_IMAGE, 'pc-club-admin:' + 'a'.repeat(40));
  assert.match(values.STAGING_DB_PASSWORD, /^[0-9a-f]{64}$/);
  assert.match(values.STAGING_PIN_PEPPER, /^[0-9a-f]{64}$/);
  assert.notEqual(values.STAGING_DB_PASSWORD, values.STAGING_PIN_PEPPER);
  assert.equal(values.STAGING_HTTP_PORT, '18080');
  if (process.platform !== 'win32') assert.equal((await stat(file)).mode & 0o777, 0o600);
  await assert.rejects(createStagingEnv(file, 'b'.repeat(40)), { code: 'EEXIST' });
  assert.equal(await readFile(file, 'utf8'), original, 'Existing secrets must remain byte-identical');
  await assert.rejects(createStagingEnv(path.join(directory, 'invalid'), 'main'), /INVALID_REVISION/);
  assert.equal(await readFile(file, 'utf8'), original);
  console.log('Staging config checks passed: independent secrets, private file, revision tag and no overwrite.');
} finally {
  await rm(directory, { recursive: true, force: true });
}
