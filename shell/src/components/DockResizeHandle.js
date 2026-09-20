import { jsx as _jsx } from "react/jsx-runtime";
import { useCallback, useRef } from 'react';
import './Toolbar.css';
// ── Generic resize handle ──
//
// Performance: when `targetRef` is provided, the handle directly mutates the
// target element's style during drag via requestAnimationFrame, bypassing React
// state entirely. `onResize` is only called once on mouseup to commit the final
// value to React state. This prevents cascading re-renders of the entire layout
// on every pointermove.
export const DockResizeHandle = ({ direction, onResize, className, targetRef, negateDelta }) => {
    // Absolute drag state: captured at pointerdown, valid for entire gesture.
    const drag = useRef(null);
    const rafPending = useRef(false);
    // The last computed size, set in RAF, read on pointerup.
    const lastCommittedDelta = useRef(0);
    const scheduleDOMUpdate = useCallback((currentPos) => {
        if (!drag.current || !targetRef?.current)
            return;
        if (rafPending.current)
            return;
        rafPending.current = true;
        requestAnimationFrame(() => {
            rafPending.current = false;
            const d = drag.current;
            if (!d || !targetRef.current)
                return;
            const delta = (currentPos - d.startPos) * (negateDelta ? -1 : 1);
            lastCommittedDelta.current = delta;
            if (direction === 'horizontal') {
                targetRef.current.style.width = `${Math.max(160, d.startSize + delta)}px`;
            }
            else {
                targetRef.current.style.height = `${Math.max(120, d.startSize + delta)}px`;
            }
        });
    }, [direction, targetRef, negateDelta]);
    const onPointerDown = useCallback((e) => {
        e.preventDefault();
        e.stopPropagation();
        const startPos = direction === 'horizontal' ? e.clientX : e.clientY;
        // If we have a direct DOM target, store absolute start state
        if (targetRef?.current) {
            const el = targetRef.current;
            drag.current = {
                startPos,
                startSize: direction === 'horizontal' ? el.offsetWidth : el.offsetHeight,
            };
            lastCommittedDelta.current = 0;
        }
        else {
            // Fallback: incremental delta mode (old behavior)
            const startRef = { current: startPos };
            const onMove = (ev) => {
                if (startRef.current === null)
                    return;
                const current = direction === 'horizontal' ? ev.clientX : ev.clientY;
                const delta = current - startRef.current;
                startRef.current = current;
                onResize(delta);
            };
            const onUp = () => {
                startRef.current = null;
                document.removeEventListener('pointermove', onMove);
                document.removeEventListener('pointerup', onUp);
                document.removeEventListener('pointercancel', onUp);
            };
            document.addEventListener('pointermove', onMove);
            document.addEventListener('pointerup', onUp);
            document.addEventListener('pointercancel', onUp);
            return;
        }
        // targetRef mode: direct DOM manipulation, RAF-scheduled
        const onMove = (ev) => {
            const current = direction === 'horizontal' ? ev.clientX : ev.clientY;
            scheduleDOMUpdate(current);
        };
        const onUp = () => {
            const d = drag.current;
            if (d && targetRef?.current) {
                const currentSize = direction === 'horizontal' ? targetRef.current.offsetWidth : targetRef.current.offsetHeight;
                const delta = currentSize - d.startSize;
                onResize(delta);
            }
            drag.current = null;
            lastCommittedDelta.current = 0;
            document.removeEventListener('pointermove', onMove);
            document.removeEventListener('pointerup', onUp);
            document.removeEventListener('pointercancel', onUp);
        };
        document.addEventListener('pointermove', onMove);
        document.addEventListener('pointerup', onUp);
        document.addEventListener('pointercancel', onUp);
    }, [direction, onResize, targetRef, scheduleDOMUpdate]);
    return (_jsx("div", { className: `shell-resize-handle ${direction} ${className || ''}`, onPointerDown: onPointerDown }));
};
