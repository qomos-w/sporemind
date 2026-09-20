const fs = require('fs');
const path = require('path');
const { Resvg } = require('@resvg/resvg-js');
const sharp = require('sharp');

const ROOT = path.resolve(__dirname, '..');
const ASSETS = path.join(ROOT, 'assets');

function renderSvg(svgPath, width, height) {
  const svg = fs.readFileSync(svgPath, 'utf-8');
  return renderSvgContent(svg, width, height);
}

function renderSvgContent(svg, width, height) {
  const opts = {
    fitTo: { mode: 'width', value: width },
    crop: { left: 0, top: 0, right: 0, bottom: 0 },
  };
  const resvg = new Resvg(svg, opts);
  const { width: w, height: h } = resvg;

  // If height is specified and different from computed, adjust
  if (height && height !== h) {
    const viewBox = svg.match(/viewBox="([^"]+)"/);
    if (viewBox) {
      const [, vb] = viewBox;
      const [, , vw, vh] = vb.split(/\s+/).map(Number);
      // Scale to fit, center crop or pad
      const scale = Math.max(width / vw, height / vh);
      // For our icons, the content fills the viewBox, so just use the default
    }
  }

  const pngData = resvg.render();
  return pngData.asPng();
}

async function resizePng(inputBuffer, width, height) {
  return sharp(inputBuffer)
    .resize(width, height, { fit: 'cover', position: 'center' })
    .png()
    .toBuffer();
}

function makeIco(pngBuffers, sizes) {
  const count = pngBuffers.length;
  const header = Buffer.alloc(6);
  header.writeUInt16LE(0, 0); // Reserved
  header.writeUInt16LE(1, 2); // Type: icon
  header.writeUInt16LE(count, 4); // Count

  let offset = 6 + count * 16;
  const entries = [];
  const images = [];

  for (let i = 0; i < count; i++) {
    const buf = pngBuffers[i];
    const dim = sizes[i];
    const size = buf.length;

    const entry = Buffer.alloc(16);
    entry.writeUInt8(dim > 255 ? 0 : dim, 0); // Width (0 = 256)
    entry.writeUInt8(dim > 255 ? 0 : dim, 1); // Height (0 = 256)
    entry.writeUInt8(0, 2); // Colors
    entry.writeUInt8(0, 3); // Reserved
    entry.writeUInt16LE(1, 4); // Color planes
    entry.writeUInt16LE(32, 6); // Bits per pixel
    entry.writeUInt32LE(size, 8); // Image size
    entry.writeUInt32LE(offset, 12); // Offset
    entries.push(entry);
    images.push(buf);
    offset += size;
  }

  return Buffer.concat([header, ...entries, ...images]);
}

async function generateAndroidIcon(sourceBuffer, outDir, name, size) {
  const buf = await resizePng(sourceBuffer, size, size);
  const outPath = path.join(outDir, `${name}.png`);
  fs.mkdirSync(outDir, { recursive: true });
  fs.writeFileSync(outPath, buf);
  console.log(`  ${outPath}`);
}

async function generateAndroidRoundIcon(sourceBuffer, outDir, size) {
  // Resize source to target size as raw RGBA
  const { data, info } = await sharp(sourceBuffer)
    .resize(size, size, { fit: 'cover', position: 'center' })
    .raw()
    .ensureAlpha()
    .toBuffer({ resolveWithObject: true });

  // Build circular mask: 255 inside circle, 0 outside
  const mask = Buffer.alloc(size * size);
  const cx = size / 2;
  const cy = size / 2;
  const r = size / 2;
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      const dx = x - cx + 0.5;
      const dy = y - cy + 0.5;
      mask[y * size + x] = Math.sqrt(dx * dx + dy * dy) <= r ? 255 : 0;
    }
  }

  // Multiply alpha channel by mask
  for (let i = 0; i < size * size; i++) {
    data[i * 4 + 3] = Math.round((data[i * 4 + 3] * mask[i]) / 255);
  }

  const buf = await sharp(data, { raw: { width: size, height: size, channels: 4 } })
    .png()
    .toBuffer();

  const outPath = path.join(outDir, `ic_launcher_round.png`);
  fs.writeFileSync(outPath, buf);
  console.log(`  ${outPath}`);
}

