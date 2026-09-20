import { jsx as _jsx, jsxs as _jsxs } from "react/jsx-runtime";
import { useCallback, useRef } from 'react';
import { TabContainer } from './TabContainer';
import './SplitContainer.css';
export const SplitContainer = ({ node, renderTabContent, renderViewToolbar, getTabIcon, badgeProvider, locale, onResizeSplit, onCloseTab, onSetActiveTab, onMoveTab, onMoveTabToEdge, }) => {
    if (node.type === 'leaf') {
        return (_jsx(TabContainer, { leaf: node, renderTabContent: renderTabContent, renderViewToolbar: renderViewToolbar, getTabIcon: getTabIcon, badgeProvider: badgeProvider, locale: locale, onCloseTab: onCloseTab, onSetActiveTab: onSetActiveTab, onMoveTab: onMoveTab, onMoveTabToEdge: onMoveTabToEdge }));
    }
    const dir = node.direction === 'horizontal' ? 'row' : 'column';
    const firstSize = `${node.ratio * 100}%`;
    return (_jsxs("div", { className: "split-container", style: { flexDirection: dir }, children: [_jsx("div", { className: "split-pane", style: { flexBasis: firstSize, flexGrow: 0, flexShrink: 0 }, children: _jsx(SplitContainer, { node: node.first, renderTabContent: renderTabContent, renderViewToolbar: renderViewToolbar, getTabIcon: getTabIcon, badgeProvider: badgeProvider, locale: locale, onResizeSplit: onResizeSplit, onCloseTab: onCloseTab, onSetActiveTab: onSetActiveTab, onMoveTab: onMoveTab, onMoveTabToEdge: onMoveTabToEdge }) }), _jsx(SplitDivider, { direction: node.direction, splitId: node.id, onResize: onResizeSplit }), _jsx("div", { className: "split-pane", style: { flex: 1 }, children: _jsx(SplitContainer, { node: node.second, renderTabContent: renderTabContent, renderViewToolbar: renderViewToolbar, getTabIcon: getTabIcon, badgeProvider: badgeProvider, locale: locale, onResizeSplit: onResizeSplit, onCloseTab: onCloseTab, onSetActiveTab: onSetActiveTab, onMoveTab: onMoveTab, onMoveTabToEdge: onMoveTabToEdge }) })] }));
};
// ── Split Divider ──
// Uses setPointerCapture so pointer events are always delivered to this element
// while dragging, regardless of what content (Monaco, DAG canvas, etc.) is underneath.
const SplitDivider = ({ direction, splitId, onResize }) => {
    // Captured at pointer-down; valid for the entire drag gesture.
    const drag = useRef(null);
    const onPointerDown = useCallback((e) => {
        e.preventDefault();
        e.stopPropagation();
        const parent = e.currentTarget.parentElement;
        if (!parent)
            return;
        // Capture the pointer so all subsequent pointer events are routed to this
        // element, even when the cursor moves over Monaco, DAG canvas, iframes, etc.
        e.currentTarget.setPointerCapture(e.pointerId);
        const rect = parent.getBoundingClientRect();
        drag.current = {
            totalSize: direction === 'horizontal' ? rect.width : rect.height,
            startOffset: direction === 'horizontal' ? rect.left : rect.top,
        };
    }, [direction]);
    const onPointerMove = useCallback((e) => {
        if (!drag.current || !onResize)
            return;
        const pos = direction === 'horizontal' ? e.clientX : e.clientY;
        const newRatio = (pos - drag.current.startOffset) / drag.current.totalSize;
        onResize(splitId, newRatio);
    }, [direction, splitId, onResize]);
    const onPointerUp = useCallback(() => {
        drag.current = null;
    }, []);
    return (_jsx("div", { className: `split-divider ${direction}`, onPointerDown: onPointerDown, onPointerMove: onPointerMove, onPointerUp: onPointerUp, onPointerCancel: onPointerUp }));
};
