"use strict";

const assert = require("node:assert/strict");
const { execFileSync } = require("node:child_process");
const crypto = require("node:crypto");
const fs = require("node:fs");
const http = require("node:http");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");
const zlib = require("node:zlib");

const { parseChecksums, resolveReleaseAsset } = require("../scripts/platform");
const { installBinary } = require("../scripts/install");

function buildTarGz(fileName, contentBuf) {
  const header = Buffer.alloc(512, 0);
  header.write(fileName, 0, Math.min(fileName.length, 100), "utf8");
  header.write("0000755\0", 100, 8, "utf8");
  header.write("0000000\0", 108, 8, "utf8");
  header.write("0000000\0", 116, 8, "utf8");
  const sizeOctal = contentBuf.length.toString(8).padStart(11, "0") + "\0";
  header.write(sizeOctal, 124, 12, "utf8");
  header.write("00000000000\0", 136, 12, "utf8");
  header.fill(0x20, 148, 156); // checksum field spaces before computing
  header.write("0", 156, 1, "utf8");
  header.write("ustar\0", 257, 6, "utf8");
  header.write("00", 263, 2, "utf8");

  let sum = 0;
  for (let i = 0; i < 512; i++) sum += header[i];
  const sumStr = sum.toString(8).padStart(6, "0") + "\0 ";
  header.write(sumStr, 148, 8, "utf8");

  const padLen = (512 - (contentBuf.length % 512)) % 512;
  const tar = Buffer.concat([header, contentBuf, Buffer.alloc(padLen, 0), Buffer.alloc(1024, 0)]);
  return zlib.gzipSync(tar);
}

test("resolveReleaseAsset maps darwin/linux/win32 and rejects unsupported arch", () => {
  const mac = resolveReleaseAsset("v0.1.0", "darwin", "arm64");
  assert.equal(mac.archiveName, "tagforge_0.1.0_darwin_arm64.tar.gz");
  assert.equal(mac.binaryName, "tagforge");
  assert.equal(
    mac.archiveURL,
    "https://github.com/LorranHippolyte/tagforge/releases/download/v0.1.0/tagforge_0.1.0_darwin_arm64.tar.gz"
  );

  const linux = resolveReleaseAsset("0.2.3", "linux", "x64");
  assert.equal(linux.archiveName, "tagforge_0.2.3_linux_amd64.tar.gz");

  const win = resolveReleaseAsset("0.2.3", "win32", "x64");
  assert.equal(win.archiveName, "tagforge_0.2.3_windows_amd64.zip");
  assert.equal(win.binaryName, "tagforge.exe");

  assert.throws(() => resolveReleaseAsset("0.1.0", "linux", "s390x"), /Unsupported CPU architecture/);
});

test("parseChecksums parses standard GoReleaser sha256 output", () => {
  const fakeDigest = "a".repeat(64);
  const parsed = parseChecksums(`${fakeDigest}  tagforge_0.1.0_linux_amd64.tar.gz\n`);
  assert.equal(parsed.get("tagforge_0.1.0_linux_amd64.tar.gz"), fakeDigest);
});

test("installBinary verifies SHA-256 checksum and rejects tampered archive", async () => {
  const payload = Buffer.from("#!/bin/sh\necho tagforge-test\n", "utf8");
  const tgz = buildTarGz("tagforge", payload);
  const digest = crypto.createHash("sha256").update(tgz).digest("hex");
  let serveTampered = false;

  const srv = http.createServer((req, res) => {
    if (req.url.endsWith("/checksums.txt")) {
      res.writeHead(200, { "Content-Type": "text/plain" });
      res.end(`${digest}  tagforge_0.1.0_linux_amd64.tar.gz\n`);
      return;
    }
    if (req.url.endsWith("/tagforge_0.1.0_linux_amd64.tar.gz")) {
      res.writeHead(200, { "Content-Type": "application/gzip" });
      if (serveTampered) {
        const corrupted = Buffer.from(tgz);
        corrupted[corrupted.length - 1] ^= 0xff;
        res.end(corrupted);
      } else {
        res.end(tgz);
      }
      return;
    }
    res.writeHead(404);
    res.end();
  });

  await new Promise((resolve) => srv.listen(0, "127.0.0.1", resolve));
  const { port } = srv.address();
  const baseURL = `http://127.0.0.1:${port}/releases/download`;
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "tagforge-npm-test-"));

  try {
    const installed = await installBinary({
      version: "0.1.0",
      platform: "linux",
      arch: "x64",
      baseURL,
      vendorDir: tmpDir,
    });
    assert.equal(fs.readFileSync(installed.binaryPath, "utf8"), payload.toString("utf8"));

    serveTampered = true;
    const tamperedDir = path.join(tmpDir, "tampered");
    await assert.rejects(
      () =>
        installBinary({
          version: "0.1.0",
          platform: "linux",
          arch: "x64",
          baseURL,
          vendorDir: tamperedDir,
        }),
      /SHA-256 verification failed/
    );
    assert.equal(fs.existsSync(path.join(tamperedDir, "tagforge")), false);
  } finally {
    srv.close();
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
});

test("bin/tagforge.js executes built Go binary cleanly when present", (t) => {
  const defaultLocalBin = path.resolve(__dirname, "..", "..", "..", "bin", "tagforge");
  const binPath = process.env.TAGFORGE_BINARY_PATH || (fs.existsSync(defaultLocalBin) ? defaultLocalBin : "");
  if (!binPath) {
    t.skip("no prebuilt tagforge binary available via TAGFORGE_BINARY_PATH");
    return;
  }

  const out = execFileSync(
    process.execPath,
    [path.resolve(__dirname, "..", "bin", "tagforge.js"), "version", "--format", "json"],
    {
      env: { ...process.env, TAGFORGE_BINARY_PATH: binPath },
      encoding: "utf8",
    }
  );
  const parsed = JSON.parse(out);
  assert.ok(parsed.version);
});
