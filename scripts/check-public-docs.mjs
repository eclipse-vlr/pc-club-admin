import { execFileSync } from 'node:child_process';

// Only the public demo linked from README belongs in the source repository.
const publicDocs = new Set(['docs/demo.md']);
const tracked = execFileSync('git', ['ls-files', '-z', '--', 'docs'], { encoding: 'utf8' }).split('\0').filter(Boolean);
const forbidden = tracked.filter(file => !publicDocs.has(file));
if (forbidden.length) {
  console.error('Private documentation must stay outside the source repository:');
  for (const file of forbidden) console.error(JSON.stringify(file));
  process.exitCode = 1;
} else {
  console.log('Documentation boundary passed: only the public demo is allowed.');
}
