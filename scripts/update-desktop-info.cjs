#!/usr/bin/env node
// Updates productName and version in the Wails desktop build metadata files:
//   - build/config.yml  (Wails config "info" section)
//   - build/windows/info.json  (Windows version-info resource)
//   - build/windows/wails.exe.manifest  (assemblyIdentity version)
//
// Usage: node scripts/update-desktop-info.cjs <desktopAppDir> <version>
const fs = require('fs');
const path = require('path');

const appDir = process.argv[2];
const ver = process.argv[3];

if (!appDir || !ver) {
  console.error('Usage: node update-desktop-info.cjs <desktopAppDir> <version>');
  process.exit(1);
}

// 1. config.yml — replace productName and version in the "info:" section
const cfgPath = path.join(appDir, 'build', 'config.yml');
const cfgLines = fs.readFileSync(cfgPath, 'utf8').split(/\r?\n/);
let section = false;
for (let i = 0; i < cfgLines.length; i++) {
  if (cfgLines[i] === 'info:') section = true;
  else if (section && /^[^ ]/.test(cfgLines[i]) && cfgLines[i] !== '') section = false;
  if (section && /^  productName:/.test(cfgLines[i])) cfgLines[i] = '  productName: "sporemind"';
  if (section && /^  version:/.test(cfgLines[i])) cfgLines[i] = '  version: "' + ver + '"';
}
fs.writeFileSync(cfgPath, cfgLines.join('\n'));

// 2. info.json / info-dev.json — update version and pin ProductName so a
// previously polluted file can never leak the dev name into a prod build.
// prod build uses info.json (Taskfile generate:syso with PRODUCTION=true),
// dev mode uses info-dev.json.
function writeWindowsInfo(fileName, productName) {
  const infoPath = path.join(appDir, 'build', 'windows', fileName);
  const j = JSON.parse(fs.readFileSync(infoPath, 'utf8'));
  j.fixed.file_version = ver;
  j.info['0000'].ProductVersion = ver;
  j.info['0000'].ProductName = productName;
  j.info['0000'].FileDescription = productName;
  fs.writeFileSync(infoPath, JSON.stringify(j, null, '\t') + '\n');
}
writeWindowsInfo('info.json', 'sporemind');
writeWindowsInfo('info-dev.json', 'sporemind-dev');

// 3. wails.exe.manifest — update assemblyIdentity version
const manPath = path.join(appDir, 'build', 'windows', 'wails.exe.manifest');
let m = fs.readFileSync(manPath, 'utf8');
m = m.replace(
  /name="com\.qomos-w\.sporemind" version="[^"]*"/,
  'name="com.qomos-w.sporemind" version="' + ver + '.0"',
);
fs.writeFileSync(manPath, m);

console.log('[build-desktop-info] productName=sporemind productVersion=' + ver);