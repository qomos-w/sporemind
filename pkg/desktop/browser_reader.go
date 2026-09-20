package desktop

import (
	"encoding/json"
	"sync"
	"time"
)

// PageSnapshot holds the latest DOM/cookie/storage data captured from a browser window.
type PageSnapshot struct {
	URL            string            `json:"url"`
	Title          string            `json:"title"`
	Text           string            `json:"text"`
	HTML           string            `json:"html"`
	Cookies        string            `json:"cookies"`
	LocalStorage   map[string]string `json:"local_storage"`
	SessionStorage map[string]string `json:"session_storage"`
	CapturedAt     time.Time         `json:"captured_at"`
}

// BrowserUseResult is the raw result from a browser-use action in the webview.
type BrowserUseResult struct {
	Success       bool
	Message       string
	ObservationID string
}

// snapshotStore keeps the latest PageSnapshot per browser instance, keyed by instance ID.
// It also tracks pending observe/use callbacks keyed by observation ID.
type snapshotStore struct {
	mu               sync.RWMutex
	snapshots        map[string]*PageSnapshot
	pendingCallbacks map[string]func(BrowserUseResult)
}

func newSnapshotStore() *snapshotStore {
	return &snapshotStore{
		snapshots:        make(map[string]*PageSnapshot),
		pendingCallbacks: make(map[string]func(BrowserUseResult)),
	}
}

func (s *snapshotStore) set(id string, snap PageSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[id] = &snap
}

func (s *snapshotStore) get(id string) *PageSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshots[id]
}

// RegisterCallback registers a callback to be invoked when a browser-use result
// with the given observation ID arrives. Returns a cancel function.
func (s *snapshotStore) RegisterCallback(obsID string, cb func(BrowserUseResult)) func() {
	s.mu.Lock()
	s.pendingCallbacks[obsID] = cb
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.pendingCallbacks, obsID)
		s.mu.Unlock()
	}
}

// readerScript is injected into browser windows. It reads DOM text/HTML,
// cookies, and localStorage/sessionStorage, then sends the result back to Go
// via chrome.webview.postMessage. Runs on demand when triggered by ExecJS.
const readerScript = `(function() {
    try {
        var ls = {};
        try {
            for (var i = 0; i < localStorage.length; i++) {
                var k = localStorage.key(i);
                ls[k] = localStorage.getItem(k);
            }
        } catch(e) {}

        var ss = {};
        try {
            for (var i = 0; i < sessionStorage.length; i++) {
                var k = sessionStorage.key(i);
                ss[k] = sessionStorage.getItem(k);
            }
        } catch(e) {}

        var bodyText = '';
        try { bodyText = document.body ? document.body.innerText : ''; } catch(e) {}

        var html = '';
        try { html = document.documentElement ? document.documentElement.outerHTML : ''; } catch(e) {}
        // Truncate huge HTML to avoid choking the message channel.
        if (html.length > 500000) html = html.slice(0, 500000);

        var cookies = '';
        try { cookies = document.cookie || ''; } catch(e) {}

        var msg = JSON.stringify({
            type: 'page-snapshot',
            url: location.href,
            title: document.title,
            text: bodyText,
            html: html,
            cookies: cookies,
            local_storage: ls,
            session_storage: ss
        });
        window.chrome.webview.postMessage('wails:' + msg);
    } catch(e) {
        try {
            window.chrome.webview.postMessage('wails:' + JSON.stringify({type:'page-snapshot', error: e.message}));
        } catch(e2) {}
    }
})();`

