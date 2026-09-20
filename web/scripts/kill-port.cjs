/**
 * kill-port — terminate the Vite process listening on a development port.
 *
 * Usage: node scripts/kill-port.cjs [PORT]
 * Default port is read from VITE_PORT env var or falls back to 5558.
 *
 * This intentionally does not kill an arbitrary process by port. Compiled
 * desktop apps and test backends use their own ports and must keep running.
 */
const { execFileSync, execSync } = require("child_process");

const port = process.argv[2] || process.env.VITE_PORT || "5558";
const platform = process.platform;

function isViteCommand(command) {
  return /(?:^|[\\/\s])vite(?:\.cmd)?(?:[.\\/]\S*|\s|$)/i.test(command || "") ||
    /node_modules[\\/](?:\.bin[\\/].*?[\\/]\.\.[\\/])?vite[\\/]bin[\\/]vite\.js\b/i.test(command || "");
}

function commandLineForPid(pid) {
  if (platform === "win32") {
    return execFileSync(
      "powershell.exe",
      ["-NoProfile", "-Command", `(Get-CimInstance Win32_Process -Filter 'ProcessId = ${pid}').CommandLine`],
      { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }
    ).trim();
  }
  return execFileSync("ps", ["-o", "command=", "-p", pid], {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "ignore"],
  }).trim();
}

function listeningPids() {
  if (platform === "win32") {
    const out = execSync("netstat -ano -p tcp", { encoding: "utf8" });
    const pids = new Set();
    const portSuffix = `:${port}`;
    for (const line of out.split(/\r?\n/)) {
      const fields = line.trim().split(/\s+/);
      if (fields[0] === "TCP" && fields[1]?.endsWith(portSuffix) && fields[3] === "LISTENING" && /^\d+$/.test(fields[4])) {
        pids.add(fields[4]);
      }
    }
    return pids;
  }
  const out = execFileSync("lsof", ["-nP", "-t", "-iTCP:" + port, "-sTCP:LISTEN"], {
    encoding: "utf8",
  });
  return new Set(out.trim().split(/\s+/).filter(Boolean));
}

try {
  for (const pid of listeningPids()) {
    let command;
    try {
      command = commandLineForPid(pid);
    } catch {
      continue;
    }
    if (!isViteCommand(command)) {
      console.log(`[kill-port] Leaving PID ${pid} on port ${port} (${command || "unknown process"})`);
      continue;
    }
    try {
      if (platform === "win32") {
        execFileSync("taskkill", ["/F", "/PID", pid], { stdio: "ignore" });
      } else {
        process.kill(Number(pid), "SIGKILL");
      }
      console.log(`[kill-port] Killed Vite PID ${pid} on port ${port}`);
    } catch {
      // The process may have exited between discovery and termination.
    }
  }
} catch {
  // No process is listening on that port.
}
