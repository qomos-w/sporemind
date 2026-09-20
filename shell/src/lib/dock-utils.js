export const DOCK_THRESHOLD = 40;
export function detectDockZone(clientX, clientY, containerRect, bottomHeight = 0) {
    const relX = clientX - containerRect.left;
    const relY = clientY - containerRect.top;
    const w = containerRect.width;
    const h = containerRect.height;
    const hExcludingBottom = h - bottomHeight;
    if (relX < DOCK_THRESHOLD)
        return relY < hExcludingBottom / 2 ? 'left-top' : 'left-bottom';
    if (relX > w - DOCK_THRESHOLD)
        return relY < hExcludingBottom / 2 ? 'right-top' : 'right-bottom';
    if (relY > h - DOCK_THRESHOLD)
        return relX < w / 2 ? 'bottom-left' : 'bottom-right';
    return null;
}
/** Clamp a floating panel so it stays within viewport */
export function clampToViewport(pos, size) {
    const vw = window.innerWidth;
    const vh = window.innerHeight;
    return {
        x: Math.max(0, Math.min(pos.x, vw - size.w)),
        y: Math.max(0, Math.min(pos.y, vh - size.h)),
    };
}