async function main() {
  console.log('Generating sporemind icons...\n');

  // ---- Render master PNGs from SVG ----
  console.log('Rendering SVG to PNG...');
  const iconSvg = path.join(ASSETS, 'icon.svg');
  const foregroundSvg = path.join(ASSETS, 'icon-foreground.svg');

  const masterPng = renderSvg(iconSvg, 1024);
  const foregroundPng = renderSvg(foregroundSvg, 432);
  console.log(`  Master: ${masterPng.length} bytes`);
  console.log(`  Foreground: ${foregroundPng.length} bytes\n`);

  // Master SVG carries its own dark rounded tile (#1b1d17); Windows .ico and
  // legacy Android launcher icons get a dark rounded-square backdrop composited
  // underneath so the tile corners stay seamless.
  const backdropSvg = Buffer.from(
    '<svg xmlns="http://www.w3.org/2000/svg" width="1024" height="1024"><rect width="1024" height="1024" rx="180" fill="#1b1d17"/></svg>'
  );
  const backedPng = await sharp(backdropSvg)
    .composite([{ input: masterPng }])
    .png()
    .toBuffer();

  // ---- Wails Desktop ----
  console.log('Wails icons:');
  const wailsBuildDir = path.join(ROOT, 'cmd', 'sporemind-desktop', 'build');
  const wailsWinDir = path.join(wailsBuildDir, 'windows');

  // appicon.png (macOS / Linux)
  fs.mkdirSync(wailsBuildDir, { recursive: true });
  fs.writeFileSync(path.join(wailsBuildDir, 'appicon.png'), masterPng);
  console.log(`  ${path.join(wailsBuildDir, 'appicon.png')}`);

  // icon.ico (Windows) - multi-resolution, on light rounded backdrop
  const icoSizes = [256, 128, 64, 48, 32, 16];
  const icoBuffers = [];
  for (const s of icoSizes) {
    icoBuffers.push(await resizePng(backedPng, s, s));
  }
  const icoBuf = makeIco(icoBuffers, icoSizes);
  fs.mkdirSync(wailsWinDir, { recursive: true });
  fs.writeFileSync(path.join(wailsWinDir, 'icon.ico'), icoBuf);
  console.log(`  ${path.join(wailsWinDir, 'icon.ico')}`);

  // ---- Dev icon (normal icon + red DEV badge) ----
  console.log('\nDev icon:');
  const devBadge = `<g transform="translate(676 70)">
    <rect width="304" height="116" rx="18" fill="#e53935"/>
    <text x="152" y="84" text-anchor="middle" font-family="Arial,sans-serif" font-size="74" font-weight="bold" fill="#fff">DEV</text>
  </g>`;
  const iconSvgContent = fs.readFileSync(iconSvg, 'utf-8');
  const devSvgContent = iconSvgContent.replace('</svg>', devBadge + '\n</svg>');
  const devMasterPng = renderSvgContent(devSvgContent, 1024);
  console.log(`  Dev master: ${devMasterPng.length} bytes`);

  fs.writeFileSync(path.join(wailsBuildDir, 'appicon-dev.png'), devMasterPng);
  console.log(`  ${path.join(wailsBuildDir, 'appicon-dev.png')}`);

  const devIcoBuffers = [];
  const devBackedPng = await sharp(backdropSvg)
    .composite([{ input: devMasterPng }])
    .png()
    .toBuffer();
  for (const s of icoSizes) {
    devIcoBuffers.push(await resizePng(devBackedPng, s, s));
  }
  const devIcoBuf = makeIco(devIcoBuffers, icoSizes);
  fs.writeFileSync(path.join(wailsWinDir, 'icon-dev.ico'), devIcoBuf);
  console.log(`  ${path.join(wailsWinDir, 'icon-dev.ico')}`);

  // ---- Android Capacitor ----
  console.log('\nAndroid icons:');
  const androidRes = path.join(ROOT, 'mobile', 'android', 'app', 'src', 'main', 'res');

  const densities = {
    'mipmap-ldpi': 36,
    'mipmap-mdpi': 48,
    'mipmap-hdpi': 72,
    'mipmap-xhdpi': 96,
    'mipmap-xxhdpi': 144,
    'mipmap-xxxhdpi': 192,
  };

  for (const [dir, size] of Object.entries(densities)) {
    const outDir = path.join(androidRes, dir);
    await generateAndroidIcon(backedPng, outDir, 'ic_launcher', size);
    await generateAndroidIcon(backedPng, outDir, 'ic_launcher_round', size);
  }

  // Android adaptive icon foreground
  const fgDensities = {
    'mipmap-ldpi': 81,
    'mipmap-mdpi': 108,
    'mipmap-hdpi': 162,
    'mipmap-xhdpi': 216,
    'mipmap-xxhdpi': 324,
    'mipmap-xxxhdpi': 432,
  };
  for (const [dir, size] of Object.entries(fgDensities)) {
    const outDir = path.join(androidRes, dir);
    await generateAndroidIcon(foregroundPng, outDir, 'ic_launcher_foreground', size);
  }

  // Android adaptive icon background (solid #1b1d17 to match icon tile)
  const bgDensities = fgDensities;
  for (const [dir, size] of Object.entries(bgDensities)) {
    const outDir = path.join(androidRes, dir);
    const buf = await sharp({ create: { width: size, height: size, channels: 4, background: { r: 27, g: 29, b: 23, alpha: 255 } } }).png().toBuffer();
    fs.writeFileSync(path.join(outDir, 'ic_launcher_background.png'), buf);
    console.log(`  ${path.join(outDir, 'ic_launcher_background.png')}`);
  }

  // Android splash screens: solid #111111, bolt (no tile) centered.
  // Bolt visual spans ~418x642 of the 1024 foreground canvas (0.95 scale).
  // Target: bolt width = 16.6% of canvas width, capped at 30% of canvas height.
  console.log('\nAndroid splash:');
  for (const dir of fs.readdirSync(androidRes)) {
    const splashPath = path.join(androidRes, dir, 'splash.png');
    if (!fs.existsSync(splashPath)) continue;
    const meta = await sharp(splashPath).metadata();
    const W = meta.width, H = meta.height;
    let targetW = W * 0.166;
    let targetH = targetW * 642 / 418;
    if (targetH > H * 0.3) {
      targetH = H * 0.3;
      targetW = targetH * 418 / 642;
    }
    const renderW = Math.round(targetW / (418 / 1024));
    const logo = await sharp(foregroundPng).resize(renderW, renderW).png().toBuffer();
    const buf = await sharp({ create: { width: W, height: H, channels: 4, background: { r: 17, g: 17, b: 17, alpha: 255 } } })
      .composite([{ input: logo, left: Math.round((W - renderW) / 2), top: Math.round((H - renderW) / 2) }])
      .png()
      .toBuffer();
    fs.writeFileSync(splashPath, buf);
    console.log(`  ${splashPath} (${W}x${H})`);
  }

  console.log('\nDone.');
}

main().catch(err => {
  console.error(err);
  process.exit(1);
});
