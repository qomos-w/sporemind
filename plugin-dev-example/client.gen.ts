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
// with streaming callables: the HTTP invoke route is unary, so
// intermediate chunks are NOT relayed (the server-side emit drops them).
// The promise resolves with the terminal response. Model stream progress
// with app events over SSE (on<Event> below) instead of chunk callbacks.
async function invokeStream<T, C>(callID: string, args: Record<string, unknown>, _onChunk: (c: C) => void): Promise<T> {
	return invoke<T>(callID, args);
}

// ── Event subscription (GET /events: shared WebSocket, SSE fallback) ──
//
// Why WebSocket: the desktop SPA embeds one iframe per plugin panel, all
// pointing at the same gateway origin, and browsers cap same-pool HTTP/1.1
// connections at 6 per host — a permanent SSE stream per panel starves every
// transient invoke (2026-09-14 incident). WebSocket connections live outside
// that pool. If the upgrade handshake is refused (an older SDK answers a
// plain 200 stream), the first dial fails without ever opening and we degrade
// to EventSource for this page lifetime — never worse than before.
//
// The connection is a window-level singleton keyed by URL
// (window.__sporemindEventChannels) shared with the host-injected bridge
// client, which subscribes to the same event stream for the legacy
// sporemind.subscribe surface: whichever module runs first creates the
// channel, the other reuses it, and one panel never holds two connections
// to the same URL.
type EventHandler = (payload: unknown) => void;

const eventHandlers = new Map<string, Set<EventHandler>>();

function dispatchEvent(eventKind: string, payload: unknown) {
	eventHandlers.get(eventKind)?.forEach((h) => h(payload));
}

interface EventChannel {
	subscribe(eventKind: string, cb: (eventKind: string, payload: unknown) => void): () => void;
	/** Host-driven suspend: close any live connection and stop dialing while
	 * the panel is hidden; the channel and its subscribers stay intact. */
	suspend(): void;
	/** Resume after a suspend: re-dial if anyone still subscribes. */
	resume(): void;
}

function eventChannel(url: string): EventChannel {
	const w = window as any;
	if (!w.__sporemindEventChannels) w.__sporemindEventChannels = new Map();
	const existing = w.__sporemindEventChannels.get(url);
	if (existing) return existing as EventChannel;
	const ch = createEventChannel(url);
	w.__sporemindEventChannels.set(url, ch);
	return ch;
}

function eventBackoff(attempt: number): number {
	const exp = Math.min(30000, 500 * Math.pow(2, Math.min(attempt, 6)));
	return exp * (0.7 + 0.6 * Math.random());
}

function createEventChannel(url: string): EventChannel {
	const subs = new Map<string, Set<(eventKind: string, payload: unknown) => void>>();
	let stopped = false;
	let fellBack = false;
	let suspended = false;
	let everOpened = false;
	let attempt = 0;
	let ws: WebSocket | null = null;
	let es: EventSource | null = null;
	const attachedKinds = new Set<string>();

	const wsUrl = url.startsWith("/")
		? (location.protocol === "https:" ? "wss:" : "ws:") + "//" + location.host + url
		: url.replace(/^http/, "ws");

	const teardown = () => {
		stopped = true;
		fellBack = true;
		ws?.close();
		es?.close();
		const w = window as any;
		w.__sporemindEventChannels?.delete(url);
	};

	const attachSseKind = (target: EventSource, eventKind: string) => {
		if (attachedKinds.has(eventKind)) return;
		attachedKinds.add(eventKind);
		target.addEventListener(eventKind, (ev) => {
			const msg = ev as MessageEvent;
			let payload: unknown;
			if (typeof msg.data === "string" && msg.data !== "") {
				try {
					payload = JSON.parse(msg.data);
				} catch {
					payload = msg.data;
				}
			}
			subs.get(eventKind)?.forEach((cb) => cb(eventKind, payload));
		});
	};

	// dial connects the WS channel with capped exponential backoff and
	// jitter so a dead backend never produces a reconnect storm.
	const dial = () => {
		if (stopped || fellBack || suspended) return;
		let sock: WebSocket;
		try {
			sock = new WebSocket(wsUrl);
		} catch {
			attempt++;
			setTimeout(dial, eventBackoff(attempt));
			return;
		}
		ws = sock;
		sock.onopen = () => {
			everOpened = true;
			attempt = 0;
		};
		sock.onmessage = (ev) => {
			try {
				const env = JSON.parse(String(ev.data)) as { event?: string; data?: unknown };
				if (env.event) {
					subs.get(env.event)?.forEach((cb) => cb(env.event!, env.data));
					subs.get("*")?.forEach((cb) => cb(env.event!, env.data));
				}
			} catch {
				// Malformed frame: skip.
			}
		};
		sock.onclose = () => {
			if (stopped || suspended) return;
			// First dial failing without ever opening means the handshake was
			// refused (older SDK): degrade to SSE for this page lifetime.
			if (!everOpened && attempt === 0) {
				fellBack = true;
				es = new EventSource(url, { withCredentials: true });
				for (const kind of subs.keys()) attachSseKind(es, kind);
				return;
			}
			attempt++;
			setTimeout(dial, eventBackoff(attempt));
		};
	};
	dial();

	return {
		suspend() {
			if (stopped || suspended) return;
			suspended = true;
			if (ws) {
				// Clear handlers before close: an in-flight frame can still land
				// between close() and the socket actually dying.
				ws.onmessage = null;
				ws.onclose = null;
				ws.close();
			}
			es?.close();
		},
		resume() {
			if (stopped || !suspended) return;
			suspended = false;
			if (fellBack) {
				// The SSE fallback never reconnects natively: rebuild it with the
				// currently-subscribed kinds.
				es = new EventSource(url, { withCredentials: true });
				for (const kind of subs.keys()) attachSseKind(es, kind);
				return;
			}
			everOpened = false;
			attempt = 0;
			if (subs.size > 0) dial();
		},
		subscribe(eventKind: string, cb: (eventKind: string, payload: unknown) => void) {
			let set = subs.get(eventKind);
			if (!set) {
				set = new Set();
				subs.set(eventKind, set);
				if (es) attachSseKind(es, eventKind);
			}
			set.add(cb);
			return () => {
				set!.delete(cb);
				if (set!.size === 0) {
					subs.delete(eventKind);
					if (subs.size === 0) teardown();
				}
			};
		},
	};
}

