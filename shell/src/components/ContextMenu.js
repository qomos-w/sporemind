import { jsx as _jsx, jsxs as _jsxs } from "react/jsx-runtime";
import { useEffect, useRef, useCallback } from 'react';
import './ContextMenu.css';
export const ContextMenu = ({ items, x, y, onClose }) => {
    const ref = useRef(null);
    const handleClickOutside = useCallback((e) => {
        if (ref.current && !ref.current.contains(e.target)) {
            onClose();
        }
    }, [onClose]);
    useEffect(() => {
        document.addEventListener('mousedown', handleClickOutside, true);
        return () => document.removeEventListener('mousedown', handleClickOutside, true);
    }, [handleClickOutside]);
    // Clamp to viewport
    const style = {
        left: Math.min(x, window.innerWidth - 200),
        top: Math.min(y, window.innerHeight - items.length * 28 - 8),
    };
    return (_jsx("div", { className: "ctx-menu", ref: ref, style: style, children: items.map(item => {
            if (item.separator) {
                return _jsx("div", { className: "ctx-menu-sep" }, item.id);
            }
            return (_jsxs("button", { className: "ctx-menu-item", onClick: () => {
                    item.onClick?.();
                    onClose();
                }, children: [_jsx("span", { className: "ctx-menu-check", children: item.checked !== undefined ? (item.checked ? '✓' : '') : '' }), _jsx("span", { className: "ctx-menu-label", children: item.label })] }, item.id));
        }) }));
};
