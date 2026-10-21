import { randomBytes } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { open } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));

export async function createStagingEnv(file, revision) {
  if (!/^[0-9a-f]{40}$/.test(revision)) throw new Error('INVALID_REVISION');
  const text = [
    '# Isolated test deployment; keep this file private and outside Git.',
    'STAGING_IMAGE=pc-club-admin:' + revision,
    'STAGING_DB_PASSWORD=' + randomBytes(32).toString('hex'),
    'STAGING_PIN_PEPPER=' + randomBytes(32).toString('hex'),
    'STAGING_TIMEZONE=Europe/Moscow',
    'STAGING_HTTP_PORT=18080',
    '',
  ].join('\n');
  // Exclusive creation: existing configuration and its original pepper are never read or replaced.
  const handle = await open(file, 'wx', 0o600);
  try {
    await handle.writeFile(text, 'utf8');
    await handle.sync();
  } finally {
    await handle.close();
  }
}

if (process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url) {
  try {
    const args = process.argv.slice(2);
    if (args.length !== 0 && (args.length !== 2 || args[0] !== '--output')) throw new Error('INVALID_ARGUMENTS');
    const file = args.length ? path.resolve(args[1]) : path.join(root, 'deploy/.env.staging');
    const revision = execFileSync('git', ['rev-parse', '--verify', 'HEAD'], { cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
    const changes = execFileSync('git', ['status', '--porcelain', '--untracked-files=normal'], { cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
    if (changes.trim()) throw new Error('DIRTY_WORKTREE');
    await createStagingEnv(file, revision);
    console.log('Настройки отдельного тестового контура созданы: ' + file);
    console.log('Образ для сборки: pc-club-admin:' + revision);
    console.log('Секреты не выводятся. Сохраните файл; повторное создание запрещено.');
  } catch (error) {
    console.error(error.code === 'EEXIST'
      ? 'Файл уже существует и сохранён без изменений. Не создавайте новый pepper поверх действующей базы.'
      : error.message === 'DIRTY_WORKTREE'
        ? 'Есть локальные изменения. Сохраните их перед подготовкой релиза.'
        : 'Настройки не созданы. Проверьте Git, аргументы и права записи; при частичной ошибке сохраните файл для разбора.');
    process.exitCode = 1;
  }
}
