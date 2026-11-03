import assert from 'node:assert/strict';
import { test } from 'node:test';
import { parseArguments, validateEnvironment, validateVolume, validateCompose, seedDemo, apiClient, demo, project, origin } from './portfolio-demo.mjs';

const env = [
  'DEMO_ENVIRONMENT=portfolio-demo', 'STAGING_IMAGE=pc-club-admin:portfolio-demo',
  'STAGING_TIMEZONE=Europe/Moscow', 'STAGING_HTTP_PORT=18081',
  'STAGING_DB_PASSWORD=' + 'a'.repeat(64), 'STAGING_PIN_PEPPER=' + 'b'.repeat(64), '',
].join('\n');

test('reset requires the exact demo selection; other commands reject extra arguments', () => {
  assert.equal(parseArguments(['up']), 'up');
  assert.equal(parseArguments(['reset', '--environment', 'portfolio-demo']), 'reset');
  for (const args of [[], ['reset'], ['reset', '--environment', 'staging'], ['up', '--url', 'https://example.com'], ['reset', '--environment', 'portfolio-demo', '--force']]) assert.throws(() => parseArguments(args));
});

test('existing foreign, duplicated or changed configuration is refused', () => {
  assert.equal(validateEnvironment(env).DEMO_ENVIRONMENT, 'portfolio-demo');
  for (const content of [env.replace('portfolio-demo', 'production'), env.replace('18081', '8080'), env + 'STAGING_IMAGE=other\n', env + 'DATABASE_URL=external\n', env.replace('b'.repeat(64), 'short')]) assert.throws(() => validateEnvironment(content));
  assert.throws(() => apiClient('http://127.0.0.1:18080'));
  assert.throws(() => apiClient('https://example.com'));
});

test('volume deletion requires its exact name and both Compose ownership labels', () => {
  const volume = { Name: project + '_staging_postgres_data', Labels: { 'com.docker.compose.project': project, 'com.docker.compose.volume': 'staging_postgres_data' } };
  validateVolume(volume);
  assert.throws(() => validateVolume({ ...volume, Name: 'club_data' }));
  assert.throws(() => validateVolume({ ...volume, Labels: {} }));
  assert.throws(() => validateVolume({ ...volume, Labels: { ...volume.Labels, 'com.docker.compose.project': 'pc-club-staging' } }));
});

test('expanded Compose rejects external exposure, Drive, binds and foreign volumes', () => {
  const config = {
    name: project, services: {
      api: { image: 'pc-club-admin:portfolio-demo', environment: { APP_ENV: 'test', CASH_POLICY_STATUS: 'unverified', DRIVE_EXCEL_SYNC: 'disabled', APP_ORIGIN: origin }, ports: [{ host_ip: '127.0.0.1', published: '18081' }] },
      db: { environment: { POSTGRES_DB: 'pcclub_staging' } },
    }, volumes: { staging_postgres_data: { name: project + '_staging_postgres_data' } },
  };
  validateCompose(config);
  for (const change of [
    c => { c.services.api.ports[0].host_ip = '0.0.0.0'; },
    c => { c.services.api.environment.APP_ENV = 'production'; },
    c => { c.services.api.environment.DRIVE_EXCEL_SYNC = 'test'; },
    c => { c.services.db.ports = [{ published: '5432' }]; },
    c => { c.volumes.staging_postgres_data.name = 'club_data'; },
    c => { c.services.api.volumes = [{ type: 'bind', source: '/secrets' }]; },
  ]) { const unsafe = structuredClone(config); change(unsafe); assert.throws(() => validateCompose(unsafe)); }
});

test('seed resumes after partial setup and leaves the current shift untouched on repeat', async () => {
  const users = [{ id: 'first', display_name: demo.employees[0].name, roles: ['admin'], active: true }];
  let shift = null, created = 0, bootstrapped = 0, logouts = 0;
  const api = async (route, method = 'GET', body) => {
    if (route === '/status') return { environment: 'test', cash_policy_status: 'unverified', excel_sync_enabled: false };
    if (route === '/auth/manager-login') return {};
    if (route === '/auth/logout') { logouts++; return {}; }
    if (route === '/users' && method === 'GET') return [...users];
    if (route === '/users' && method === 'POST') { created++; const user = { id: 'second', display_name: body.display_name, roles: body.roles, active: true }; users.push(user); return user; }
    if (route === '/shifts/current') return { shift };
    if (route === '/shifts/bootstrap') { bootstrapped++; assert.equal(body.opening_cash_kopecks, 1000000); shift = { id: 'demo-shift' }; return shift; }
    throw new Error('unexpected call');
  };
  await seedDemo(api); await seedDemo(api);
  assert.equal(created, 1); assert.equal(bootstrapped, 1); assert.equal(logouts, 2);
});

test('seed refuses a non-test server before sending credentials', async () => {
  let calls = 0;
  await assert.rejects(seedDemo(async () => { calls++; return { environment: 'production' }; }));
  assert.equal(calls, 1);
});
