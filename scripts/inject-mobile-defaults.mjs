import fs from 'fs';
import path from 'path';

const ENV_PATH = process.env.MOBILE_ENV_PATH || '.env';
const INDEX_PATH = process.env.MOBILE_INDEX_PATH || 'mobile/www/index.html';

function parseEnv(filePath) {
  const text = fs.readFileSync(filePath, 'utf-8');
  let serverUrls = [];
  let account = '';

  for (const line of text.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#')) continue;

    const eq = trimmed.indexOf('=');
    if (eq === -1) continue;

    const key = trimmed.slice(0, eq).trim();
    let value = trimmed.slice(eq + 1).trim();

    // Remove surrounding quotes
    if ((value.startsWith('"') && value.endsWith('"')) ||
        (value.startsWith("'") && value.endsWith("'"))) {
      value = value.slice(1, -1);
    }

    if (key === 'DEFAULT_SERVER_URLS') {
      try {
        serverUrls = JSON.parse(value);
      } catch {
        serverUrls = [];
      }
    }
    if (key === 'DEFAULT_ACCOUNT') {
      account = value;
    }
    // Fallback: derive server URL from VITE_API_BASE (strip /api/... path)
    if (key === 'VITE_API_BASE' && serverUrls.length === 0) {
      try {
        const u = new URL(value);
        const base = u.origin;
        if (base && base !== 'null') serverUrls = [base];
      } catch {}
    }
  }

  return { serverUrls, account };
}

function inject() {
  let env = { serverUrls: [], account: '' };
  if (fs.existsSync(ENV_PATH)) {
    env = parseEnv(ENV_PATH);
  }
  // Fallback: if primary .env has no server URLs, try .env.example
  if (env.serverUrls.length === 0) {
    const examplePath = path.resolve('.env.example');
    if (fs.existsSync(examplePath)) {
      const exampleEnv = parseEnv(examplePath);
      if (exampleEnv.serverUrls.length > 0) env = exampleEnv;
    }
  }
  let index = fs.readFileSync(INDEX_PATH, 'utf-8');

  const startMarker = '<!-- MOBILE_DEFAULT_CONFIG -->';
  const endMarker = '<!-- /MOBILE_DEFAULT_CONFIG -->';

  const startIdx = index.indexOf(startMarker);
  const endIdx = index.indexOf(endMarker);

  if (startIdx === -1 || endIdx === -1 || endIdx <= startIdx) {
    console.error(`[inject-mobile-defaults] Markers not found in ${INDEX_PATH}`);
    process.exit(1);
  }

  const injected = `<script>
window.__defaultServerUrls = ${JSON.stringify(env.serverUrls)};
window.__defaultAccount = ${JSON.stringify(env.account)};
</script>`;

  const before = index.slice(0, startIdx + startMarker.length);
  const after = index.slice(endIdx);
  const updated = before + '\n' + injected + '\n' + after;

  fs.writeFileSync(INDEX_PATH, updated, 'utf-8');
  console.log(`[inject-mobile-defaults] Injected ${env.serverUrls.length} server URLs and account "${env.account}" into ${INDEX_PATH}`);
}

inject();
