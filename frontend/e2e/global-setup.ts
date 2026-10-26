import { execFileSync, spawn, type ChildProcess } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { createWriteStream, existsSync, mkdirSync, mkdtempSync, readdirSync, rmSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { API_PORT, API_URL } from './support/stack';

const BACKEND_DIR = resolve(__dirname, '..', '..', 'backend');
// A stable binary path keeps Windows Firewall and Defender from prompting on every run.
const BIN_DIR = resolve(__dirname, '.cache');
const BINARY = join(BIN_DIR, process.platform === 'win32' ? 'nestwall-api.exe' : 'nestwall-api');
const READY_TIMEOUT_MS = 60_000;

/**
 * Starts a throwaway API on API_PORT with its own SQLCipher database in a temp
 * directory. The live Docker stack and its database are never touched.
 * The returned function stops the API and deletes the temp directory.
 */
export default async function globalSetup(): Promise<() => Promise<void>> {
  const workDir = mkdtempSync(join(tmpdir(), 'nestwall-e2e-'));
  let api: ChildProcess | undefined;

  const stop = async (): Promise<void> => {
    if (api && api.exitCode === null && api.signalCode === null) {
      const exited = new Promise<void>((done) => api?.once('exit', () => done()));
      api.kill();
      await exited;
    }
    rmSync(workDir, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 });
  };

  try {
    if (backendChangedSinceBuild()) {
      mkdirSync(BIN_DIR, { recursive: true });
      execFileSync('go', ['build', '-o', BINARY, './cmd/api'], {
        cwd: BACKEND_DIR,
        env: { ...process.env, CGO_ENABLED: '1' },
        stdio: 'inherit',
      });
    }

    let output = '';
    const child = spawn(BINARY, [], {
      cwd: workDir,
      env: {
        ...process.env,
        API_PORT: String(API_PORT),
        DB_PATH: join(workDir, 'nestwall-e2e.db'),
        DB_KEY: randomBytes(32).toString('hex'),
        SESSION_SECRET: randomBytes(32).toString('hex'),
      },
      stdio: ['ignore', 'pipe', 'pipe'],
      windowsHide: true,
    });
    api = child;
    // Opt-in: E2E_API_LOG=../logs/e2e-api.log keeps the full API output for a log scan.
    const logFile = process.env.E2E_API_LOG ? createWriteStream(process.env.E2E_API_LOG) : undefined;
    const collect = (chunk: Buffer): void => {
      output = (output + chunk.toString()).slice(-4000);
      logFile?.write(chunk);
    };
    child.stdout?.on('data', collect);
    child.stderr?.on('data', collect);
    child.once('exit', () => logFile?.end());

    await waitForReady(child, () => output);
    return stop;
  } catch (err) {
    await stop();
    throw err;
  }
}

function backendChangedSinceBuild(): boolean {
  if (!existsSync(BINARY)) {
    return true;
  }
  const built = statSync(BINARY).mtimeMs;
  const newest = readdirSync(BACKEND_DIR, { recursive: true, withFileTypes: true })
    .filter((entry) => entry.isFile() && (entry.name.endsWith('.go') || entry.name === 'go.mod' || entry.name === 'go.sum'))
    .reduce((max, entry) => Math.max(max, statSync(join(entry.parentPath, entry.name)).mtimeMs), 0);
  return newest > built;
}

async function waitForReady(child: ChildProcess, stderr: () => string): Promise<void> {
  const deadline = Date.now() + READY_TIMEOUT_MS;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) {
      throw new Error(`E2E API exited early (${child.exitCode}): ${stderr()}`);
    }
    try {
      const res = await fetch(`${API_URL}/api/v1/health/ready`);
      if (res.ok) {
        return;
      }
    } catch {
      // Not listening yet.
    }
    await new Promise((done) => setTimeout(done, 250));
  }
  throw new Error(`E2E API not ready on ${API_URL} after ${READY_TIMEOUT_MS} ms: ${stderr()}`);
}
