import { jsx as _jsx, jsxs as _jsxs, Fragment as _Fragment } from "react/jsx-runtime";
import React, { useState, useCallback, useRef, useEffect, useSyncExternalStore } from 'react';
import { X, MoreVertical } from 'lucide-react';
import { noBadgeProvider } from '../adapters/tab-badge';
import { getTabContextMenuLabels } from '../i18n/tab-context-menu';
import './TabContainer.css';
const TAB_DND_TYPE = 'application/sporemind-tab';
const TAB_BAR_HEIGHT = 28; // matches --tab-bar-height
// Module-level fallback for WebView2/Chromium environments where
// getData() returns empty string for custom MIME types during drop.
let _activeDrag = null;
export const TabContainer = ({ leaf, renderTabContent, renderViewToolbar, getTabIcon, badgeProvider = noBadgeProvider, locale, onCloseTab, onSetActiveTab, onMoveTab, onMoveTabToEdge, }) => {
    const [dropEdge, setDropEdge] = useState(null);
    const [dropTabIndex, setDropTabIndex] = useState(null);
    const [isDragActive, setIsDragActive] = useState(false);
    const [contextMenu, setContextMenu] = useState(null);
    const containerRef = useRef(null);
    const tabbarRef = useRef(null);
    const overflowBtnRef = useRef(null);
    const [overflowOpen, setOverflowOpen] = useState(false);
    const [hiddenIndices, setHiddenIndices] = useState([]);
    const labels = getTabContextMenuLabels(locale);
    // Subscribe to badge provider for per-tab dirty/badge indicators.
    useSyncExternalStore(badgeProvider.subscribe, badgeProvider.getVersion);
    const activeTab = leaf.tabs[leaf.activeTabIndex] || null;
    // Close context menu when clicking outside
    useEffect(() => {
        if (!contextMenu)
            return;
        const handleClick = () => setContextMenu(null);
        document.addEventListener('click', handleClick);
        return () => document.removeEventListener('click', handleClick);
    }, [contextMenu]);
    // Track which tabs are hidden by overflow
    useEffect(() => {
        const bar = tabbarRef.current;
        if (!bar)
            return;
        const updateHidden = () => {
            const tabs = Array.from(bar.querySelectorAll('.tc-tab'));
            const barRect = bar.getBoundingClientRect();
            const barLeft = barRect.left;
            const barRight = barRect.right - 28; // reserve overflow button width
            const hidden = [];
            tabs.forEach((tab, i) => {
                const rect = tab.getBoundingClientRect();
                if (rect.left < barLeft || rect.right > barRight) {
                    hidden.push(i);
                }
            });
            setHiddenIndices(hidden);
        };
        const ro = new ResizeObserver(updateHidden);
        ro.observe(bar);
        bar.addEventListener('scroll', updateHidden);
        updateHidden();
        return () => {
            ro.disconnect();
            bar.removeEventListener('scroll', updateHidden);
        };
    }, [leaf.tabs.length, leaf.activeTabIndex]);
    // Auto-scroll active tab into view
    useEffect(() => {
        const bar = tabbarRef.current;
        if (!bar)
            return;
        const activeTab = bar.querySelector('.tc-tab.active');
        if (activeTab) {
            activeTab.scrollIntoView({ behavior: 'smooth', inline: 'nearest' });
        }
    }, [leaf.activeTabIndex]);
    // Close overflow menu on outside click
    useEffect(() => {
        if (!overflowOpen)
            return;
        const handleClick = () => setOverflowOpen(false);
        document.addEventListener('click', handleClick);
        return () => document.removeEventListener('click', handleClick);
    }, [overflowOpen]);
    const handleTabContextMenu = useCallback((e, tabIndex) => {
        e.preventDefault();
        setOverflowOpen(false);
        setContextMenu({ x: e.clientX, y: e.clientY, tabIndex });
    }, []);
    const handleCloseTab = useCallback((tabIndex) => {
        const tab = leaf.tabs[tabIndex];
        if (tab)
            onCloseTab?.(leaf.id, tab.id);
        setContextMenu(null);
    }, [leaf.id, leaf.tabs, onCloseTab]);
    const handleCloseOthers = useCallback((tabIndex) => {
        leaf.tabs.forEach((tab, i) => {
            if (i !== tabIndex && tab.closable) {
                onCloseTab?.(leaf.id, tab.id);
            }
        });
        setContextMenu(null);
    }, [leaf.id, leaf.tabs, onCloseTab]);
    const handleCloseRight = useCallback((tabIndex) => {
        for (let i = tabIndex + 1; i < leaf.tabs.length; i++) {
            const tab = leaf.tabs[i];
            if (tab.closable)
                onCloseTab?.(leaf.id, tab.id);
        }
        setContextMenu(null);
    }, [leaf.id, leaf.tabs, onCloseTab]);
    // Track global drag start/end so every TabContainer knows when to show
    // its capture overlay, even if the drag started in a different container.
    useEffect(() => {
        const onStart = () => setIsDragActive(true);
        const onEnd = () => { setIsDragActive(false); setDropEdge(null); setDropTabIndex(null); };
        document.addEventListener('dragstart', onStart);
        document.addEventListener('dragend', onEnd);
        return () => {
            document.removeEventListener('dragstart', onStart);
            document.removeEventListener('dragend', onEnd);
        };
    }, []);
    // ── Drag start ──
    const handleTabDragStart = useCallback((e, tab) => {
        _activeDrag = { leafId: leaf.id, tabId: tab.id };
        e.dataTransfer.setData(TAB_DND_TYPE, JSON.stringify(_activeDrag));
        e.dataTransfer.effectAllowed = 'move';
    }, [leaf.id]);
    // ── Tab bar: calculate insert index from cursor X ──
    const getTabInsertIndex = useCallback((clientX) => {
        const bar = tabbarRef.current;
        if (!bar)
            return leaf.tabs.length;
        const tabs = Array.from(bar.querySelectorAll('.tc-tab'));
        for (let i = 0; i < tabs.length; i++) {
            const rect = tabs[i].getBoundingClientRect();
            if (clientX < rect.left + rect.width / 2)
                return i;
        }
        return tabs.length;
    }, [leaf.tabs.length]);
    // ── Tab bar drag handlers (reorder / merge) ──
    const handleTabBarDragOver = useCallback((e) => {
        if (!e.dataTransfer.types.includes(TAB_DND_TYPE))
            return;
        e.preventDefault();
        e.stopPropagation();
        e.dataTransfer.dropEffect = 'move';
        setDropTabIndex(getTabInsertIndex(e.clientX));
        setDropEdge(null);
    }, [getTabInsertIndex]);
    const handleTabBarDragLeave = useCallback((e) => {
        if (!tabbarRef.current?.contains(e.relatedTarget)) {
            setDropTabIndex(null);
        }
    }, []);
    const handleTabBarDrop = useCallback((e) => {
        e.preventDefault();
        e.stopPropagation();
        const insertIdx = getTabInsertIndex(e.clientX);
        setDropTabIndex(null);
        const raw = e.dataTransfer.getData(TAB_DND_TYPE);
        const parsed = raw ? JSON.parse(raw) : _activeDrag;
        _activeDrag = null;
        if (!parsed)
            return;
        const { leafId: fromLeafId, tabId } = parsed;
        if (fromLeafId === leaf.id) {
            const fromIndex = leaf.tabs.findIndex(t => t.id === tabId);
            if (fromIndex < 0)
                return;
            const adjustedIdx = insertIdx > fromIndex ? insertIdx - 1 : insertIdx;
            if (adjustedIdx === fromIndex)
                return;
            onMoveTab?.(leaf.id, tabId, leaf.id, adjustedIdx);
        }
        else {
            onMoveTab?.(fromLeafId, tabId, leaf.id, insertIdx);
        }
    }, [leaf.id, leaf.tabs, getTabInsertIndex, onMoveTab]);
    // ── Content area edge detection for splitting ──
    const detectEdge = useCallback((e) => {
        const rect = containerRef.current?.getBoundingClientRect();
        if (!rect)
            return null;
        const x = e.clientX - rect.left;
        const y = e.clientY - rect.top;
        const w = rect.width;
        const h = rect.height;
        const contentH = h - TAB_BAR_HEIGHT;
        const margin = Math.min(w, contentH) * 0.25;
        const cy = y - TAB_BAR_HEIGHT;
        if (x < margin)
            return 'left';
        if (x > w - margin)
            return 'right';
        if (cy < margin)
            return 'top';
        if (cy > contentH - margin)
            return 'bottom';
        return 'center';
    }, []);
    // ── Drag-capture overlay handlers (splitting / moving to center) ──
    const handleOverlayDragOver = useCallback((e) => {
        if (!e.dataTransfer.types.includes(TAB_DND_TYPE))
            return;
        e.preventDefault();
        e.dataTransfer.dropEffect = 'move';
        setDropEdge(detectEdge(e));
        setDropTabIndex(null);
    }, [detectEdge]);
    const handleOverlayDragLeave = useCallback((e) => {
        if (!containerRef.current?.contains(e.relatedTarget)) {
            setDropEdge(null);
        }
    }, []);
    const handleOverlayDrop = useCallback((e) => {
        e.preventDefault();
        setDropEdge(null);
        const raw = e.dataTransfer.getData(TAB_DND_TYPE);
        const parsed = raw ? JSON.parse(raw) : _activeDrag;
        _activeDrag = null;
        if (!parsed)
            return;
        const { leafId: fromLeafId, tabId } = parsed;
        const edge = detectEdge(e);
        if (edge === 'center' || edge === null) {
            if (fromLeafId === leaf.id)
                return;
            onMoveTab?.(fromLeafId, tabId, leaf.id);
        }
        else {
            onMoveTabToEdge?.(fromLeafId, tabId, leaf.id, edge);
        }
    }, [leaf.id, detectEdge, onMoveTab, onMoveTabToEdge]);
    return (_jsxs("div", { ref: containerRef, className: "tc-container", children: [_jsxs("div", { className: "tc-tabbar-wrapper", children: [_jsxs("div", { className: "tc-tabbar", ref: tabbarRef, onDragOver: handleTabBarDragOver, onDragLeave: handleTabBarDragLeave, onDrop: handleTabBarDrop, children: [leaf.tabs.map((tab, i) => (_jsxs(React.Fragment, { children: [dropTabIndex === i && _jsx("div", { className: "tc-insert-indicator" }), _jsxs("div", { className: `tc-tab ${i === leaf.activeTabIndex ? 'active' : ''}`, draggable: true, onDragStart: (e) => handleTabDragStart(e, tab), onClick: () => onSetActiveTab?.(leaf.id, i), onContextMenu: (e) => handleTabContextMenu(e, i), children: [getTabIcon && (_jsx("span", { className: "tc-tab-icon", children: getTabIcon(tab) })), _jsx("span", { className: "tc-tab-label", children: tab.label }), badgeProvider.getBadge(tab) === 'dirty' && (_jsx("span", { className: "tc-tab-dirty" })), tab.closable && (_jsx("button", { className: "tc-tab-close", onClick: (e) => {
                                                    e.stopPropagation();
                                                    onCloseTab?.(leaf.id, tab.id);
                                                }, children: _jsx(X, { size: 10 }) }))] })] }, tab.id))), dropTabIndex === leaf.tabs.length && _jsx("div", { className: "tc-insert-indicator" })] }), hiddenIndices.length > 0 && (_jsxs(_Fragment, { children: [_jsx("button", { className: "tc-overflow-btn", ref: overflowBtnRef, type: "button", onClick: (e) => {
                                    e.stopPropagation();
                                    setContextMenu(null);
                                    setOverflowOpen((prev) => !prev);
                                }, children: _jsx(MoreVertical, { size: 14 }) }), overflowOpen && (_jsx("div", { className: "tc-overflow-menu", style: {
                                    left: overflowBtnRef.current?.getBoundingClientRect().left ?? 0,
                                    top: (overflowBtnRef.current?.getBoundingClientRect().bottom ?? 0) + 4,
                                }, onClick: (e) => e.stopPropagation(), children: hiddenIndices.map((i) => {
                                    const tab = leaf.tabs[i];
                                    if (!tab)
                                        return null;
                                    return (_jsxs("div", { className: "tc-overflow-item", onClick: () => {
                                            onSetActiveTab?.(leaf.id, i);
                                            setOverflowOpen(false);
                                        }, children: [getTabIcon && (_jsx("span", { className: "tc-overflow-item-icon", children: getTabIcon(tab) })), _jsx("span", { className: "tc-overflow-item-label", children: tab.label }), badgeProvider.getBadge(tab) === 'dirty' && (_jsx("span", { className: "tc-tab-dirty" }))] }, tab.id));
                                }) }))] }))] }), _jsxs("div", { className: "tc-content", children: [activeTab ? renderTabContent(activeTab) : (_jsx("div", { className: "tc-empty", children: "No tab selected" })), renderViewToolbar && activeTab && renderViewToolbar(activeTab.viewType), isDragActive && (_jsx("div", { className: "tc-drag-capture", onDragOver: handleOverlayDragOver, onDragLeave: handleOverlayDragLeave, onDrop: handleOverlayDrop }))] }), dropEdge && dropEdge !== 'center' && (_jsx("div", { className: `tc-drop-zone ${dropEdge}` })), dropEdge === 'center' && (_jsx("div", { className: "tc-drop-zone center" })), contextMenu && (_jsxs("div", { className: "tc-context-menu", style: { left: contextMenu.x, top: contextMenu.y }, onClick: (e) => e.stopPropagation(), children: [_jsx("div", { className: "tc-menu-item", onClick: () => handleCloseTab(contextMenu.tabIndex), children: labels.close }), _jsx("div", { className: "tc-menu-item", onClick: () => handleCloseOthers(contextMenu.tabIndex), children: labels.closeOthers }), _jsx("div", { className: "tc-menu-item", onClick: () => handleCloseRight(contextMenu.tabIndex), children: labels.closeRight })] }))] }));
};
