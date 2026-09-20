// WindowController — abstracts native window control for the title bar.
//
// In a Wails (or other host-based) build the consumer wires this to the
// runtime bindings; in pure browser mode the no-op controller is fine.
/** Pure-browser fallback: no-ops for host-only methods, native API for fullscreen. */
export const browserNoOpController = {
    isHostMode() { return false; },
    async minimise() { },
    async toggleMaximise() { },
    async isMaximised() { return false; },
    async getSize() { return null; },
    async getPosition() { return null; },
    async quit() { },
    async fullscreen() {
        if (document.documentElement.requestFullscreen) {
            await document.documentElement.requestFullscreen();
        }
    },
    async unfullscreen() {
        if (document.exitFullscreen) {
            await document.exitFullscreen();
        }
    },
    async isFullscreen() {
        return Boolean(document.fullscreenElement);
    },
};
