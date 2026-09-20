import { jsx as _jsx, jsxs as _jsxs, Fragment as _Fragment } from "react/jsx-runtime";
import { useRef, useCallback, useState, useEffect } from 'react';
import { Pin, X } from 'lucide-react';
import { detectDockZone } from '../lib/dock-utils';
import './FloatingPanel.css';
function detectEdge(e, el, margin = 6) {
    const rect = el.getBoundingClientRect();
    const x = e.clientX - rect.left;
    const y = e.clientY - rect.top;
    const w = rect.width;
    const h = rect.height;
    const top = y < margin;
    const bottom = y > h - margin;
    const left = x < margin;
    const right = x > w - margin;
    if (top && left)
        return 'nw';
    if (top && right)
        return 'ne';
    if (bottom && left)
        return 'sw';
    if (bottom && right)
        return 'se';
    if (top)
        return 'n';
    if (bottom)
        return 's';
    if (left)
        return 'w';
    if (right)
        return 'e';
    return null;
}
const EDGE_CURSORS = {
    n: 'ns-resize', s: 'ns-resize',
    e: 'ew-resize', w: 'ew-resize',
    ne: 'nesw-resize', sw: 'nesw-resize',
    nw: 'nwse-resize', se: 'nwse-resize',
};
export const FloatingPanel = ({ id, title, mode, visible, pos, size, minSize = { w: 200, h: 120 }, zIndex, containerRef, onClose, onTogglePin, onMove, onMoveEnd, onResize, onFocus, onDock, bottomHeight = 0, getPreviewStyle, children, }) => {
    const panelRef = useRef(null);
    // Local position state for high-frequency drag updates
    const [localPos, setLocalPos] = useState(pos);
    const [dragging, setDragging] = useState(false);
    const [dockPreview, setDockPreview] = useState(null);
    const [dockPreviewStyle, setDockPreviewStyle] = useState({});
    const [hoverEdge, setHoverEdge] = useState(null);
    const dockPreviewRef = useRef(null);
    dockPreviewRef.current = dockPreview;
    // RAF-batched drag state updates (3 useState → 1 RAF per frame)
    const pendingState = useRef({ pos: null, zone: null, style: {} });
    const rafPending = useRef(false);
    const commitPendingState = useCallback(() => {
        const p = pendingState.current;
        if (p.pos) {
            setLocalPos(p.pos);
            p.pos = null;
        }
        setDockPreview(p.zone);
        setDockPreviewStyle(p.style);
        rafPending.current = false;
    }, []);
    const scheduleDragUpdate = useCallback(() => {
        if (rafPending.current)
            return;
        rafPending.current = true;
        requestAnimationFrame(commitPendingState);
    }, [commitPendingState]);
    const callbacksRef = useRef({ onMove, onMoveEnd, onDock, onResize, containerRef, minSize, getPreviewStyle });
    callbacksRef.current = { onMove, onMoveEnd, onDock, onResize, containerRef, minSize, getPreviewStyle };
    // Sync external pos when not dragging
    useEffect(() => {
        if (!dragging)
            setLocalPos(pos);
    }, [pos.x, pos.y, dragging]);
    // ── Drag (titlebar) ──
    const dragState = useRef(null);
    const onTitlePointerDown = useCallback((e) => {
        if (e.target.closest('.fp-titlebar-btn'))
            return;
        e.preventDefault();
        e.stopPropagation();
        dragState.current = { startX: e.clientX, startY: e.clientY, origX: pos.x, origY: pos.y };
        setDragging(true);
        onFocus?.();
    }, [pos.x, pos.y, onFocus]);
    useEffect(() => {
        if (!dragging)
            return;
        const onPointerMove = (e) => {
            if (!dragState.current)
                return;
            const dx = e.clientX - dragState.current.startX;
            const dy = e.clientY - dragState.current.startY;
            const newPos = { x: dragState.current.origX + dx, y: dragState.current.origY + dy };
            pendingState.current.pos = newPos;
            callbacksRef.current.onMove?.(newPos);
            const cRef = callbacksRef.current.containerRef;
            if (cRef?.current) {
                const rect = cRef.current.getBoundingClientRect();
                const zone = detectDockZone(e.clientX, e.clientY, rect, bottomHeight);
                pendingState.current.zone = zone;
                if (zone && callbacksRef.current.getPreviewStyle) {
                    pendingState.current.style = callbacksRef.current.getPreviewStyle(zone, size);
                }
                else {
                    pendingState.current.style = {};
                }
            }
            scheduleDragUpdate();
        };
        const onPointerUp = () => {
            const zone = dockPreviewRef.current;
            if (zone && callbacksRef.current.onDock) {
                callbacksRef.current.onDock(zone);
            }
            else {
                callbacksRef.current.onMoveEnd?.();
            }
            dragState.current = null;
            setDragging(false);
            setDockPreview(null);
            setDockPreviewStyle({});
        };
        document.addEventListener('pointermove', onPointerMove);
        document.addEventListener('pointerup', onPointerUp);
        document.addEventListener('pointercancel', onPointerUp);
        return () => {
            document.removeEventListener('pointermove', onPointerMove);
            document.removeEventListener('pointerup', onPointerUp);
            document.removeEventListener('pointercancel', onPointerUp);
        };
    }, [dragging, bottomHeight, size]);
    // ── Edge resize (all 8 edges) ──
    const edgeResizeState = useRef(null);
    const [edgeResizing, setEdgeResizing] = useState(false);
    const onPanelPointerDown = useCallback((e) => {
        if (!panelRef.current)
            return;
        const edge = detectEdge(e, panelRef.current);
        if (!edge)
            return; // not on an edge — let normal events bubble
        e.preventDefault();
        e.stopPropagation();
        edgeResizeState.current = {
            edge,
            startX: e.clientX, startY: e.clientY,
            origX: pos.x, origY: pos.y, origW: size.w, origH: size.h,
        };
        setEdgeResizing(true);
        onFocus?.();
    }, [pos.x, pos.y, size.w, size.h, onFocus]);
    useEffect(() => {
        if (!edgeResizing)
            return;
        const onPointerMove = (e) => {
            const s = edgeResizeState.current;
            if (!s)
                return;
            const { minSize: ms, onResize: onRes } = callbacksRef.current;
            const dx = e.clientX - s.startX;
            const dy = e.clientY - s.startY;
            let newX = s.origX, newY = s.origY, newW = s.origW, newH = s.origH;
            // Horizontal
            if (s.edge.includes('e')) {
                newW = Math.max(ms.w, s.origW + dx);
            }
            if (s.edge.includes('w')) {
                const dw = Math.min(dx, s.origW - ms.w);
                newX = s.origX + dw;
                newW = s.origW - dw;
            }
            // Vertical
            if (s.edge.includes('s')) {
                newH = Math.max(ms.h, s.origH + dy);
            }
            if (s.edge.includes('n')) {
                const dh = Math.min(dy, s.origH - ms.h);
                newY = s.origY + dh;
                newH = s.origH - dh;
            }
            onRes?.({ w: newW, h: newH }, { x: newX, y: newY });
        };
        const onPointerUp = () => {
            edgeResizeState.current = null;
            setEdgeResizing(false);
        };
        document.addEventListener('pointermove', onPointerMove);
        document.addEventListener('pointerup', onPointerUp);
        document.addEventListener('pointercancel', onPointerUp);
        return () => {
            document.removeEventListener('pointermove', onPointerMove);
            document.removeEventListener('pointerup', onPointerUp);
            document.removeEventListener('pointercancel', onPointerUp);
        };
    }, [edgeResizing]);
    // ── Hover cursor on edges ──
    const onPanelPointerMove = useCallback((e) => {
        if (dragging || edgeResizing)
            return;
        if (!panelRef.current)
            return;
        const edge = detectEdge(e, panelRef.current);
        setHoverEdge(edge);
    }, [dragging, edgeResizing]);
    const onPanelPointerLeave = useCallback(() => {
        if (!edgeResizing)
            setHoverEdge(null);
    }, [edgeResizing]);
    if (!visible)
        return null;
    const displayPos = dragging ? localPos : pos;
    const cursorStyle = hoverEdge ? EDGE_CURSORS[hoverEdge] : undefined;
    return (_jsxs(_Fragment, { children: [_jsxs("div", { ref: panelRef, className: `floating-panel mode-${mode}${dragging ? ' dragging' : ''}`, "data-panel-id": id, style: {
                    left: displayPos.x,
                    top: displayPos.y,
                    width: size.w,
                    height: size.h,
                    zIndex,
                    cursor: cursorStyle,
                }, onPointerDown: (e) => {
                    // Try edge resize first; if not on edge, just focus
                    if (panelRef.current && detectEdge(e, panelRef.current)) {
                        onPanelPointerDown(e);
                    }
                    else {
                        onFocus?.();
                    }
                }, onPointerMove: onPanelPointerMove, onPointerLeave: onPanelPointerLeave, children: [_jsxs("div", { className: "fp-titlebar", onPointerDown: onTitlePointerDown, children: [_jsx("span", { className: "fp-titlebar-label", children: title }), _jsxs("div", { className: "fp-titlebar-actions", children: [onTogglePin && (_jsx("button", { className: `fp-titlebar-btn ${mode === 'pinned' ? 'pinned' : ''}`, onPointerDown: e => e.stopPropagation(), onClick: (e) => { e.stopPropagation(); onTogglePin(); }, title: mode === 'pinned' ? 'Unpin (float)' : 'Pin in place', children: _jsx(Pin, { size: 12, opacity: mode === 'pinned' ? 1 : 0.5 }) })), onClose && (_jsx("button", { className: "fp-titlebar-btn", onPointerDown: e => e.stopPropagation(), onClick: (e) => { e.stopPropagation(); onClose(); }, title: "Close", children: _jsx(X, { size: 10 }) }))] })] }), _jsx("div", { className: "fp-body", children: children })] }), dragging && dockPreview && dockPreviewStyle && Object.keys(dockPreviewStyle).length > 0 && (_jsx("div", { className: "fp-dock-preview", style: dockPreviewStyle }))] }));
};
