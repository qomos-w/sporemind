import { jsx as _jsx, jsxs as _jsxs, Fragment as _Fragment } from "react/jsx-runtime";
import { useState, useEffect, useRef, useCallback } from 'react';
import { Pin, X } from 'lucide-react';
import { detectDockZone } from '../lib/dock-utils';
export const DockZonePanel = ({ zone, activeTabId, panelTitle, panelSize, canvasAreaRef, bottomHeight, getPreviewStyle, onDock, onTogglePin, onHide, renderPanel, }) => {
    const [dragging, setDragging] = useState(false);
    const [dockPreview, setDockPreview] = useState(null);
    const dragStateRef = useRef(null);
    const onTitlePointerDown = useCallback((e) => {
        if (e.target.closest('.dl-zone-actions'))
            return;
        e.preventDefault();
        dragStateRef.current = { startX: e.clientX, startY: e.clientY };
        setDragging(true);
    }, []);
    useEffect(() => {
        if (!dragging)
            return;
        const onPointerMove = (e) => {
            if (!dragStateRef.current || !canvasAreaRef.current)
                return;
            const rect = canvasAreaRef.current.getBoundingClientRect();
            setDockPreview(detectDockZone(e.clientX, e.clientY, rect, bottomHeight));
        };
        const onPointerUp = () => {
            if (dockPreview && dockPreview !== zone && activeTabId) {
                onDock?.(activeTabId, dockPreview);
            }
            dragStateRef.current = null;
            setDragging(false);
            setDockPreview(null);
        };
        document.addEventListener('pointermove', onPointerMove);
        document.addEventListener('pointerup', onPointerUp);
        return () => {
            document.removeEventListener('pointermove', onPointerMove);
            document.removeEventListener('pointerup', onPointerUp);
        };
    }, [dragging, dockPreview, zone, activeTabId, canvasAreaRef, onDock]);
    if (!activeTabId)
        return null;
    return (_jsxs(_Fragment, { children: [_jsxs("div", { className: "dl-zone", children: [_jsxs("div", { className: `dl-zone-header${dragging ? ' dragging' : ''}`, onPointerDown: onTitlePointerDown, children: [_jsx("span", { className: "dl-zone-title", children: panelTitle }), _jsxs("div", { className: "dl-zone-actions", children: [_jsx("button", { className: "dl-docked-panel-btn", onClick: () => onTogglePin?.(activeTabId), onPointerDown: e => e.stopPropagation(), title: "Undock (float)", children: _jsx(Pin, { size: 10, opacity: 0.5 }) }), _jsx("button", { className: "dl-docked-panel-btn", onClick: () => onHide?.(activeTabId), onPointerDown: e => e.stopPropagation(), title: "Hide", children: _jsx(X, { size: 8 }) })] })] }), _jsx("div", { className: "dl-zone-body", children: renderPanel(activeTabId) })] }), dragging && dockPreview && panelSize && (_jsx("div", { className: "dl-dock-preview", style: getPreviewStyle(dockPreview, panelSize) }))] }));
};
