#!/usr/bin/env node
"use strict";

const { spawn } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");

const binaryName = process.platform === "win32" ? "tagforge.exe" : "tagforge";
const defaultBinPath = path.join(__dirname, "..", "vendor", binaryName);
const binPath = process.env.TAGFORGE_BINARY_PATH || defaultBinPath;

if (!fs.existsSync(binPath)) {
  console.error(
    `tagforge binary not found at ${binPath}. Reinstall the package or set TAGFORGE_BINARY_PATH.`
  );
  process.exit(1);
}

const child = spawn(binPath, process.argv.slice(2), {
  stdio: "inherit",
});

for (const sig of ["SIGINT", "SIGTERM"]) {
  process.on(sig, () => {
    if (!child.killed) {
      child.kill(sig);
    }
  });
}

child.on("error", (err) => {
  console.error(`Failed to execute tagforge binary (${binPath}): ${err.message}`);
  process.exit(1);
});

child.on("exit", (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code === null ? 1 : code);
});
