// prepack: ship the repo's own install.sh / install.cmd inside the package, so
// npm installs exactly what the curl one-liners install.
'use strict';
const fs = require('fs');
const path = require('path');

const root = path.join(__dirname, '..');
for (const name of ['install.sh', 'install.cmd']) {
  fs.copyFileSync(path.join(root, '..', name), path.join(root, name));
}
