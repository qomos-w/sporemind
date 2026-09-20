import { rmSync } from "node:fs";

for (const p of process.argv.slice(2)) {
  rmSync(p, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
}
