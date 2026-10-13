// postinstall: fetch the binary now. Never fails the npm install -- if this is
// skipped (--ignore-scripts, pnpm, bun) or fails, bin/aos.js retries on first run.
'use strict';
const fs = require('fs');
const { install, binaryPath } = require('./install');

if (!fs.existsSync(binaryPath)) install();
