// client.gen.ts — generated from appdef. DO NOT EDIT.
//
// Direct-HTTP data path: the plugin process serves this page same-origin, so
// every call below is a plain fetch/EventSource against the plugin's own
// listener (POST /invoke/{id}, GET /events SSE, static files). There is no
// base64 or binary codec on this path — large media goes over HTTP-native
// binary via uploadFile/streamVideo at the bottom of this file.
//
// Mount base: under the gateway the panel iframe loads from
// /plugin/{id}/ and the host shell injects window.__sporemindAppBase
// ("/plugin/{id}"); standalone dev leaves it unset ("") and URLs stay
// root-relative. appBase()/withAppBase() below apply the prefix, and every
// data call first awaits the __sporemindAppBaseReady handshake gate the
// injected snippet exposes — so a first fetch on page load cannot race the
// handshake and 404 at the gateway root.

// appBase resolves the mount base the host shell injects when the panel
// iframe is served through the gateway under /plugin/{id}/. Standalone dev
// (direct plugin listener) leaves it unset, so URLs stay root-relative. The
// value never carries a trailing slash: "" or "/plugin/{id}".
//
// Race gate: the shell injects the base via the postMessage bootstrap
// handshake, which can land AFTER this script runs. The injected snippet
// exposes window.__sporemindAppBaseReady — resolved when the handshake
// arrives (or "" after a bounded fallback) — and every data call below
// awaits it before building a URL, so the first fetch cannot 404 against
// the gateway root. Standalone dev pages without the snippet resolve
// immediately with the current global.
let appBaseGate: Promise<void> | null = null;
function ensureAppBaseReady(): Promise<void> {
	const w = window as any;
	if (!appBaseGate) {
		appBaseGate = w.__sporemindAppBaseReady instanceof Promise
			? w.__sporemindAppBaseReady.then(() => undefined)
			: Promise.resolve();
	}
	return appBaseGate;
}

async function appBase(): Promise<string> {
	await ensureAppBaseReady();
	return (window as any).__sporemindAppBase || "";
}

// withAppBase prefixes same-origin app paths ("/media/...") with the mount
// base; absolute URLs pass through untouched.
async function withAppBase(url: string): Promise<string> {
	return url.startsWith("/") ? (await appBase()) + url : url;
}

async function invoke<T>(callID: string, args: Record<string, unknown>): Promise<T> {
	const res = await fetch((await appBase()) + "/invoke/" + encodeURIComponent(callID), {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		credentials: "same-origin",
		body: JSON.stringify(args ?? {}),
	});
	if (!res.ok) {
		let detail = res.statusText;
		try {
			detail = ((await res.json()) as { error?: string }).error ?? detail;
		} catch {
			// non-JSON error body: keep statusText
		}
		throw new Error("invoke " + callID + " failed: " + res.status + " " + detail);
	}
	return (await res.json()) as T;
}

// invokeStream is the streaming-adjacent shape kept for source compatibility
// with streaming callables: the direct-HTTP invoke route is unary, so
// intermediate chunks are NOT relayed (the server-side emit drops them).
// The promise resolves with the terminal response. Model stream progress
// with app events over SSE (on<Event> below) instead of chunk callbacks.
async function invokeStream<T, C>(callID: string, args: Record<string, unknown>, _onChunk: (c: C) => void): Promise<T> {
	return invoke<T>(callID, args);
}

// ── SSE event subscription (GET /events, filtered by event kind) ──
//
// One shared EventSource per page: it attaches lazily on the first
// subscription and auto-reconnects natively after drops. The SDK SSE hub
// writes each event with its kind as the SSE "event:" field, so filtering is
// a plain addEventListener per kind — no demux parsing.
type EventHandler = (payload: unknown) => void;

const eventHandlers = new Map<string, Set<EventHandler>>();
let sharedEventSource: EventSource | null = null;
let eventSourceReady: Promise<EventSource> | null = null;
// attachedKinds prevents duplicate addEventListener when a kind is
// unsubscribed (map entry dropped) and later re-subscribed: the ES listener
// is never removed, so re-attaching would double-dispatch every event.
const attachedKinds = new Set<string>();

function ensureEventSource(): Promise<EventSource> {
	if (!eventSourceReady) {
		eventSourceReady = (async () => {
			// Await the appBase gate: subscribing on module load races the
			// bootstrap handshake, and a root-relative /events EventSource
			// 404s against the gateway.
			const base = await appBase();
			const es = new EventSource(base + "/events", { withCredentials: true });
			// Native behavior: EventSource reconnects automatically with the
			// server-provided retry delay. Nothing else to do on error.
			sharedEventSource = es;
			return es;
		})();
	}
	return eventSourceReady;
}

