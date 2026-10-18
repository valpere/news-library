#!/usr/bin/env node
// Explicit start/stop/status of the local news-library site (user-requested only).
import { spawn } from 'node:child_process';
import { closeSync, existsSync, mkdirSync, openSync, readFileSync, readdirSync, readlinkSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const DEFAULTS = { state: join(ROOT, 'tmp', 'site'), binary: join(ROOT, 'build', 'news-library') };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

export function bindAddress(envPath = join(ROOT, '.env')) {
  let bind = '127.0.0.1:8787';
  if (!existsSync(envPath)) return bind;
  for (let line of readFileSync(envPath, 'utf8').split('\n')) {
    line = line.trim().replace(/^export\s+/, '');
    const i = line.indexOf('=');
    if (i > 0 && line.slice(0, i).trim() === 'NEWS_LIBRARY_BIND') {
      bind = line.slice(i + 1).trim().replace(/^(['"])(.*)\1$/, '$2');
    }
  }
  return bind;
}

function readPid(state) {
  const n = Number.parseInt(readFileSync(join(state, 'pid'), 'utf8'), 10);
  return Number.isInteger(n) && n > 0 ? n : null;
}

// True if pid runs our binary. After a rebuild /proc/<pid>/exe ends with
// " (deleted)"; the process is still ours.
function isOurs(pid, binary) {
  try {
    const exe = readlinkSync(`/proc/${pid}/exe`).replace(/ \(deleted\)$/, '');
    return exe === binary || exe === realpathSync(binary, { throws: false }) || exe === resolve(binary);
  } catch {
    return false;
  }
}

function scanProc(binary) {
  return readdirSync('/proc').filter((n) => /^\d+$/.test(n)).map(Number).filter((pid) => isOurs(pid, binary));
}

// Pid of the running site: the pid file if it is valid, else a /proc scan
// (a lost or stale pid file must not hide a running site).
export function status({ state = DEFAULTS.state, binary = DEFAULTS.binary } = {}) {
  let pid = null;
  try { pid = readPid(state); } catch { /* no pid file */ }
  if (pid && isOurs(pid, binary)) return pid;
  return scanProc(binary)[0] ?? null;
}

export async function start(extra, { state = DEFAULTS.state, binary = DEFAULTS.binary, settleMs = 1500 } = {}) {
  if (status({ state, binary })) return 'already running';
  if (!existsSync(binary)) return 'binary missing: run make build';
  mkdirSync(state, { recursive: true });
  const logPath = join(state, 'agent.log');
  const fd = openSync(logPath, 'a');
  const child = spawn(binary, extra, { cwd: ROOT, stdio: ['ignore', fd, fd], detached: true });
  closeSync(fd);
  let exitCode = null;
  child.on('exit', (code, sig) => { exitCode = code ?? sig; });
  child.unref();
  await sleep(settleMs);
  if (exitCode !== null) {
    rmSync(join(state, 'pid'), { force: true });
    const tail = readFileSync(logPath, 'utf8').trim().split('\n').slice(-3).join(' | ');
    return `exited at once (${exitCode}); see ${logPath}: ${tail}`;
  }
  writeFileSync(join(state, 'pid'), `${child.pid}\n`);
  return `started pid ${child.pid}`;
}

export async function stop({ state = DEFAULTS.state, binary = DEFAULTS.binary, timeoutMs = 30000 } = {}) {
  const pid = status({ state, binary });
  if (!pid) return 'not running';
  process.kill(pid, 'SIGTERM'); // graceful only; never SIGKILL
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (!isOurs(pid, binary)) {
      rmSync(join(state, 'pid'), { force: true });
      return `stopped pid ${pid}`;
    }
    await sleep(200);
  }
  return `pid ${pid} still running after ${timeoutMs / 1000}s; not killed`;
}

async function main() {
  const { positionals, values } = parseArgs({
    allowPositionals: true,
    options: { 'resume-restored': { type: 'boolean', default: false } },
  });
  const action = positionals[0];
  let msg;
  if (action === 'start') msg = await start(values['resume-restored'] ? ['-resume-restored'] : []);
  else if (action === 'stop') msg = await stop();
  else if (action === 'status') {
    const pid = status();
    msg = pid ? `running pid ${pid}, bind ${bindAddress()}` : 'not running';
  } else {
    console.error('usage: site_ctl.mjs status|start [--resume-restored]|stop');
    return 2;
  }
  console.log(msg);
  return /^(binary missing|exited at once)|still running/.test(msg) ? 1 : 0;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exit(await main());
}
