"use strict";

const crypto = require("node:crypto");
const fs = require("node:fs");
const http = require("node:http");
const https = require("node:https");
const path = require("node:path");
const zlib = require("node:zlib");
const { DEFAULT_RELEASE_BASE_URL, parseChecksums, resolveReleaseAsset } = require("./platform");

function fetchBuffer(url, maxRedirects = 10) {
  return new Promise((resolve, reject) => {
    if (maxRedirects < 0) {
      reject(new Error(`Too many redirects fetching ${url}`));
      return;
    }
    const client = url.startsWith("https://") ? https : url.startsWith("http://") ? http : null;
    if (!client) {
      reject(new Error(`Unsupported URL scheme for ${url}`));
      return;
    }

    const req = client.get(
      url,
      { headers: { "User-Agent": "tagforge-npm-wrapper" } },
      (res) => {
        const status = res.statusCode || 0;
        if (status >= 300 && status < 400 && res.headers.location) {
          res.resume();
          const nextURL = new URL(res.headers.location, url).toString();
          resolve(fetchBuffer(nextURL, maxRedirects - 1));
          return;
        }
        if (status < 200 || status >= 300) {
          res.resume();
          reject(new Error(`HTTP ${status} fetching ${url}`));
          return;
        }
        const chunks = [];
        res.on("data", (c) => chunks.push(c));
        res.on("end", () => resolve(Buffer.concat(chunks)));
        res.on("error", reject);
      }
    );
    req.on("error", reject);
  });
}

function verifySHA256(buf, expectedHex, label) {
  const actual = crypto.createHash("sha256").update(buf).digest("hex").toLowerCase();
  const expected = String(expectedHex || "").toLowerCase();
  if (!expected || actual !== expected) {
    throw new Error(
      `SHA-256 verification failed for ${label}: expected ${expected || "<missing>"}, got ${actual}`
    );
  }
  return actual;
}

function readTarString(buf, start, len) {
  const slice = buf.subarray(start, start + len);
  const zero = slice.indexOf(0);
  return slice.subarray(0, zero === -1 ? len : zero).toString("utf8");
}

function extractTarGzBinary(archiveBuf, binaryName) {
  const tar = zlib.gunzipSync(archiveBuf);
  let offset = 0;
  let paxPath = "";

  while (offset + 512 <= tar.length) {
    const header = tar.subarray(offset, offset + 512);
    if (header.every((b) => b === 0)) break;

    const rawName = readTarString(header, 0, 100);
    const prefix = readTarString(header, 345, 155);
    const sizeRaw = readTarString(header, 124, 12).trim();
    const typeFlag = String.fromCharCode(header[156] || 48);
    const size = sizeRaw ? parseInt(sizeRaw, 8) : 0;
    const fullName = paxPath || (prefix ? `${prefix}/${rawName}` : rawName);
    paxPath = "";

    const dataStart = offset + 512;
    const dataEnd = dataStart + size;
    if (dataEnd > tar.length) {
      throw new Error("Truncated tar archive");
    }

    if (typeFlag === "x") {
      const paxText = tar.subarray(dataStart, dataEnd).toString("utf8");
      for (const line of paxText.split("\n")) {
        const idx = line.indexOf(" path=");
        if (idx !== -1) paxPath = line.slice(idx + 6);
      }
    } else if ((typeFlag === "0" || typeFlag === "\0") && path.posix.basename(fullName) === binaryName) {
      return Buffer.from(tar.subarray(dataStart, dataEnd));
    }

    const padded = Math.ceil(size / 512) * 512;
    offset = dataStart + padded;
  }

  throw new Error(`Binary ${binaryName} not found inside tar.gz archive`);
}

