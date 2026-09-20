// Sporemind Cookie Bridge popup.
//
// Polls GET {server}/pending for requests created by the sporemind agent's
// import_site tool call, shows one card per request, and on confirmation
// reads the site's cookies via chrome.cookies.getAll and POSTs them to
// {server}/push. Cookie values exist only in the POST body — never in the
// DOM, storage, or console.

const els = {
  status: document.getElementById("status"),
  settings: document.getElementById("settings"),
  settingsBtn: document.getElementById("settings-btn"),
  serverUrl: document.getElementById("server-url"),
  pairingToken: document.getElementById("pairing-token"),
  saveSettings: document.getElementById("save-settings"),
  noRequests: document.getElementById("no-requests"),
  requestList: document.getElementById("request-list"),
  serverState: document.getElementById("server-state"),
};

const state = {
  serverUrl: "",
  token: "",
  busy: false,
};

async function loadSettings() {
  const got = await chrome.storage.local.get(["serverUrl", "pairingToken"]);
  state.serverUrl = (got.serverUrl || "http://127.0.0.1:47613").replace(/\/+$/, "");
  state.token = got.pairingToken || "";
  els.serverUrl.value = state.serverUrl;
  els.pairingToken.value = state.token;
}

function authHeaders() {
  return { "X-Pairing-Token": state.token };
}

function setStatus(kind, text) {
  els.status.hidden = !text;
  els.status.className = "status " + (kind || "");
  els.status.textContent = text || "";
}

function timeAgo(iso) {
  const s = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000));
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  return `${Math.round(s / 3600)}h ago`;
}

async function fetchPending() {
  const resp = await fetch(`${state.serverUrl}/pending`, {
    headers: authHeaders(),
  });
  if (resp.status === 401) {
    throw new Error("Pairing token rejected. Update it in the settings (⚙).");
  }
  if (!resp.ok) {
    throw new Error(`Sporemind answered HTTP ${resp.status}. Is the desktop app running?`);
  }
  return resp.json();
}

async function confirmRequest(req) {
  if (state.busy) return;
  state.busy = true;
  setStatus("", `Reading cookies for ${req.domain}…`);
  try {
    // Read cookies for the requested domain and its subdomains.
    const cookies = await chrome.cookies.getAll({ domain: req.domain });
    const partitionedExtra = await chrome.cookies
      .getAll({ domain: req.domain, partitionKey: {} })
      .catch(() => []);
    const all = cookies.concat(partitionedExtra);
    if (!all.length) {
      setStatus("err", `No cookies found for ${req.domain} in this Chrome profile.`);
      return;
    }
    const resp = await fetch(`${state.serverUrl}/push`, {
      method: "POST",
      headers: { "Content-Type": "application/json", ...authHeaders() },
      body: JSON.stringify({ pendingId: req.id, cookies: all }),
    });
    const body = await resp.json().catch(() => ({}));
    if (!resp.ok) {
      setStatus("err", body.error || `Push failed (HTTP ${resp.status}).`);
      return;
    }
    setStatus("ok", `Imported ${body.imported} cookies for ${req.domain}.`);
  } catch (err) {
    setStatus("err", String(err && err.message ? err.message : err));
  } finally {
    state.busy = false;
    await refresh();
  }
}

function renderRequests(requests) {
  els.requestList.textContent = "";
  els.noRequests.hidden = requests.length > 0;
  for (const req of requests) {
    const card = document.createElement("div");
    card.className = "request";

    const domain = document.createElement("div");
    domain.className = "domain";
    domain.textContent = req.domain;

    const meta = document.createElement("div");
    meta.className = "meta";
    meta.textContent = `→ sporemind browser ${req.instanceId} · ${timeAgo(req.createdAt)}`;

    const row = document.createElement("div");
    row.className = "row";
    const allow = document.createElement("button");
    allow.className = "primary";
    allow.textContent = "Move cookies";
    allow.disabled = state.busy;
    allow.addEventListener("click", () => confirmRequest(req));
    row.appendChild(allow);

    card.append(domain, meta, row);
    els.requestList.appendChild(card);
  }
}

async function refresh() {
  if (!state.serverUrl) return;
  try {
    const data = await fetchPending();
    els.serverState.textContent = state.serverUrl;
    renderRequests(data.requests || []);
  } catch (err) {
    els.serverState.textContent = state.serverUrl + " (unreachable)";
    setStatus("err", String(err && err.message ? err.message : err));
  }
}

els.settingsBtn.addEventListener("click", () => {
  els.settings.hidden = !els.settings.hidden;
});

els.saveSettings.addEventListener("click", async () => {
  state.serverUrl = els.serverUrl.value.trim().replace(/\/+$/, "");
  state.token = els.pairingToken.value.trim();
  await chrome.storage.local.set({ serverUrl: state.serverUrl, pairingToken: state.token });
  els.settings.hidden = true;
  setStatus("", "Saved.");
  await refresh();
});

loadSettings().then(refresh);
// Re-poll while the popup is open so late import_site calls appear.
setInterval(refresh, 3000);
