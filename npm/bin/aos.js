#!/usr/bin/env node
// Shim: run the aos binary that lives in this package, passing args, stdio and
// the exit code straight through. Installs it first if postinstall never ran.
'use strict';
const fs = require('fs');
const { spawnSync } = require('child_process');
const { install, binaryPath } = require('../lib/install');

if (!fs.existsSync(binaryPath) && !install()) process.exit(1);

const r = spawnSync(binaryPath, process.argv.slice(2), { stdio: 'inherit' });
if (r.error) {
  process.stderr.write(`aos: ${r.error.message}\n`);
  process.exit(1);
}
if (r.signal) process.kill(process.pid, r.signal);
process.exit(r.status === null ? 1 : r.status);
