import assert from 'node:assert/strict';
import { copyFileSync, mkdtempSync, rmSync, writeFileSync, mkdirSync, existsSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { execFileSync } from 'node:child_process';
import { bindAddress, start, status, stop } from './site_ctl.mjs';

function fixture(t) {
  const dir = mkdtempSync(join(tmpdir(), 'site-ctl-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const binary = join(dir, 'srv');
  copyFileSync(execFileSync('which', ['sleep']).toString().trim(), binary);
  return { dir, binary, state: join(dir, 'state') };
}

test('start/stop cycle', async (t) => {
  const f = fixture(t);
  assert.match(await start(['300'], f), /^started pid \d+$/);
  assert.ok(status(f));
  assert.equal(await start(['300'], f), 'already running');
  assert.match(await stop(f), /^stopped/);
  assert.equal(status(f), null);
});

test('foreign pid in pid file is not ours', (t) => {
  const f = fixture(t);
  mkdirSync(f.state);
  writeFileSync(join(f.state, 'pid'), `${process.pid}\n`);
  assert.equal(status(f), null);
});

test('missing binary', async (t) => {
  const f = fixture(t);
  assert.match(await start([], { ...f, binary: join(f.dir, 'none') }), /binary missing/);
});

test('finds a running process without a pid file', async (t) => {
  const f = fixture(t);
  await start(['300'], f);
  rmSync(join(f.state, 'pid'));
  assert.ok(status(f));
  assert.match(await stop(f), /^stopped/);
});

test('finds a process whose binary was replaced after start', async (t) => {
  const f = fixture(t);
  await start(['300'], f);
  const pid = status(f);
  rmSync(f.binary); // like make build replacing the file: exe becomes "(deleted)"
  writeFileSync(f.binary, 'new');
  assert.equal(status(f), pid);
  assert.match(await stop(f), /^stopped/);
});

test('a start that exits at once reports failure and leaves no pid file', async (t) => {
  const f = fixture(t);
  const msg = await start(['not-a-number'], f); // sleep exits with an error
  assert.match(msg, /^exited at once/);
  assert.equal(existsSync(join(f.state, 'pid')), false);
  assert.equal(status(f), null);
});

test('bindAddress reads the env file', (t) => {
  const f = fixture(t);
  const env = join(f.dir, '.env');
  writeFileSync(env, "# c\nNEWS_LIBRARY_BIND='127.0.0.1:9999'\n");
  assert.equal(bindAddress(env), '127.0.0.1:9999');
  assert.equal(bindAddress(join(f.dir, 'missing')), '127.0.0.1:8787');
  assert.ok(readFileSync(env));
});
