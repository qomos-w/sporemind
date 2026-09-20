import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { isWails } from "./application/runtime";
import { I18nProvider } from "./i18n";
import { loadDesktopConfig } from "./application/desktop-config";

// In Wails desktop frameless mode the WebView2 scrollbars at the window edges
// intercept the mouse events that the Wails frontend resize handler needs.
// Adding a class here lets CSS inset the layout so scrollbars don't overlap
// the native resize border (≈5px on Windows).
if (isWails()) {
  document.body.classList.add('wails-desktop')
}

async function bootstrap() {
  // In Wails desktop mode the transport selection depends on the
  // sporemind.yaml configuration. Load it before any module that imports
  // generated-client creates the transport.
  if (isWails()) {
    await loadDesktopConfig()
  }

  const { App } = await import('./App')

  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <I18nProvider>
        <App />
      </I18nProvider>
    </StrictMode>,
  );
}

bootstrap()
