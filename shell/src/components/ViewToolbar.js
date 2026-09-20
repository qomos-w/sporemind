import { jsx as _jsx } from "react/jsx-runtime";
import './ViewToolbar.css';
export const ViewToolbar = ({ children }) => (_jsx("div", { className: "view-toolbar", children: children }));
export const VTBtn = ({ title, active, disabled, onClick, children }) => (_jsx("button", { className: `vt-btn${active ? ' active' : ''}`, title: title, disabled: disabled, onClick: onClick, children: children }));
export const VTSep = () => _jsx("div", { className: "vt-sep" });
