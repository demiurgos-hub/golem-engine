import { spawn } from 'node:child_process';
import { delimiter, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const commands = {
  generate: ['run', '-mod=mod', 'github.com/demiurgos-hub/golem-engine/cmd/golem-bake'],
  server: ['run', './cmd/server'],
  test: ['test', './...'],
};
const args = commands[process.argv[2]];
if (!args) throw new Error('Expected generate, server, or test');

// The sample is its own module; the optional engine workspace must not mask it.
const pathKey = Object.keys(process.env).find((key) => key.toLowerCase() === 'path') ?? 'PATH';
const child = spawn('go', args, {
  cwd: root,
  stdio: 'inherit',
  env: {
    ...process.env,
    GOWORK: 'off',
    [pathKey]: `${resolve(root, 'node_modules/.bin')}${delimiter}${process.env[pathKey] ?? ''}`,
  },
});
child.on('error', (error) => { console.error(error); process.exitCode = 1; });
child.on('exit', (code) => { process.exitCode = code ?? 1; });
for (const signal of ['SIGINT', 'SIGTERM']) {
  process.on(signal, () => child.kill(signal));
}
