#!/usr/bin/env node
/**
 * Generates a bundled TypeScript icon module from the locally cloned
 * a-file-icon-vscode + iconGenerator repos.
 *
 * Prerequisites:
 *   git clone --depth 1 https://github.com/AtomMaterialUI/a-file-icon-vscode.git ../a-file-icon-vscode
 *   git clone --depth 1 https://github.com/mallowigi/iconGenerator.git ../iconGenerator
 */

const fs = require('fs');
const path = require('path');

const REPO_A = path.resolve(__dirname, '..', '..', '..', 'a-file-icon-vscode');
const REPO_ICONS = path.resolve(__dirname, '..', '..', '..', 'iconGenerator');
const OUT_DIR = path.join(__dirname, '..', 'src', 'ui', 'panels', 'atom-icons');

/** Parse TS mapping files to extract { name, fileExtensions, fileNames } entries */
function parseMappingEntries(tsText) {
  const entries = [];
  const re = /\{[^}]*?name:\s*['"]([^'"]+)['"][^}]*?\}/gs;
  let m;
  while ((m = re.exec(tsText)) !== null) {
    const block = m[0];
    const name = m[1];
    const exts = [];
    const names = [];

    const extMatch = block.match(/fileExtensions:\s*\[([^\]]*)\]/);
    if (extMatch) {
      extMatch[1].split(',').forEach(e => {
        const v = e.trim().replace(/['"]/g, '');
        if (v) exts.push(v);
      });
    }
    const nameMatch = block.match(/fileNames:\s*\[([^\]]*)\]/);
    if (nameMatch) {
      nameMatch[1].split(',').forEach(e => {
        const v = e.trim().replace(/['"]/g, '');
        if (v) names.push(v);
      });
    }
    entries.push({ name, fileExtensions: exts, fileNames: names });
  }
  return entries;
}

function parseFolderEntries(tsText) {
  const entries = [];
  // { folderNames: ['...'], name: '...' }
  const re = /\{[^}]*?folderNames:\s*\[([^\]]*)\][^}]*?name:\s*['"]([^'"]+)['"][^}]*?\}/gs;
  let m;
  while ((m = re.exec(tsText)) !== null) {
    const folderNames = [];
    m[1].split(',').forEach(e => {
      const v = e.trim().replace(/['"]/g, '');
      if (v) folderNames.push(v);
    });
    entries.push({ name: m[2], folderNames });
  }
  const re2 = /\{[^}]*?name:\s*['"]([^'"]+)['"][^}]*?folderNames:\s*\[([^\]]*)\][^}]*?\}/gs;
  while ((m = re2.exec(tsText)) !== null) {
    const folderNames = [];
    m[2].split(',').forEach(e => {
      const v = e.trim().replace(/['"]/g, '');
      if (v) folderNames.push(v);
    });
    entries.push({ name: m[1], folderNames });
  }
  return entries;
}

function readSvg(dir, name) {
  const p = path.join(dir, name + '.svg');
  if (!fs.existsSync(p)) return null;
  const svg = fs.readFileSync(p, 'utf8');
  const viewBoxMatch = svg.match(/viewBox=["']([^"']+)["']/);
  const viewBox = viewBoxMatch ? viewBoxMatch[1] : '0 0 24 24';
  const inner = svg.replace(/<\?xml[^>]*\?>/, '').replace(/<svg[^>]*>/, '').replace(/<\/svg>/, '').trim();
  return { viewBox, inner };
}

function main() {
  const letters = 'abcdefghijklmnopqrstuvwxyz'.split('');
  const fileSpecial = ['archive','audio','binaries','config','custom','images','languages','least','numbers','tests','video'];

  // ── Parse file mappings ──
  console.log('Parsing file mappings...');
  const fileEntries = [];
  for (const f of [...letters, ...fileSpecial]) {
    const p = path.join(REPO_A, 'src', 'icons', 'files', `${f}.ts`);
    if (fs.existsSync(p)) {
      fileEntries.push(...parseMappingEntries(fs.readFileSync(p, 'utf8')));
    }
  }
  console.log(`  ${fileEntries.length} file icon entries`);

  // ── Parse folder mappings ──
  console.log('Parsing folder mappings...');
  const folderEntries = [];
  for (const f of letters) {
    const p = path.join(REPO_A, 'src', 'icons', 'folders', `${f}.ts`);
    if (fs.existsSync(p)) {
      folderEntries.push(...parseFolderEntries(fs.readFileSync(p, 'utf8')));
    }
  }
  console.log(`  ${folderEntries.length} folder icon entries`);

  // ── Collect unique icon names ──
  const fileIconNames = [...new Set(fileEntries.map(e => e.name))];
  const folderIconNames = [...new Set(folderEntries.map(e => e.name))];
  console.log(`  ${fileIconNames.length} unique file icons, ${folderIconNames.length} unique folder icons`);

  // ── Read file SVGs ──
  console.log('Reading file SVGs...');
  const filesDir = path.join(REPO_ICONS, 'assets', 'icons', 'files');
  const foldersDir = path.join(REPO_ICONS, 'assets', 'icons', 'folders');
  const foldersOpenDir = path.join(REPO_ICONS, 'assets', 'icons', 'foldersOpen');

  const fileSvgs = {};
  for (const name of fileIconNames) {
    const svg = readSvg(filesDir, name);
    if (svg) fileSvgs[name] = svg;
  }
  // Default file icon
  const defaultFileSvg = readSvg(filesDir, 'file');
  if (defaultFileSvg) fileSvgs['file'] = defaultFileSvg;
  console.log(`  ${Object.keys(fileSvgs).length}/${fileIconNames.length} file SVGs`);

  // ── Read folder SVGs ──
  console.log('Reading folder SVGs...');
  const folderSvgs = {};
  const folderOpenSvgs = {};

  // Default folder
  const df = readSvg(foldersDir, 'folder');
  if (df) folderSvgs['folder'] = df;
  const dfo = readSvg(foldersOpenDir, 'folder');
  if (dfo) folderOpenSvgs['folder'] = dfo;

  for (const name of folderIconNames) {
    const svg = readSvg(foldersDir, name);
    if (svg) folderSvgs[name] = svg;
    const svgOpen = readSvg(foldersOpenDir, name);
    if (svgOpen) folderOpenSvgs[name] = svgOpen;
  }
  console.log(`  ${Object.keys(folderSvgs).length} folder SVGs, ${Object.keys(folderOpenSvgs).length} folder-open SVGs`);

  // ── Build maps ──
  const extMap = {};
  const nameMap = {};
  for (const entry of fileEntries) {
    for (const ext of entry.fileExtensions) {
      extMap[ext.toLowerCase()] = entry.name;
    }
    for (const fn of entry.fileNames) {
      nameMap[fn.toLowerCase()] = entry.name;
    }
  }

  const folderNameMap = {};
  for (const entry of folderEntries) {
    for (const fn of entry.folderNames) {
      folderNameMap[fn.toLowerCase()] = entry.name;
    }
  }

  // ── Generate ──
  console.log('Generating module...');
  const output = `// AUTO-GENERATED by scripts/download-atom-icons.cjs
// Source: https://github.com/AtomMaterialUI/a-file-icon-vscode
// Icons: https://github.com/mallowigi/iconGenerator
// License: MIT (see upstream repos for copyright; each SVG embeds the notice)
// DO NOT EDIT MANUALLY

export const atomFileIcons: Record<string, { viewBox: string; inner: string }> = ${JSON.stringify(fileSvgs)};
export const atomFolderIcons: Record<string, { viewBox: string; inner: string }> = ${JSON.stringify(folderSvgs)};
export const atomFolderOpenIcons: Record<string, { viewBox: string; inner: string }> = ${JSON.stringify(folderOpenSvgs)};
export const atomExtMap: Record<string, string> = ${JSON.stringify(extMap)};
export const atomNameMap: Record<string, string> = ${JSON.stringify(nameMap)};
export const atomFolderNameMap: Record<string, string> = ${JSON.stringify(folderNameMap)};
`;

  fs.mkdirSync(OUT_DIR, { recursive: true });
  fs.writeFileSync(path.join(OUT_DIR, 'iconData.ts'), output);
  console.log(`Done! Wrote ${path.relative(process.cwd(), path.join(OUT_DIR, 'iconData.ts'))}`);
  console.log(`  ${Object.keys(fileSvgs).length} file icons, ${Object.keys(folderSvgs).length} folder icons`);
  console.log(`  ${Object.keys(extMap).length} ext mappings, ${Object.keys(nameMap).length} name mappings, ${Object.keys(folderNameMap).length} folder mappings`);
}

main();
