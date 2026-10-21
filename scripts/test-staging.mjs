import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtemp, rm } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createStagingEnv } from './init-staging.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
const directory = await mkdtemp(path.join(os.tmpdir(), 'pcclub-staging-check-'));
const file = path.join(directory, '.env.staging');
const project = 'pcclub-staging-check-' + randomUUID();
const image = process.env.PC_CLUB_TEST_IMAGE || 'pc-club-admin:local';
// Override only this disposable installation. Never read the user's staging configuration.
const environment = { ...process.env, STAGING_IMAGE: image, STAGING_HTTP_PORT: '0' };
const base = ['compose', '--env-file', file, '-f', 'deploy/compose.staging.yaml', '--project-name', project];

function run(args, input) {
  return new Promise((resolve, reject) => {
    const child = spawn('docker', args, { cwd: root, env: environment, stdio: ['pipe', 'pipe', 'pipe'], timeout: 120_000 });
    let output = '', error = '';
    child.stdout.on('data', chunk => output += chunk);
    child.stderr.on('data', chunk => error += chunk);
    child.once('error', reject);
    child.once('close', code => code === 0 ? resolve(output.trim()) : reject(new Error('Isolated staging command failed: ' + error)));
    child.stdin.on('error', () => {});
    child.stdin.end(input);
  });
}
const compose = (args, input) => run([...base, ...args], input);
const sql = query => compose(['exec', '-T', 'db', 'psql', '-U', 'pcclub_staging', '-d', 'pcclub_staging', '-At', '-v', 'ON_ERROR_STOP=1', '-c', query]);
async function probe(origin, route, wanted) {
  const response = await fetch(origin + route, { signal: AbortSignal.timeout(5000) });
  assert.equal(response.status, wanted, route);
  return response;
}

try {
  await createStagingEnv(file, 'a'.repeat(40));
  const config = JSON.parse(await compose(['config', '--format', 'json']));
  assert.equal(config.name, project);
  assert.equal(config.services.api.environment.APP_ENV, 'test');
  assert.equal(config.services.api.environment.CASH_POLICY_STATUS, 'unverified');
  assert.equal(config.services.api.environment.DRIVE_EXCEL_SYNC, 'disabled');
  assert.equal(config.services.api.environment.DRIVE_EXCEL_CREDENTIALS_FILE, undefined);
  assert.equal(config.services.api.read_only, true);
  assert.equal(config.services.api.ports.length, 1);
  assert.equal(config.services.api.ports[0].host_ip, '127.0.0.1');
  assert.equal(config.services.api.ports[0].target, 8080);
  assert.equal(config.services.db.ports, undefined);
  assert.equal(config.networks.backend.internal, true);
  assert.equal(config.services.api.stop_grace_period, '30s');
  assert.equal(config.services.api.logging.options['max-size'], '10m');
  const expectedImageID = await run(['image', 'inspect', '--format', '{{.Id}}', image]);
  await compose(['up', '-d', '--wait', '--wait-timeout', '90', 'db']);
  const databaseID = await compose(['ps', '-q', 'db']);
  await compose(['run', '--rm', '-T', 'migrate']);
  await compose(['run', '--rm', '-T', 'admin', 'bootstrap-manager', '--login', 'staging-manager', '--name', 'Synthetic staging manager', '--password-stdin'], 'synthetic-staging-manager-password\n');
  const managerID = await sql("SELECT id::text FROM users WHERE manager_login='staging-manager'");
  assert.match(managerID, /^[a-f0-9-]{36}$/);
  await compose(['up', '-d', '--wait', '--wait-timeout', '90', 'api']);
  const firstAPI = await compose(['ps', '-q', 'api']);
  const inspect = JSON.parse(await run(['inspect', firstAPI]))[0];
  assert.equal(inspect.Config.User, '10001:10001');
  assert.equal(inspect.HostConfig.ReadonlyRootfs, true);
  assert.equal(inspect.Image, expectedImageID);
  const bindings = inspect.HostConfig.PortBindings['8080/tcp'];
  assert.equal(bindings.length, 1);
  assert.equal(bindings[0].HostIp, '127.0.0.1');
  const address = await compose(['port', 'api', '8080']);
  assert.match(address, /^127\.0\.0\.1:\d+$/);
  const origin = 'http://' + address;
  await probe(origin, '/healthz', 200);
  await probe(origin, '/readyz', 200);
  assert.match(await (await probe(origin, '/', 200)).text(), /lang="ru"/);
  const status = await (await probe(origin, '/api/v1/status', 200)).json();
  assert.equal(status.environment, 'test');
  assert.equal(status.cash_policy_status, 'unverified');
  assert.equal(status.excel_sync_enabled, false);
  // A process remains alive during a database outage but must stop admitting work.
  await compose(['stop', 'db']);
  await probe(origin, '/healthz', 200);
  await probe(origin, '/readyz', 503);
  await compose(['up', '-d', '--wait', '--wait-timeout', '90', 'db']);
  await probe(origin, '/readyz', 200);
  assert.equal(await sql("SELECT id::text FROM users WHERE manager_login='staging-manager'"), managerID);
  // Recreate only the API; reuse the same database volume and retry migrations.
  await compose(['stop', 'api']);
  await compose(['run', '--rm', '-T', 'migrate']);
  await compose(['up', '-d', '--force-recreate', '--wait', '--wait-timeout', '90', 'api']);
  const nextAPI = await compose(['ps', '-q', 'api']);
  assert.notEqual(nextAPI, firstAPI);
  assert.equal(await compose(['ps', '-q', 'db']), databaseID);
  assert.equal(await sql("SELECT id::text FROM users WHERE manager_login='staging-manager'"), managerID);
  assert.equal(await sql('SELECT count(*) FROM users'), '1');
  const nextAddress = await compose(['port', 'api', '8080']);
  await probe('http://' + nextAddress, '/readyz', 200);
  assert.equal(await run(['inspect', '--format', '{{.Image}}', nextAPI]), expectedImageID);
  console.log('Staging smoke passed: real Compose, loopback-only API, migrations/bootstrap, readiness during DB outage, persistent data and API recreation with the same image.');
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
} finally {
  // Only this random project owns these disposable volumes; never reuse for a club installation.
  try { await compose(['down', '--volumes', '--remove-orphans']); } catch (error) { console.error(error.message); process.exitCode = 1; }
  await rm(directory, { recursive: true, force: true });
}