function ensureEvents() {
	void appBase().then((base) => {
		// Await the appBase gate: subscribing on module load races the
		// bootstrap handshake, and a root-relative /events URL 404s against
		// the gateway. An empty base (standalone dev open) is valid: both
		// transports then build URLs from location.host. The "*" subscription
		// receives every envelope; per-kind filtering happens in
		// dispatchEvent.
		eventChannel(base + "/events").subscribe("*", dispatchEvent);
	});
}

function subscribeEvent(eventKind: string, cb: EventHandler): () => void {
	let set = eventHandlers.get(eventKind);
	if (!set) {
		set = new Set();
		eventHandlers.set(eventKind, set);
		ensureEvents();
	}
	set.add(cb);
	return () => {
		set!.delete(cb);
		if (set!.size === 0) {
			// Keep the shared channel alive across idle periods: closing
			// it would throw away reconnect state and race in-flight dials.
			// The channel refcounts and closes itself when its last
			// subscriber (including the bridge client) leaves.
			eventHandlers.delete(eventKind);
		}
	};
}
export interface ProvisionRequest {
  Name: string;
  Secret?: string;
  Digits?: number;
  Period?: number;
  Algorithm?: string;
}

export interface ProvisionResponse {
  AccountId: string;
  Name: string;
  SecretBase32: string;
  OtpAuthUrl: string;
  Digits: number;
  Period: number;
  Algorithm: string;
}

export interface AccountCode {
  AccountId: string;
  Name: string;
  Code: string;
  SecretBase32: string;
  SecondsRemaining: number;
  Period: number;
}

export interface CodesRequest {
  AccountId?: string;
}

export interface CodesResponse {
  Accounts: AccountCode[];
  NowUnix: number;
}

export interface VerifyRequest {
  AccountId: string;
  Code: string;
  Window?: number;
}

export interface VerifyResponse {
  Valid: boolean;
  AccountId: string;
  Error: string;
}

export interface RemoveRequest {
  AccountId: string;
}

export interface RemoveResponse {
  Removed: boolean;
  AccountId: string;
}

export interface UpdateRequest {
  AccountId: string;
  Name?: string;
  Secret?: string;
}

export interface UpdateResponse {
  Updated: boolean;
  AccountId: string;
  Name: string;
  SecretBase32: string;
  OtpAuthUrl: string;
}

export interface DiscoverRequest {
  Service?: string;
  Callable?: string;
  Limit?: number;
  Cursor?: string;
}

export interface DiscoverMatch {
  CallID: string;
  Service: string;
  Description: string;
  Permission: string;
  EffectKind: string;
  ReqSchemaId: number;
  RespSchemaId: number;
}

export interface DiscoverResponse {
  Items: DiscoverMatch[];
  NextCursor: string;
}

export async function provision(req: ProvisionRequest): Promise<ProvisionResponse> {
	return invoke<ProvisionResponse>("provision", req as Record<string, unknown>);
}

export async function codes(req: CodesRequest): Promise<CodesResponse> {
	return invoke<CodesResponse>("codes", req as Record<string, unknown>);
}

export async function verify(req: VerifyRequest): Promise<VerifyResponse> {
	return invoke<VerifyResponse>("verify", req as Record<string, unknown>);
}

export async function remove(req: RemoveRequest): Promise<RemoveResponse> {
	return invoke<RemoveResponse>("remove", req as Record<string, unknown>);
}

export async function update(req: UpdateRequest): Promise<UpdateResponse> {
	return invoke<UpdateResponse>("update", req as Record<string, unknown>);
}

export async function discover(req: DiscoverRequest): Promise<DiscoverResponse> {
	return invoke<DiscoverResponse>("discover", req as Record<string, unknown>);
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
