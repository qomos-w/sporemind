# Sporemind Cookie Bridge (Chrome MV3 extension)

Moves cookies for **one site at a time** from this Chrome profile into a
Sporemind **independent browser instance**, on explicit confirmation. It is the
extraction half of sporemind's `cookiebridge` actor — Chrome's app-bound
encryption (v127+) means an extension using `chrome.cookies` is the only
sanctioned way to read Chrome cookies.

## Pairing

1. In sporemind desktop open **Settings → MCP → Cookie Bridge**, copy the
   **URL** and the **pairing token** (or call `cookiebridge.pairing_info`).
2. Load this extension: `chrome://extensions` → Developer mode →
   **Load unpacked** → select this folder.
3. Click the extension icon → ⚙ → paste URL + token → **Save**.

The token is stored in `chrome.storage.local` (plain text inside your local
Chrome profile — treat a paired profile like a logged-in session).

## Use

1. Ask the sporemind agent to migrate a site (it calls the MCP tool
   `import_site` and waits).
2. Open the Cookie Bridge popup — the request appears as a card
   (`domain → instance`).
3. Click **Move cookies**. The popup reads the site's cookies via
   `chrome.cookies.getAll` and POSTs them to `http://127.0.0.1:47613/push`
   on your machine. Only counts and domain names go back to the agent.

Nothing is sent anywhere except your local sporemind process; the bridge
listener binds loopback only and rejects requests without the pairing token.

## Files

- `manifest.json` — MV3 manifest (`cookies`, `storage`, loopback host perms)
- `popup.html` / `popup.css` / `popup.js` — the whole UI; no background worker
  (reads happen only when you click **Move cookies**)

## Notes

- Chrome Web Store review for `cookies` permission is strict; v1 ships as
  load-unpacked. Store listing is a separate effort.
- If you regenerate the token in sporemind settings, re-pair the extension.
