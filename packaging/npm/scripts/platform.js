"use strict";

const DEFAULT_RELEASE_BASE_URL = "https://github.com/LorranHippolyte/tagforge/releases/download";

const PLATFORM_MAP = {
  darwin: "darwin",
  linux: "linux",
  win32: "windows",
};

const ARCH_MAP = {
  x64: "amd64",
  arm64: "arm64",
};

/**
 * Resolve GoReleaser v2 archive and binary metadata for the target OS/CPU.
 * Matches .goreleaser.yaml archive name_template:
 *   "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
 */
function resolveReleaseAsset(
  version,
  platform = process.platform,
  arch = process.arch,
  baseURL = DEFAULT_RELEASE_BASE_URL
) {
  if (!version || typeof version !== "string") {
    throw new Error("version string is required");
  }
  const cleanVersion = version.trim().replace(/^v/, "");
  if (!cleanVersion) {
    throw new Error("version string cannot be empty");
  }

  const goos = PLATFORM_MAP[platform];
  if (!goos) {
    throw new Error(`Unsupported OS platform "${platform}" for tagforge prebuilt binaries`);
  }
  const goarch = ARCH_MAP[arch];
  if (!goarch) {
    throw new Error(`Unsupported CPU architecture "${arch}" for tagforge prebuilt binaries`);
  }

  const ext = goos === "windows" ? "zip" : "tar.gz";
  const binaryName = goos === "windows" ? "tagforge.exe" : "tagforge";
  const tag = `v${cleanVersion}`;
  const archiveName = `tagforge_${cleanVersion}_${goos}_${goarch}.${ext}`;
  const cleanBase = String(baseURL || DEFAULT_RELEASE_BASE_URL).replace(/\/+$/, "");

  return {
    version: cleanVersion,
    tag,
    goos,
    goarch,
    ext,
    binaryName,
    archiveName,
    archiveURL: `${cleanBase}/${tag}/${archiveName}`,
    checksumsURL: `${cleanBase}/${tag}/checksums.txt`,
  };
}

/**
 * Parse a GoReleaser SHA-256 checksums.txt file into Map<filename, hexDigest>.
 */
function parseChecksums(text) {
  const map = new Map();
  if (!text) return map;
  for (const rawLine of String(text).split(/\r?\n/)) {
    const line = rawLine.trim();
    if (!line || line.startsWith("#")) continue;
    const match = line.match(/^([a-fA-F0-9]{64})\s+\*?(.+)$/);
    if (!match) continue;
    map.set(match[2].trim(), match[1].toLowerCase());
  }
  return map;
}

module.exports = {
  DEFAULT_RELEASE_BASE_URL,
  resolveReleaseAsset,
  parseChecksums,
};
