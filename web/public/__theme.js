// Default pre-paint theme for non-Wails serving paths (vite dev, gateway
// static hosting, capacitor). The Wails asset middleware in
// cmd/sporemind-desktop/main.go intercepts this path and serves the persisted
// theme instead.
window.__SPOREMIND_THEME__ = '{"mode":"light"}'
document.documentElement.setAttribute('data-theme', 'light')