// observeScript extracts page state and interactive elements, injects data-sporemind-ref
// attributes for stable element referencing, and posts the result back to Go via
// chrome.webview.postMessage. It installs the global __sporemindObserve(obsId)
// entrypoint: Go invokes it with the correlation ID it registered in
// observeCallbacks, and the script echoes it back so handleBrowserObserveMessage
// can match the pending Observe call (a self-generated ID never matches — the
// correlation miss left every observe to time out). The obsId argument falls
// back to a self-generated value only when Go omits it. This script is
// constrained: it only reads DOM properties and does not execute arbitrary code.
const observeScript = `window.__sporemindObserve = function(obsIdArg) {
    try {
        var idx = 0;
        var elements = [];

        // Collect interactive elements from a document, then recurse into all
        // same-origin iframes so embedded widgets (login forms, editors, chat
        // panes) are observable and addressable via the same ref scheme.
        function collectFrom(doc, depth) {
            var interactives = null;
            try {
                interactives = doc.querySelectorAll(
                    'a,button,input,textarea,select,[contenteditable],[role="button"],[role="link"],' +
                    '[role="textbox"],[role="checkbox"],[role="radio"],[role="menuitem"],' +
                    '[role="menu"],[role="tab"],[role="treeitem"],[role="option"]'
                );
            } catch(e) { return; }
            interactives.forEach(function(el) {
            var ref = 'e' + (++idx);
            el.setAttribute('data-sporemind-ref', ref);

            var rect = {x:0,y:0,width:0,height:0};
            try { var r = el.getBoundingClientRect(); rect = {x:Math.round(r.left),y:Math.round(r.top),width:Math.round(r.width),height:Math.round(r.height)}; } catch(e){}

            var style = null;
            try { style = window.getComputedStyle(el); } catch(e){}

            var tag = '';
            try { tag = el.tagName.toLowerCase(); } catch(e){}
            var role = '';
            try { role = el.getAttribute('role') || ''; } catch(e){}
            var text = '';
            try { text = (el.textContent || '').replace(/\\s+/g, ' ').trim().slice(0, 500); } catch(e){}
            var placeholder = '';
            try { placeholder = el.getAttribute('placeholder') || ''; } catch(e){}
            var ariaLabel = '';
            try { ariaLabel = el.getAttribute('aria-label') || ''; } catch(e){}
            var value = '';
            try { value = (el.value !== undefined ? String(el.value) : ''); } catch(e){}
            var href = '';
            try { href = el.href || ''; } catch(e){}
            var src = '';
            try { src = el.src || ''; } catch(e){}
            var isVisible = rect.width > 0 && rect.height > 0;
            try { isVisible = isVisible && !(style && (style.display === 'none' || style.visibility === 'hidden')); } catch(e){}
            var isEnabled = true;
            try { isEnabled = !(el.disabled || el.hasAttribute('aria-disabled') && el.getAttribute('aria-disabled') === 'true'); } catch(e){}
            var isFocusable = false;
            try { isFocusable = !!(el.tabIndex >= 0 || el.tagName === 'A' || el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.tagName === 'SELECT'); } catch(e){}
            var isChecked = false;
            try { if (tag === 'input') { var t = el.type; isChecked = (t === 'checkbox' || t === 'radio') && el.checked; } } catch(e){}
            var inputType = '';
            try { if (tag === 'input') inputType = el.type; } catch(e){}
            var fontSize = 0;
            try { if (style) fontSize = parseFloat(style.fontSize) || 0; } catch(e){}
            var fontWeight = 0;
            try { if (style) fontWeight = parseFloat(style.fontWeight) || 0; } catch(e){}
            var color = '';
            try { if (style) color = style.color || ''; } catch(e){}
            var bgColor = '';
            try { if (style) bgColor = style.backgroundColor || ''; } catch(e){}
            var sel = '';
            try {
                if (el.id) sel = '#' + el.id;
                else {
                    var parts = [];
                    var cur = el;
                    while (cur && cur !== doc.body && parts.length < 5) {
                        var s = cur.tagName.toLowerCase();
                        if (cur.id) { s += '#' + cur.id; parts.unshift(s); break; }
                        else {
                            var siblings = Array.from(cur.parentElement ? cur.parentElement.children : []).filter(function(c){return c.tagName === cur.tagName});
                            s += ':nth-of-type(' + (siblings.indexOf(cur) + 1) + ')';
                            parts.unshift(s);
                            cur = cur.parentElement;
                        }
                    }
                    sel = parts.join(' > ');
                }
            } catch(e){ sel = ''; }

            var actionHint = '';
            if (tag === 'a') actionHint = 'navigate';
            else if (tag === 'button' || role === 'button') actionHint = 'click';
            else if (tag === 'input' || tag === 'textarea' || role === 'textbox') actionHint = 'type';
            else if (tag === 'select') actionHint = 'select';
            else if (el.getAttribute('contenteditable') !== null) actionHint = 'type';

            elements.push({
                Id: ref,
                TagName: tag,
                Selector: sel,
                XPath: '',
                Rect: rect,
                Text: text,
                Placeholder: placeholder,
                Role: role,
                AriaLabel: ariaLabel,
                IsVisible: isVisible,
                IsEnabled: isEnabled,
                IsFocusable: isFocusable,
                IsChecked: isChecked,
                InputType: inputType,
                Value: value.slice(0, 200),
                Src: src,
                Href: href.slice(0, 500),
                FontSize: fontSize,
                FontWeight: fontWeight,
                Color: color,
                BackgroundColor: bgColor,
                ActionHint: actionHint,
                ActionConfidence: actionHint ? 0.9 : 0,
                ChildCount: el.children ? el.children.length : 0,
                SiblingIndex: 0,
                ParentId: ''
            });
        });

            // Recurse into same-origin frames; cross-origin access throws and
            // is skipped silently.
            if (depth <= 0) return;
            var frames = null;
            try { frames = doc.querySelectorAll('iframe'); } catch(e) { return; }
            for (var fi = 0; fi < frames.length; fi++) {
                try {
                    var fdoc = frames[fi].contentDocument;
                    if (fdoc && fdoc !== doc) collectFrom(fdoc, depth - 1);
                } catch(e) {}
            }
        }

        collectFrom(document, 3);

        var obsId = obsIdArg || ('obs-' + Date.now() + '-' + Math.random().toString(36).slice(2, 9));
        var readyState = 'complete';
        try { readyState = document.readyState; } catch(e){}
        var msg = JSON.stringify({
            type: 'browser-observe',
            obsId: obsId,
            url: location.href,
            title: document.title || '',
            viewportWidth: window.innerWidth,
            viewportHeight: window.innerHeight,
            scrollX: window.scrollX || window.pageXOffset || 0,
            scrollY: window.scrollY || window.pageYOffset || 0,
            loadState: readyState,
            elements: elements,
            totalElementCount: elements.length,
            timestamp: new Date().toISOString(),
            historyLength: window.history ? window.history.length : 1
        });
        window.chrome.webview.postMessage('wails:' + msg);
    } catch(e) {
        try {
            window.chrome.webview.postMessage('wails:' + JSON.stringify({type:'browser-observe', obsId: obsIdArg || '', error: e ? e.message : String(e)}));
        } catch(e2){}
    }
};`

