// Smoke test for generated gospore client bindings.
// Usage: npx tsx src/test-smoke.ts [baseURL]

import { EventSource } from "eventsource";
(globalThis as Record<string, unknown>).EventSource = EventSource;

import { GosporeClient, HTTPTransport } from "@qomos/gospore-client";
import * as filesystem from "./gen-clients/filesystem/client";
import * as workspace from "./gen-clients/workspace/client";
import * as agentChat from "./gen-clients/local/client";

const baseURL = process.argv[2] ?? "http://localhost:18080";
const transport = new HTTPTransport({ baseURL, timeoutMs: 10000 });
const client = new GosporeClient(transport);

let passed = 0;
let failed = 0;
let smokeProjectId: string | null = null;

function assert(cond: boolean, msg: string) {
  if (cond) {
    passed++;
    console.log(`  PASS: ${msg}`);
  } else {
    failed++;
    console.error(`  FAIL: ${msg}`);
  }
}

// "Not ready" classifier — backend either is not listening yet, or its
// gateway returned 404 because the callable hasn't been registered.
// Anything else (5xx, business error, etc.) means the backend is up
// and the callable is reachable; surface those as ready.
function isNotReady(err: unknown): boolean {
  const msg = String((err as { message?: unknown })?.message ?? err);
  if (
    msg.includes("ECONNREFUSED") ||
    msg.includes("fetch failed") ||
    msg.includes("ENOTFOUND") ||
    msg.includes("ETIMEDOUT") ||
    msg.includes("network") ||
    msg.includes("NetworkError")
  ) {
    return true;
  }
  // gospore gateway responds 404 + "service not found" when the target
  // callable isn't in the registry yet.
  if (msg.includes("404") && msg.includes("service not found")) {
    return true;
  }
  return false;
}

async function getFirstProjectId(): Promise<string | null> {
  try {
    const resp = await workspace.listProject(client);
    return resp.Items?.[0]?.ActorId ?? null;
  } catch {
    return null;
  }
}

async function pollReady(maxWaitMs = 20000): Promise<boolean> {
  const deadline = Date.now() + maxWaitMs;
  while (Date.now() < deadline) {
    try {
      const projectId = await getFirstProjectId();
      if (!projectId) {
        await new Promise((r) => setTimeout(r, 300));
        continue;
      }
      await filesystem.listJson(client, { Path: "." });
      return true;
    } catch (err) {
      if (isNotReady(err)) {
        await new Promise((r) => setTimeout(r, 300));
        continue;
      }
      // Gateway responded with a real error — backend is up, callable
      // is reachable; treat as ready (the test will surface the failure).
      return true;
    }
  }
  return false;
}

async function runEventSmoke() {
  console.log("\nTest 2: agent turn event stream");

  const agentsResp = await workspace.agents(client);
  assert(Array.isArray(agentsResp.Items), "workspace.agents returns an item list");
  const activeAgent = agentsResp.Items.find((agent) => agent.ActorId);
  if (!activeAgent) {
    throw new Error("no active agent with actorId found for event smoke");
  }

  const events: { kind?: string }[] = [];
  const done = new Promise<void>((resolve, reject) => {
    const timer = setTimeout(() => {
      off();
      reject(new Error("timeout waiting for agent turn completion"));
    }, 10000);

    const off = client.events.onInstance(activeAgent.ActorId, "turn", (payload) => {
      const ev = payload as { kind?: string };
      events.push(ev);
      console.log(`  event: ${JSON.stringify(ev)}`);
      if (ev.kind === "turn.completed" || ev.kind === "turn.failed" || ev.kind === "turn.cancelled") {
        clearTimeout(timer);
        resolve();
      }
    });
  });

  await agentChat.chatSubmit(
    client,
    { Text: "Reply with exactly OK", Unit: { model: "claude-sonnet-4-6", provider: "" } },
    { target: activeAgent.ActorId },
  );
  await done;

  assert(events.length >= 1, "received at least one turn event");
  assert(events.some((ev) => ev.kind === "turn.started"), "received turn.started");
  assert(
    events.some((ev) => ev.kind === "turn.completed" || ev.kind === "turn.failed" || ev.kind === "turn.cancelled"),
    "received terminal turn event",
  );
}

async function runTests() {
  console.log(`smoke: connecting to ${baseURL}`);

  console.log("\nWaiting for backend to be ready ...");
  const ready = await pollReady();
  assert(ready, "backend is ready (callables registered)");
  if (!ready) {
    client.close();
    process.exit(1);
  }

  smokeProjectId = await getFirstProjectId();
  if (!smokeProjectId) {
    console.error("smoke: no projects mounted; cannot run project-scoped tests");
    client.close();
    process.exit(1);
  }

  // Test 1: unary invoke — filesystem.list_json
  console.log("\nTest 1: filesystem.list_json('.')");
  if (!smokeProjectId) {
    failed++;
    console.error("  FAIL: no project available for filesystem.list_json");
  } else {
    try {
      const entriesResp = await filesystem.listJson(client, { Path: "." });
      assert(Array.isArray(entriesResp.Items), "returns an item list");
      assert(entriesResp.Items.length > 0, "returns at least one entry");
      const first = entriesResp.Items[0];
      if (first) {
        assert(typeof first.Name === "string", "entry has Name string");
        assert(typeof first.IsDir === "boolean", "entry has IsDir boolean");
        assert(typeof first.Size === "number", "entry has Size number");
        assert(typeof first.ModTime === "string", "entry has ModTime string");
        console.log(`  entries[0] = ${JSON.stringify(first)}`);
      }
    } catch (err) {
      failed++;
      console.error(`  FAIL: filesystem.list_json threw: ${err}`);
    }
  }

  try {
    await runEventSmoke();
  } catch (err) {
    failed++;
    console.error(`  FAIL: agent turn event stream threw: ${err}`);
  }

  // Summary
  console.log(`\n--- summary: ${passed} passed, ${failed} failed ---`);
  client.close();
  process.exit(failed > 0 ? 1 : 0);
}

runTests().catch((err) => {
  console.error("smoke: unexpected error:", err);
  process.exit(1);
});