function extractZipBinary(zipBuf, binaryName) {
  // Locate End of Central Directory (0x06054b50) scanning backwards from the tail.
  let eocdOffset = -1;
  const minStart = Math.max(0, zipBuf.length - 65557);
  for (let i = zipBuf.length - 22; i >= minStart; i--) {
    if (zipBuf.readUInt32LE(i) === 0x06054b50) {
      eocdOffset = i;
      break;
    }
  }
  if (eocdOffset === -1) {
    throw new Error("Invalid zip archive: EOCD record not found");
  }

  const totalEntries = zipBuf.readUInt16LE(eocdOffset + 10);
  let cdOffset = zipBuf.readUInt32LE(eocdOffset + 16);

  for (let idx = 0; idx < totalEntries; idx++) {
    if (cdOffset + 46 > zipBuf.length || zipBuf.readUInt32LE(cdOffset) !== 0x02014b50) {
      throw new Error("Invalid zip central directory entry");
    }
    const method = zipBuf.readUInt16LE(cdOffset + 10);
    const compSize = zipBuf.readUInt32LE(cdOffset + 20);
    const nameLen = zipBuf.readUInt16LE(cdOffset + 28);
    const extraLen = zipBuf.readUInt16LE(cdOffset + 30);
    const commentLen = zipBuf.readUInt16LE(cdOffset + 32);
    const localOffset = zipBuf.readUInt32LE(cdOffset + 42);
    const name = zipBuf.subarray(cdOffset + 46, cdOffset + 46 + nameLen).toString("utf8");

    if (path.posix.basename(name) === binaryName) {
      if (zipBuf.readUInt32LE(localOffset) !== 0x04034b50) {
        throw new Error("Invalid zip local file header");
      }
      const lNameLen = zipBuf.readUInt16LE(localOffset + 26);
      const lExtraLen = zipBuf.readUInt16LE(localOffset + 28);
      const dataStart = localOffset + 30 + lNameLen + lExtraLen;
      const compressed = zipBuf.subarray(dataStart, dataStart + compSize);
      if (method === 0) return Buffer.from(compressed);
      if (method === 8) return zlib.inflateRawSync(compressed);
      throw new Error(`Unsupported zip compression method ${method}`);
    }

    cdOffset += 46 + nameLen + extraLen + commentLen;
  }

  throw new Error(`Binary ${binaryName} not found inside zip archive`);
}

async function downloadAndVerify(asset) {
  const [checksumBuf, archiveBuf] = await Promise.all([
    fetchBuffer(asset.checksumsURL),
    fetchBuffer(asset.archiveURL),
  ]);
  const checksums = parseChecksums(checksumBuf.toString("utf8"));
  const expected = checksums.get(asset.archiveName);
  const sha256 = verifySHA256(archiveBuf, expected, asset.archiveName);

  const binaryBuf =
    asset.ext === "zip"
      ? extractZipBinary(archiveBuf, asset.binaryName)
      : extractTarGzBinary(archiveBuf, asset.binaryName);

  return { binaryBuf, sha256 };
}

async function installBinary(options = {}) {
  const pkg = require("../package.json");
  const version = options.version || pkg.version;
  const platform = options.platform || process.platform;
  const arch = options.arch || process.arch;
  const baseURL =
    options.baseURL || process.env.TAGFORGE_RELEASE_BASE_URL || DEFAULT_RELEASE_BASE_URL;
  const vendorDir = options.vendorDir || path.join(__dirname, "..", "vendor");

  const asset = resolveReleaseAsset(version, platform, arch, baseURL);
  const { binaryBuf, sha256 } = await downloadAndVerify(asset);

  fs.mkdirSync(vendorDir, { recursive: true });
  const destPath = path.join(vendorDir, asset.binaryName);
  fs.writeFileSync(destPath, binaryBuf, { mode: 0o755 });
  if (platform !== "win32") {
    fs.chmodSync(destPath, 0o755);
  }

  return { binaryPath: destPath, archiveName: asset.archiveName, sha256 };
}

if (require.main === module) {
  if (process.env.TAGFORGE_SKIP_DOWNLOAD === "1") {
    process.exit(0);
  }
  installBinary()
    .then((res) => {
      console.error(`tagforge binary installed: ${res.binaryPath} (${res.archiveName})`);
    })
    .catch((err) => {
      console.error(`Failed to install tagforge binary: ${err.message}`);
      console.error("Set TAGFORGE_BINARY_PATH to a locally built tagforge binary or build from source with Go 1.26+.");
      process.exit(1);
    });
}

module.exports = {
  downloadAndVerify,
  installBinary,
  extractTarGzBinary,
  extractZipBinary,
  verifySHA256,
};
