import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';

const check = fileURLToPath(new URL('./check-public-docs.mjs', import.meta.url));

function repository(t, files) {
  const directory = mkdtempSync(path.join(os.tmpdir(), 'pcclub-public-docs-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  execFileSync('git', ['init', '--quiet', directory]);
  for (const file of files) {
    const target = path.join(directory, file);
    mkdirSync(path.dirname(target), { recursive: true });
    writeFileSync(target, 'Synthetic documentation fixture.\n');
    execFileSync('git', ['-C', directory, 'add', '--', file]);
  }
  return spawnSync(process.execPath, [check], { cwd: directory, encoding: 'utf8' });
}

test('an empty documentation directory passes', t => {
  const result = repository(t, []);
  assert.equal(result.status, 0, result.stderr);
});

test('the public demo linked from README passes', t => {
  const result = repository(t, ['docs/demo.md']);
  assert.equal(result.status, 0, result.stderr);
});

test('private documents are rejected even alongside the allowed demo', t => {
  const result = repository(t, ['docs/demo.md', 'docs/development.md', 'docs/demo draft.md']);
  assert.equal(result.status, 1);
  assert.match(result.stderr, /docs\/development\.md/);
  assert.match(result.stderr, /docs\/demo draft\.md/);
});