// useScript executes a constrained DOM action
// (click/type/scroll/scroll_to/wait/navigate/press/select/hover/double_click/
// right_click/drag/focus) on the page. It receives a JSON payload from Go via
// window.__sporemindUse and posts the result back via chrome.webview.postMessage.
// This script is never exposed to agents; Go constructs and injects it based on
// the structured BrowserUseReq. URL scheme for navigate is validated Go-side
// before reaching this script.
const useScript = `(function() {
    window.__sporemindUse = window.__sporemindUse || {};
    window.__sporemindUse.run = function(payload) {
        var obsId = payload.obsId || ('use-' + Date.now() + '-' + Math.random().toString(36).slice(2, 9));
        var result = {type: 'browser-use-result', obsId: obsId, success: false, message: ''};

        // Walk the top document plus all same-origin iframe documents so
        // elements inside embedded frames are locatable by ref or selector.
        function allDocs() {
            var docs = [];
            function walk(doc, depth) {
                docs.push(doc);
                if (depth <= 0) return;
                var frames = null;
                try { frames = doc.querySelectorAll('iframe'); } catch(e) { return; }
                for (var fi = 0; fi < frames.length; fi++) {
                    try {
                        var fdoc = frames[fi].contentDocument;
                        if (fdoc) walk(fdoc, depth - 1);
                    } catch(e) {}
                }
            }
            walk(document, 3);
            return docs;
        }
        function findEl(ref) {
            var dlist = allDocs();
            for (var di = 0; di < dlist.length; di++) {
                try {
                    var hit = dlist[di].querySelector('[data-sporemind-ref="' + ref + '"]');
                    if (hit) return hit;
                } catch(e) {}
            }
            return null;
        }
        function querySel(sel) {
            var dlist = allDocs();
            for (var di = 0; di < dlist.length; di++) {
                try {
                    var hit = dlist[di].querySelector(sel);
                    if (hit) return hit;
                } catch(e) {}
            }
            return null;
        }

        try {
            var action = payload.action || '';
            var el = null;

            // Locate element by ref or selector (searches same-origin iframes too)
            if (payload.elementId) {
                el = findEl(payload.elementId);
            } else if (payload.elementSelector) {
                try { el = querySel(payload.elementSelector); } catch(e){}
            }

            switch(action) {
                case 'click':
                    if (!el) { result.message = 'element not found'; break; }
                    if (payload.clickX != null && payload.clickY != null) {
                        var synthetic = new MouseEvent('click', {clientX: payload.clickX, clientY: payload.clickY, bubbles: true, cancelable: true});
                        document.elementFromPoint(payload.clickX, payload.clickY).dispatchEvent(synthetic);
                    } else {
                        el.dispatchEvent(new MouseEvent('click', {bubbles: true, cancelable: true}));
                        el.dispatchEvent(new MouseEvent('mouseover', {bubbles: true, cancelable: true}));
                    }
                    result.success = true;
                    result.message = 'clicked ' + (payload.elementId || 'at (' + payload.clickX + ',' + payload.clickY + ')');
                    break;

                case 'type':
                    if (!el) { result.message = 'element not found'; break; }
                    var tag = el.tagName ? el.tagName.toLowerCase() : '';
                    var isContentEditable = el.getAttribute('contenteditable') !== null;
                    if (tag === 'input' || tag === 'textarea' || isContentEditable) {
                        if (!payload.append) {
                            try { el.focus(); } catch(e){}
                            try { if (tag === 'input' || tag === 'textarea') el.value = ''; } catch(e){}
                            try { document.execCommand('selectAll', false, null); document.execCommand('delete', false, null); } catch(e){}
                        }
                        var text = payload.text || '';
                        for (var i = 0; i < text.length; i++) {
                            var evt = document.createEvent('TextEvent');
                            evt.initTextEvent('textInput', true, true, null, text[i]);
                            el.dispatchEvent(evt);
                        }
                        if (tag === 'input' || tag === 'textarea') el.value = (payload.append ? (el.value || '') : '') + text;
                        if (isContentEditable) el.textContent = (payload.append ? (el.textContent || '') : '') + text;
                        result.success = true;
                        result.message = 'typed into ' + (payload.elementId || 'element');
                        if (payload.submit) {
                            var form = el.form;
                            if (form) {
                                form.dispatchEvent(new Event('submit', {bubbles: true, cancelable: true}));
                            } else {
                                el.dispatchEvent(new KeyboardEvent('keydown', {key: 'Enter', keyCode: 13, which: 13, bubbles: true}));
                            }
                            result.message += ' (submitted)';
                        }
                    } else {
                        result.message = 'element does not accept text input';
                    }
                    break;

                case 'scroll':
                    if (payload.elementId && el) {
                        el.scrollLeft = (el.scrollLeft || 0) + (payload.scrollX || 0);
                        el.scrollTop = (el.scrollTop || 0) + (payload.scrollY || 0);
                    } else {
                        window.scrollBy(payload.scrollX || 0, payload.scrollY || 0);
                    }
                    result.success = true;
                    result.message = 'scrolled to (' + window.scrollX + ',' + window.scrollY + ')';
                    break;

                case 'wait':
                    var waitFor = payload.waitFor || '';
                    var waitTimeout = payload.timeoutMs || 10000;

                    function postWait(ok, msg) {
                        var doneMsg = JSON.stringify({
                            type: 'browser-use-result',
                            obsId: obsId,
                            success: ok,
                            message: msg
                        });
                        try { window.chrome.webview.postMessage('wails:' + doneMsg); } catch(e) {}
                    }

                    if (waitFor === 'element') {
                        var waitRef = payload.waitElementId || '';
                        if (!waitRef) { result.message = 'wait: waitFor=element requires waitElementId'; break; }
                        if (findEl(waitRef)) { postWait(true, 'element "' + waitRef + '" present'); return; }
                        var elWaitMo = null;
                        var elWaitPoll = null;
                        var elWaitTimer = null;
                        var elWaitDone = false;
                        function elWaitCleanup() {
                            if (elWaitMo) { try { elWaitMo.disconnect(); } catch(e){} }
                            if (elWaitPoll) clearInterval(elWaitPoll);
                            if (elWaitTimer) clearTimeout(elWaitTimer);
                            elWaitMo = null; elWaitPoll = null; elWaitTimer = null;
                        }
                        function elWaitCheck() {
                            if (elWaitDone) return;
                            if (findEl(waitRef)) {
                                elWaitDone = true;
                                elWaitCleanup();
                                postWait(true, 'element "' + waitRef + '" appeared');
                            }
                        }
                        function elWaitTimeout() {
                            if (elWaitDone) return;
                            if (findEl(waitRef)) { elWaitCheck(); return; }
                            elWaitDone = true;
                            elWaitCleanup();
                            postWait(false, 'wait: element "' + waitRef + '" not found within ' + waitTimeout + 'ms');
                        }
                        try { elWaitMo = new MutationObserver(function() { elWaitCheck(); }); } catch(e){}
                        var waitDocs = allDocs();
                        for (var wdi = 0; wdi < waitDocs.length; wdi++) {
                            try {
                                elWaitMo.observe(waitDocs[wdi].documentElement || waitDocs[wdi], {childList: true, subtree: true, attributes: true});
                            } catch(e) {}
                        }
                        elWaitPoll = setInterval(elWaitCheck, 150);
                        elWaitTimer = setTimeout(elWaitTimeout, waitTimeout);
                        return;
                    }

                    if (waitFor === 'domready') {
                        var drDone = false;
                        var drPoll = null;
                        function drReady() {
                            var rs = 'loading';
                            try { rs = document.readyState; } catch(e){}
                            return rs === 'complete';
                        }
                        function drFinish() {
                            if (drDone) return;
                            drDone = true;
                            if (drPoll) clearInterval(drPoll);
                            document.removeEventListener('readystatechange', drCheck);
                            var ready = drReady();
                            postWait(ready, ready ? 'dom ready' : 'wait: dom not ready within ' + waitTimeout + 'ms');
                        }
                        function drCheck() {
                            if (drReady()) drFinish();
                        }
                        if (drReady()) { postWait(true, 'dom ready'); return; }
                        document.addEventListener('readystatechange', drCheck);
                        drPoll = setInterval(drCheck, 100);
                        setTimeout(drFinish, waitTimeout);
                        return;
                    }

                    if (waitFor === 'networkidle') {
                        var idleWindow = 500;
                        var niLast = Date.now();
                        var niDone = false;
                        var niPoll = null;
                        var niObs = null;
                        function niMark() { niLast = Date.now(); }
                        function niFinish() {
                            if (niDone) return;
                            niDone = true;
                            if (niPoll) clearInterval(niPoll);
                            if (niObs) { try { niObs.disconnect(); } catch(e){} }
                            var idle = (Date.now() - niLast) >= idleWindow;
                            postWait(idle, idle ? 'network idle' : 'wait: network not idle within ' + waitTimeout + 'ms');
                        }
                        try {
                            niObs = new PerformanceObserver(function(list) {
                                var entries = list.getEntries();
                                for (var ei = 0; ei < entries.length; ei++) niMark();
                            });
                            niObs.observe({entryTypes: ['resource']});
                        } catch(e) {}
                        niPoll = setInterval(function() {
                            if ((Date.now() - niLast) >= idleWindow) niFinish();
                        }, 100);
                        setTimeout(niFinish, waitTimeout);
                        return;
                    }

                    var waitMs = payload.waitMs || 0;
                    if (waitMs > 0) {
                        setTimeout(function() { postWait(true, 'waited ' + waitMs + 'ms'); }, waitMs);
                        return; // async wait; short-circuit return
                    }
                    result.success = true;
                    result.message = 'wait complete';
                    break;

                case 'navigate':
                    var mode = payload.navigateMode || 'navigate';
                    // Post the result before leaving the page: once the
                    // navigation commits the document is torn down, and a
                    // postMessage fired after it can be lost, leaving the
                    // Go-side callback waiting for the full 15s deadline.
                    result.success = true;
                    result.message = 'navigate: ' + mode + (payload.url ? ' ' + payload.url : '');
                    window.chrome.webview.postMessage('wails:' + JSON.stringify(result));
                    if (mode === 'back') {
                        history.back();
                    } else if (mode === 'forward') {
                        history.forward();
                    } else if (mode === 'reload') {
                        location.reload();
                    } else {
                        var navUrl = payload.url || '';
                        if (!navUrl) {
                            result.success = false;
                            result.message = 'navigate: url required';
                            window.chrome.webview.postMessage('wails:' + JSON.stringify(result));
                            return;
                        }
                        window.location.href = navUrl;
                    }
                    return;

                case 'press':
                    var pressKey = payload.key || '';
                    if (!pressKey) { result.message = 'press: key required'; break; }
                    var pressTarget = el || document.activeElement || document.body;
                    if (!pressTarget) { result.message = 'press: no element to dispatch on'; break; }
                    var modifiers = payload.modifiers || [];
                    var keyInit = {key: pressKey, bubbles: true, cancelable: true, ctrlKey: false, shiftKey: false, altKey: false, metaKey: false};
                    for (var mi = 0; mi < modifiers.length; mi++) {
                        var mod = String(modifiers[mi]).toLowerCase();
                        if (mod === 'ctrl' || mod === 'control') keyInit.ctrlKey = true;
                        else if (mod === 'shift') keyInit.shiftKey = true;
                        else if (mod === 'alt' || mod === 'option') keyInit.altKey = true;
                        else if (mod === 'meta' || mod === 'cmd' || mod === 'command' || mod === 'win') keyInit.metaKey = true;
                    }
                    pressTarget.dispatchEvent(new KeyboardEvent('keydown', keyInit));
                    pressTarget.dispatchEvent(new KeyboardEvent('keypress', keyInit));
                    pressTarget.dispatchEvent(new KeyboardEvent('keyup', keyInit));
                    result.success = true;
                    result.message = 'pressed "' + pressKey + '" on ' + (payload.elementId || 'active element') + (modifiers.length ? ' with modifiers' : '');
                    break;

                case 'select':
                    if (!el) { result.message = 'element not found'; break; }
                    var selTag = el.tagName ? el.tagName.toLowerCase() : '';
                    if (selTag !== 'select') { result.message = 'select: target is not a <select> element'; break; }
                    var selText = payload.text || '';
                    var selValue = payload.value || '';
                    var selMatched = false;
                    if (selText) {
                        for (var oi = 0; oi < el.options.length; oi++) {
                            var opt = el.options[oi];
                            var optText = '';
                            try { optText = (opt.text || '').replace(/\s+/g, ' ').trim(); } catch(e){}
                            if (optText === selText) {
                                el.value = opt.value;
                                try { opt.selected = true; } catch(e){}
                                selMatched = true;
                                break;
                            }
                        }
                        if (!selMatched) { result.message = 'select: no option with text "' + selText + '"'; break; }
                    } else {
                        el.value = selValue;
                    }
                    el.dispatchEvent(new Event('input', {bubbles: true}));
                    el.dispatchEvent(new Event('change', {bubbles: true}));
                    result.success = true;
                    result.message = 'selected "' + (selText || selValue) + '" in ' + (payload.elementId || 'element');
                    break;

                case 'hover':
                    if (!el) { result.message = 'element not found'; break; }
                    el.dispatchEvent(new MouseEvent('mouseover', {bubbles: true, cancelable: true}));
                    el.dispatchEvent(new MouseEvent('mousemove', {bubbles: true, cancelable: true}));
                    el.dispatchEvent(new MouseEvent('mouseenter', {bubbles: false, cancelable: false}));
                    result.success = true;
                    result.message = 'hovered ' + (payload.elementId || 'element');
                    break;

                case 'double_click':
                    if (!el) { result.message = 'element not found'; break; }
                    el.dispatchEvent(new MouseEvent('mousedown', {bubbles: true, cancelable: true, detail: 1}));
                    el.dispatchEvent(new MouseEvent('mouseup', {bubbles: true, cancelable: true, detail: 1}));
                    el.dispatchEvent(new MouseEvent('click', {bubbles: true, cancelable: true, detail: 1}));
                    el.dispatchEvent(new MouseEvent('mousedown', {bubbles: true, cancelable: true, detail: 2}));
                    el.dispatchEvent(new MouseEvent('mouseup', {bubbles: true, cancelable: true, detail: 2}));
                    el.dispatchEvent(new MouseEvent('click', {bubbles: true, cancelable: true, detail: 2}));
                    el.dispatchEvent(new MouseEvent('dblclick', {bubbles: true, cancelable: true, detail: 2}));
                    result.success = true;
                    result.message = 'double-clicked ' + (payload.elementId || 'element');
                    break;

                case 'right_click':
                    if (!el) { result.message = 'element not found'; break; }
                    el.dispatchEvent(new MouseEvent('mousedown', {bubbles: true, cancelable: true, button: 2, buttons: 2, detail: 1}));
                    el.dispatchEvent(new MouseEvent('mouseup', {bubbles: true, cancelable: true, button: 2, buttons: 0, detail: 1}));
                    el.dispatchEvent(new MouseEvent('contextmenu', {bubbles: true, cancelable: true, button: 2, buttons: 0, detail: 1}));
                    result.success = true;
                    result.message = 'right-clicked ' + (payload.elementId || 'element');
                    break;

                case 'drag':
                    if (!el) { result.message = 'element not found'; break; }
                    var dragTarget = null;
                    if (payload.dragTargetElementId) dragTarget = findEl(payload.dragTargetElementId);
                    if (!dragTarget && (payload.clickX == null || payload.clickY == null)) {
                        result.message = 'drag: dragTargetElementId or clickX/clickY required';
                        break;
                    }
                    var sRect = el.getBoundingClientRect();
                    var sX = sRect.left + sRect.width / 2;
                    var sY = sRect.top + sRect.height / 2;
                    var dX, dY, dropEl;
                    if (dragTarget) {
                        var dRect = dragTarget.getBoundingClientRect();
                        dX = dRect.left + dRect.width / 2;
                        dY = dRect.top + dRect.height / 2;
                        dropEl = dragTarget;
                    } else {
                        dX = payload.clickX;
                        dY = payload.clickY;
                        dropEl = null;
                        try { dropEl = document.elementFromPoint(dX, dY); } catch(e){}
                        if (!dropEl) dropEl = document.body;
                    }
                    try { el.focus(); } catch(e){}
                    el.dispatchEvent(new MouseEvent('mousedown', {bubbles: true, cancelable: true, clientX: sX, clientY: sY, button: 0, buttons: 1, detail: 1}));
                    if (typeof DragEvent !== 'undefined' && typeof DataTransfer !== 'undefined') {
                        try {
                            var dt = new DataTransfer();
                            el.dispatchEvent(new DragEvent('dragstart', {bubbles: true, cancelable: true, dataTransfer: dt, clientX: sX, clientY: sY}));
                            el.dispatchEvent(new DragEvent('drag', {bubbles: true, cancelable: true, dataTransfer: dt, clientX: sX, clientY: sY}));
                            dropEl.dispatchEvent(new DragEvent('dragenter', {bubbles: true, cancelable: true, dataTransfer: dt, clientX: dX, clientY: dY}));
                            dropEl.dispatchEvent(new DragEvent('dragover', {bubbles: true, cancelable: true, dataTransfer: dt, clientX: dX, clientY: dY}));
                            dropEl.dispatchEvent(new DragEvent('drop', {bubbles: true, cancelable: true, dataTransfer: dt, clientX: dX, clientY: dY}));
                            el.dispatchEvent(new DragEvent('dragend', {bubbles: true, cancelable: true, dataTransfer: dt, clientX: dX, clientY: dY}));
                        } catch(eDrag) {}
                    } else {
                        dropEl.dispatchEvent(new MouseEvent('mousemove', {bubbles: true, cancelable: true, clientX: dX, clientY: dY, button: 0, buttons: 1, detail: 1}));
                    }
                    dropEl.dispatchEvent(new MouseEvent('mouseup', {bubbles: true, cancelable: true, clientX: dX, clientY: dY, button: 0, buttons: 0, detail: 1}));
                    result.success = true;
                    result.message = 'dragged ' + (payload.elementId || 'element') + ' to ' + (dragTarget ? 'target element' : '(' + dX + ',' + dY + ')');
                    break;

                case 'scroll_to':
                    if (!el) { result.message = 'element not found'; break; }
                    try { el.scrollIntoView({behavior: 'smooth', block: 'center'}); } catch(e) {
                        try { el.scrollIntoView(true); } catch(e2) {}
                    }
                    result.success = true;
                    result.message = 'scrolled ' + (payload.elementId || 'element') + ' into view';
                    break;

                case 'focus':
                    if (!el) { result.message = 'element not found'; break; }
                    try { el.focus(); } catch(e) {}
                    result.success = true;
                    result.message = 'focused ' + (payload.elementId || 'element');
                    break;

                default:
                    result.message = 'unknown action: ' + action;
            }
        } catch(e) {
            result.message = 'error: ' + (e ? e.message : String(e));
        }
        window.chrome.webview.postMessage('wails:' + JSON.stringify(result));
    };
})();`

