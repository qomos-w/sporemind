import { jsx as _jsx, jsxs as _jsxs } from "react/jsx-runtime";
import { useState, useEffect, useRef, useCallback } from 'react';
import { Minus, Square, X, Hexagon, Circle, ChevronRight } from 'lucide-react';
import { browserNoOpController } from '../adapters/window';
import './TitleBar.css';
const MenuList = ({ items, onItemClick, level = 0 }) => {
    return (_jsx("div", { className: `tb-submenu tb-submenu-level-${level}`, children: items.map(sub => (sub.separator ? (_jsx("div", { className: "tb-submenu-separator" }, sub.id)) : sub.submenu ? (_jsxs("div", { className: "tb-submenu-item-wrapper has-children", children: [_jsxs("button", { className: `tb-submenu-item ${sub.disabled ? 'disabled' : ''}`, disabled: sub.disabled, type: "button", children: [_jsx("span", { className: "tb-submenu-check", children: sub.checked ? '✓' : '' }), _jsx("span", { className: "tb-submenu-label", children: sub.label }), _jsx("span", { className: "tb-submenu-chevron", children: _jsx(ChevronRight, { size: 12 }) })] }), _jsx(MenuList, { items: sub.submenu, onItemClick: onItemClick, level: level + 1 })] }, sub.id)) : (_jsxs("button", { className: `tb-submenu-item ${sub.disabled ? 'disabled' : ''}`, disabled: sub.disabled, onClick: () => onItemClick(sub.id), type: "button", children: [_jsx("span", { className: "tb-submenu-check", children: sub.checked ? '✓' : '' }), _jsx("span", { className: "tb-submenu-label", children: sub.label })] }, sub.id)))) }));
};
export const TitleBar = ({ title = '', menuItems = [], shellMode = 'workbench', onShellModeChange, onMenuItemClick, controller = browserNoOpController, showMulticonsole = false, agentAvatars, rightSlot, }) => {
    const [maximised, setMaximised] = useState(false);
    const [openMenuId, setOpenMenuId] = useState(null);
    const menuRef = useRef(null);
    const [mcDropdownOpen, setMcDropdownOpen] = useState(false);
    const mcDropdownRef = useRef(null);
    const refreshMaximised = useCallback(() => {
        controller.isMaximised().then(setMaximised).catch(() => { });
    }, [controller]);
    useEffect(() => {
        refreshMaximised();
        const handleWindowStateChange = () => {
            refreshMaximised();
        };
        window.addEventListener('resize', handleWindowStateChange);
        window.addEventListener('focus', handleWindowStateChange);
        return () => {
            window.removeEventListener('resize', handleWindowStateChange);
            window.removeEventListener('focus', handleWindowStateChange);
        };
    }, [refreshMaximised]);
    useEffect(() => {
        if (!openMenuId)
            return;
        const handleClick = (e) => {
            if (menuRef.current && !menuRef.current.contains(e.target)) {
                setOpenMenuId(null);
            }
        };
        document.addEventListener('mousedown', handleClick);
        return () => document.removeEventListener('mousedown', handleClick);
    }, [openMenuId]);
    useEffect(() => {
        if (!mcDropdownOpen)
            return;
        const handleClick = (e) => {
            if (mcDropdownRef.current && !mcDropdownRef.current.contains(e.target)) {
                setMcDropdownOpen(false);
            }
        };
        document.addEventListener('mousedown', handleClick);
        return () => document.removeEventListener('mousedown', handleClick);
    }, [mcDropdownOpen]);
    useEffect(() => {
        const onGrid = (e) => {
            const detail = e.detail;
            setMcCols(detail.cols);
            setMcRows(detail.rows);
        };
        window.addEventListener('sporemind:multiconsole-grid', onGrid);
        return () => {
            window.removeEventListener('sporemind:multiconsole-grid', onGrid);
        };
    }, []);
    const handleMenuToggle = useCallback((id) => {
        setOpenMenuId(prev => prev === id ? null : id);
    }, []);
    const handleSubmenuClick = useCallback((itemId) => {
        setOpenMenuId(null);
        onMenuItemClick?.(itemId);
    }, [onMenuItemClick]);
    const handleMinimise = useCallback(() => {
        if (!controller.isHostMode())
            return;
        controller.minimise();
    }, [controller]);
    const handleToggleMaximise = useCallback(() => {
        if (!controller.isHostMode())
            return;
        controller.toggleMaximise().finally(refreshMaximised);
    }, [controller, refreshMaximised]);
    const handleClose = useCallback(() => {
        if (!controller.isHostMode())
            return;
        controller.quit();
    }, [controller]);
    const handleDragDoubleClick = useCallback(() => {
        handleToggleMaximise();
    }, [handleToggleMaximise]);
    const isWorkbench = shellMode === 'workbench';
    const isMulticonsole = shellMode === 'multiconsole';
    const showShellModeSwitch = isWorkbench || isMulticonsole;
    const showMenuItems = isWorkbench && menuItems.length > 0;
    const showMcButton = isMulticonsole || showMulticonsole;
    const [mcCols, setMcCols] = useState(() => {
        const saved = localStorage.getItem('sporemind:multiconsole-grid-config');
        if (saved) {
            try {
                return JSON.parse(saved).cols;
            }
            catch { /* ignore */ }
        }
        return 2;
    });
    const [mcRows, setMcRows] = useState(() => {
        const saved = localStorage.getItem('sporemind:multiconsole-grid-config');
        if (saved) {
            try {
                return JSON.parse(saved).rows;
            }
            catch { /* ignore */ }
        }
        return 2;
    });
    return (_jsxs("div", { className: "titlebar", children: [_jsxs("div", { className: "tb-menu-section", ref: menuRef, children: [showShellModeSwitch && (_jsx("button", { className: `tb-shell-mode-switch ${isWorkbench ? 'ide' : 'shell'}`, type: "button", onClick: () => onShellModeChange?.(isWorkbench ? 'standalone-ai-shell' : 'workbench'), title: isWorkbench ? 'Workbench' : 'Shell', "aria-label": isWorkbench ? 'Switch to shell mode' : 'Switch to workbench mode', children: isWorkbench ? _jsx(Hexagon, { size: 14 }) : _jsx(Circle, { size: 14 }) })), showMenuItems && menuItems.map(item => (_jsxs("div", { className: "tb-menu-item-wrapper", children: [_jsx("button", { className: `tb-menu-item ${openMenuId === item.id ? 'open' : ''}`, onClick: () => handleMenuToggle(item.id), onPointerEnter: () => { if (openMenuId)
                                    setOpenMenuId(item.id); }, type: "button", children: item.label }), openMenuId === item.id && item.submenu && (_jsx(MenuList, { items: item.submenu, onItemClick: handleSubmenuClick }))] }, item.id)))] }), _jsx("div", { className: "tb-drag-region", onDoubleClick: handleDragDoubleClick, children: isMulticonsole && agentAvatars ? (_jsx("div", { className: "tb-multiconsole-avatars", children: agentAvatars })) : (title ? _jsx("span", { className: "tb-title", children: title }) : null) }), _jsx("div", { className: "tb-center-toggle", onDoubleClick: (event) => event.stopPropagation() }), _jsxs("div", { className: "tb-right-section", children: [showMcButton && (_jsxs("div", { className: "tb-multiconsole-wrapper", ref: mcDropdownRef, children: [_jsx("button", { className: `tb-multiconsole-btn ${isMulticonsole ? 'active' : ''}`, type: "button", onClick: () => {
                                    if (isMulticonsole) {
                                        setMcDropdownOpen(prev => !prev);
                                    }
                                    else {
                                        onShellModeChange?.('multiconsole');
                                    }
                                }, title: "Multiconsole", children: _jsxs("svg", { width: "14", height: "14", viewBox: "0 0 14 14", fill: "none", stroke: "currentColor", strokeWidth: "1.2", children: [_jsx("rect", { x: "1", y: "1", width: "5", height: "5", rx: "1" }), _jsx("rect", { x: "8", y: "1", width: "5", height: "5", rx: "1" }), _jsx("rect", { x: "1", y: "8", width: "5", height: "5", rx: "1" }), _jsx("rect", { x: "8", y: "8", width: "5", height: "5", rx: "1" })] }) }), mcDropdownOpen && isMulticonsole && (_jsx("div", { className: "tb-multiconsole-dropdown", children: _jsxs("div", { className: "tb-mc-selector", children: [_jsxs("div", { className: "tb-mc-slider-row", children: [_jsx("span", { className: "tb-mc-slider-label", children: "Width" }), _jsx("div", { className: "tb-mc-slider-track", children: [1, 2, 3, 4, 5].map(v => (_jsx("button", { className: `tb-mc-slider-dot ${mcCols >= v ? 'active' : ''}`, type: "button", onClick: () => {
                                                            setMcCols(v);
                                                            window.dispatchEvent(new CustomEvent('sporemind:multiconsole-grid', { detail: { cols: v, rows: mcRows } }));
                                                        }, title: `${v}` }, v))) }), _jsx("span", { className: "tb-mc-slider-value", children: mcCols })] }), _jsxs("div", { className: "tb-mc-slider-row", children: [_jsx("span", { className: "tb-mc-slider-label", children: "Height" }), _jsx("div", { className: "tb-mc-slider-track", children: [1, 2, 3, 4, 5].map(v => (_jsx("button", { className: `tb-mc-slider-dot ${mcRows >= v ? 'active' : ''}`, type: "button", onClick: () => {
                                                            setMcRows(v);
                                                            window.dispatchEvent(new CustomEvent('sporemind:multiconsole-grid', { detail: { cols: mcCols, rows: v } }));
                                                        }, title: `${v}` }, v))) }), _jsx("span", { className: "tb-mc-slider-value", children: mcRows })] })] }) }))] })), rightSlot, _jsxs("div", { className: "tb-window-controls", children: [_jsx("button", { className: "tb-win-btn", onClick: handleMinimise, title: "Minimize", type: "button", children: _jsx(Minus, { size: 14 }) }), _jsx("button", { className: "tb-win-btn", onClick: handleToggleMaximise, title: maximised ? 'Restore' : 'Maximize', type: "button", children: maximised
                                    ? _jsxs("svg", { width: "14", height: "14", viewBox: "0 0 14 14", fill: "none", stroke: "currentColor", strokeWidth: "1.2", children: [_jsx("rect", { x: "2.5", y: "4", width: "8", height: "8", rx: "1" }), _jsx("path", { d: "M4.5 4V2.5a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1H10" })] })
                                    : _jsx(Square, { size: 12 }) }), _jsx("button", { className: "tb-win-btn close", onClick: handleClose, title: "Close", type: "button", children: _jsx(X, { size: 14 }) })] })] })] }));
};
