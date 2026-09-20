import { jsx as _jsx, jsxs as _jsxs } from "react/jsx-runtime";
import { Settings } from 'lucide-react';
import './ActivityBar.css';
// Zones for each section
const TOP_ZONES = new Set(['left-top', 'right-top']);
const MIDDLE_ZONES = new Set(['left-bottom', 'right-bottom']);
const BOTTOM_ZONES = new Set(['bottom-left', 'bottom-right']);
export const ActivityBar = ({ items, onToggle, position = 'left', showSettings = false, }) => {
    const topItems = items.filter(item => item.dockZone && TOP_ZONES.has(item.dockZone));
    const middleItems = items.filter(item => item.dockZone && MIDDLE_ZONES.has(item.dockZone));
    const bottomItems = items.filter(item => item.dockZone && BOTTOM_ZONES.has(item.dockZone));
    const renderBtn = (item) => (_jsx("button", { className: `ab-btn ${item.visible ? 'active' : ''}`, onClick: () => onToggle(item.id), title: item.label, children: item.icon }, item.id));
    return (_jsxs("div", { className: `activity-bar ${position}`, children: [_jsxs("div", { className: "ab-upper", children: [_jsx("div", { className: "ab-top", children: topItems.map(renderBtn) }), _jsx("div", { className: "ab-middle", children: middleItems.reverse().map(renderBtn) })] }), _jsxs("div", { className: "ab-lower", children: [bottomItems.map(renderBtn), showSettings && (_jsx("button", { className: "ab-btn", title: "Settings", disabled: true, children: _jsx(Settings, { size: 16 }) }))] })] }));
};