// handleBrowserUseResult parses a browser-use result postMessage and resolves
// the matching pending callback. Returns true if the message was handled.
func (store *snapshotStore) handleBrowserUseResult(message string) bool {
	var raw map[string]any
	if err := json.Unmarshal([]byte(message), &raw); err != nil {
		return false
	}
	if raw["type"] != "browser-use-result" {
		return false
	}
	obsID, _ := raw["obsId"].(string)
	store.mu.RLock()
	cb, ok := store.pendingCallbacks[obsID]
	store.mu.RUnlock()
	if !ok {
		return true // recognized but no pending request
	}
	store.mu.Lock()
	delete(store.pendingCallbacks, obsID)
	store.mu.Unlock()
	result := BrowserUseResult{
		Success:       getBool(raw, "success"),
		Message:       getString(raw, "message"),
		ObservationID: obsID,
	}
	cb(result)
	return true
}
func (store *snapshotStore) handleSnapshotMessage(instanceID, message string) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(message), &raw); err != nil {
		return
	}
	if raw["type"] != "page-snapshot" {
		return
	}

	snap := PageSnapshot{
		URL:        getString(raw, "url"),
		Title:      getString(raw, "title"),
		Text:       getString(raw, "text"),
		HTML:       getString(raw, "html"),
		Cookies:    getString(raw, "cookies"),
		CapturedAt: time.Now(),
	}
	snap.LocalStorage = getStringMap(raw, "local_storage")
	snap.SessionStorage = getStringMap(raw, "session_storage")
	store.set(instanceID, snap)
}

func getString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getStringMap(m map[string]any, key string) map[string]string {
	v, ok := m[key].(map[string]any)
	if !ok {
		return map[string]string{}
	}
	result := make(map[string]string, len(v))
	for k, val := range v {
		if s, ok := val.(string); ok {
			result[k] = s
		}
	}
	return result
}

func getBool(m map[string]any, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}
