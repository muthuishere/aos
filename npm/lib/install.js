// Installs the aos binary INTO this package (vendor/), using the same
// install.sh / install.cmd the curl one-liners use, so `npm uninstall`
// removes it and nothing is put on the global PATH.
'use strict';
const fs = require('fs');
const path = require('path');
const { spawnSync } = require('child_process');

const root = path.join(__dirname, '..');
const win = process.platform === 'win32';
const vendorDir = path.join(root, 'vendor');
const binaryPath = path.join(vendorDir, win ? 'aos.exe' : 'aos');
const version = require('../package.json').version;

const MANUAL = win
  ? 'curl -fsSL -o install.cmd https://raw.githubusercontent.com/muthuishere/aos/main/install.cmd && install.cmd'
  : 'curl -fsSL https://raw.githubusercontent.com/muthuishere/aos/main/install.sh | sh';

function supported() {
  return ['darwin', 'linux', 'win32'].includes(process.platform) &&
    ['x64', 'arm64'].includes(process.arch);
}

// install() -> true when the binary is in place. Output goes to stderr so a
// first-run install never pollutes the command's stdout.
function install() {
  if (!supported()) {
    fail(`aos has no build for ${process.platform}/${process.arch}.`);
    return false;
  }
  const env = Object.assign({}, process.env, {
    AOS_INSTALL_DIR: vendorDir,
    AOS_NO_PATH: '1',
  });
  // 0.0.0 is the unpublished in-repo version: take the latest release.
  if (version && version !== '0.0.0' && !env.AOS_VERSION) env.AOS_VERSION = 'v' + version;
  // npm's proxy config reaches scripts as npm_config_*; curl and PowerShell read
  // the conventional variables.
  const proxy = env.npm_config_https_proxy || env.npm_config_proxy;
  if (proxy) {
    if (!env.HTTPS_PROXY && !env.https_proxy) env.HTTPS_PROXY = proxy;
    if (!env.HTTP_PROXY && !env.http_proxy) env.HTTP_PROXY = env.npm_config_proxy || proxy;
  }

  const r = win
    ? spawnSync(env.ComSpec || 'cmd.exe', ['/d', '/c', path.join(root, 'install.cmd')], { env, stdio: ['ignore', 2, 2] })
    : spawnSync('sh', [path.join(root, 'install.sh')], { env, stdio: ['ignore', 2, 2] });

  if (r.error || r.status !== 0 || !fs.existsSync(binaryPath)) {
    fail(r.error ? r.error.message : `the installer exited with ${r.status}.`, env.AOS_VERSION);
    return false;
  }
  return true;
}

function fail(why, wanted) {
  const v = wanted ? ` (version ${wanted})` : '';
  process.stderr.write(
    `\n@muthuishere/aos: could not install the aos binary${v}: ${why}\n` +
    'Behind a proxy? Set HTTPS_PROXY (or npm config set https-proxy). Or install it manually:\n' +
    `    ${MANUAL}\n\n`);
}

module.exports = { install, binaryPath };