function subscribeEvent(eventKind: string, cb: EventHandler): () => void {
	let set = eventHandlers.get(eventKind);
	if (!set) {
		set = new Set();
		eventHandlers.set(eventKind, set);
		void ensureEventSource().then((es) => {
			if (!attachedKinds.has(eventKind)) {
				attachedKinds.add(eventKind);
				es.addEventListener(eventKind, (ev) => {
					const msg = ev as MessageEvent;
					let payload: unknown;
					if (typeof msg.data === "string" && msg.data !== "") {
						try {
							payload = JSON.parse(msg.data);
						} catch {
							payload = msg.data;
						}
					}
					eventHandlers.get(eventKind)?.forEach((h) => h(payload));
				});
			}
		});
	}
	set.add(cb);
	return () => {
		set!.delete(cb);
		if (set!.size === 0) {
			// Keep the shared EventSource alive across idle periods: closing
			// it would throw away the server's retry state and race in-flight
			// reconnects. It costs one idle connection per page.
			eventHandlers.delete(eventKind);
		}
	};
}
export interface ForcedToolRequest {
  Prompt: string;
  Provider?: string;
  Model?: string;
  Choice?: string;
  Mode?: string;
}

export interface ForcedToolResponse {
  ToolName: string;
  Arguments: string;
  StopInfo: string;
  Raw: string;
}

export async function forcedTool(req: ForcedToolRequest): Promise<ForcedToolResponse> {
	return invoke<ForcedToolResponse>("forced_tool", req as Record<string, unknown>);
}

// ── Binary helpers (HTTP-native, no base64/codec) ──
//
// Large media travels as raw HTTP bodies, not JSON: the session cookie is
// same-origin (HttpOnly, SameSite=Lax), so fetch, <video>, <audio> and <img>
// all carry it automatically.

// uploadFile POSTs a File/Blob as the raw request body to an app HTTP
// endpoint. When onProgress is given it uses XMLHttpRequest (fetch has no
// upload-progress event); otherwise it uses fetch. Returns the raw Response
// so callers can inspect status/headers.
export async function uploadFile(
	url: string,
	file: Blob,
	opts: { method?: string; headers?: Record<string, string>; onProgress?: (sent: number, total: number) => void } = {},
): Promise<Response> {
	url = await withAppBase(url);
	if (!opts.onProgress) {
		const res = await fetch(url, {
			method: opts.method ?? "POST",
			headers: opts.headers ?? {},
			credentials: "same-origin",
			body: file,
		});
		if (!res.ok) {
			throw new Error("upload to " + url + " failed: " + res.status + " " + res.statusText);
		}
		return res;
	}
	return new Promise<Response>((resolve, reject) => {
		const xhr = new XMLHttpRequest();
		xhr.open(opts.method ?? "POST", url);
		xhr.withCredentials = true;
		for (const [k, v] of Object.entries(opts.headers ?? {})) {
			xhr.setRequestHeader(k, v);
		}
		xhr.upload.onprogress = (ev) => {
			if (ev.lengthComputable) opts.onProgress!(ev.loaded, ev.total);
		};
		xhr.onload = () => {
			if (xhr.status >= 200 && xhr.status < 300) {
				resolve(new Response(xhr.response as ArrayBuffer, { status: xhr.status, statusText: xhr.statusText }));
			} else {
				reject(new Error("upload to " + url + " failed: " + xhr.status + " " + xhr.statusText));
			}
		};
		xhr.onerror = () => reject(new Error("upload to " + url + " failed: network error"));
		xhr.send(file);
	});
}

// streamVideo feeds an HTTP response (chunked or static) into a <video>
// element through Media Source Extensions, so playback starts while the body
// is still downloading. mimeType must match the container/codec actually
// served (e.g. "video/mp4; codecs=avc1.42E01E" for H.264 MP4).
export async function streamVideo(url: string, videoEl: HTMLVideoElement, mimeType = "video/mp4"): Promise<void> {
	url = await withAppBase(url);
	const mediaSource = new MediaSource();
	videoEl.src = URL.createObjectURL(mediaSource);
	await new Promise<void>((resolve) => {
		if (mediaSource.readyState === "open") {
			resolve();
		} else {
			mediaSource.addEventListener("sourceopen", () => resolve(), { once: true });
		}
	});
	const sourceBuffer = mediaSource.addSourceBuffer(mimeType);
	const res = await fetch(url, { credentials: "same-origin" });
	if (!res.ok || !res.body) {
		URL.revokeObjectURL(videoEl.src);
		throw new Error("stream " + url + " failed: " + res.status + " " + res.statusText);
	}
	const reader = res.body.getReader();
	try {
		for (;;) {
			const { done, value } = await reader.read();
			if (done) {
				break;
			}
			await new Promise<void>((resolve, reject) => {
				sourceBuffer.addEventListener("updateend", () => resolve(), { once: true });
				sourceBuffer.addEventListener("error", () => reject(new Error("MSE append failed")), { once: true });
				sourceBuffer.appendBuffer(value);
			});
		}
		if (mediaSource.readyState === "open") {
			mediaSource.endOfStream();
		}
	} finally {
		reader.releaseLock();
	}
}
