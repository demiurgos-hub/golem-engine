import type { WebTransportCertificateHash } from "./client.js";

function copyBytes(value: ArrayBuffer | ArrayBufferView): ArrayBuffer {
  const bytes = ArrayBuffer.isView(value)
    ? new Uint8Array(value.buffer, value.byteOffset, value.byteLength)
    : new Uint8Array(value);
  const copy = new Uint8Array(bytes.byteLength);
  copy.set(bytes);
  return copy.buffer;
}

function hexToBytes(hex: string): Uint8Array {
  const normalized = hex.replace(/\s+/g, "");
  if (normalized.length % 2 !== 0) {
    throw new Error("golem-js: certificate hash hex string must have even length");
  }
  if (!/^[0-9a-fA-F]*$/.test(normalized)) {
    throw new Error("golem-js: certificate hash hex string contains non-hex characters");
  }
  const out = new Uint8Array(normalized.length / 2);
  for (let i = 0; i < normalized.length; i += 2) {
    out[i / 2] = Number.parseInt(normalized.slice(i, i + 2), 16);
  }
  return out;
}

export function normalizeCertificateHash(hash: WebTransportCertificateHash): { algorithm: string; value: ArrayBuffer } {
  const algorithm = hash.algorithm.trim().toLowerCase();
  if (algorithm !== "sha-256") {
    throw new Error(`golem-js: unsupported certificate hash algorithm ${JSON.stringify(hash.algorithm)}`);
  }
  const value = copyBytes(typeof hash.value === "string" ? hexToBytes(hash.value) : hash.value);
  if (value.byteLength !== 32) {
    throw new Error(`golem-js: sha-256 certificate hash length ${value.byteLength}, want 32`);
  }
  return { algorithm, value };
}

export function cloneCertificateHash(hash: WebTransportCertificateHash): WebTransportCertificateHash {
  return { algorithm: hash.algorithm, value: typeof hash.value === "string" ? hash.value : copyBytes(hash.value) };
}
