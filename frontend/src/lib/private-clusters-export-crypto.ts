// Passphrase encryption for private-cluster export files.
//
// The plaintext is the `kafkito.private-clusters/v1` bundle JSON that
// exportBundle() writes and importBundle() reads. The file is a versioned
// JSON envelope around it:
//
//   {
//     "format": "kafkito.private-clusters.export",
//     "version": 2,
//     "kdf": { "name": "PBKDF2", "hash": "SHA-256", "iterations": 600000,
//              "salt": "<base64, 16 random bytes>" },
//     "cipher": { "name": "AES-GCM", "iv": "<base64, 12 random bytes>" },
//     "data": "<base64 of the ciphertext followed by the 16-byte GCM tag>"
//   }
//
// PBKDF2 derives a non-extractable AES-256-GCM key from the NFC-normalised,
// UTF-8 encoded passphrase. The additional authenticated data is the UTF-8
// JSON of all fields except "data", in the order above and without
// whitespace, so a changed header fails decryption like a changed
// ciphertext. Every decryption failure gives the same message, whether the
// passphrase is wrong or the file was changed.

const FORMAT = "kafkito.private-clusters.export";
const VERSION = 2;
const ITERATIONS = 600_000;
// Caps the key-derivation work that a crafted file can demand.
const MAX_ITERATIONS = 10_000_000;
const SALT_BYTES = 16;
const IV_BYTES = 12;
const TAG_BYTES = 16;

const DECRYPT_FAILED = "Wrong passphrase or damaged file.";
const NO_WEBCRYPTO = "Encrypted exports need a secure connection (HTTPS or localhost).";

export const MIN_PASSPHRASE_LENGTH = 12;

interface Header {
  format: typeof FORMAT;
  version: typeof VERSION;
  kdf: { name: "PBKDF2"; hash: "SHA-256"; iterations: number; salt: string };
  cipher: { name: "AES-GCM"; iv: string };
}

const BASE64 = /^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/;

function toBase64(bytes: Uint8Array): string {
  let binary = "";
  for (let i = 0; i < bytes.length; i += 0x8000) {
    binary += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  }
  return btoa(binary);
}

function fromBase64(text: string): Uint8Array<ArrayBuffer> | null {
  if (!BASE64.test(text)) return null;
  const binary = atob(text);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

function utf8(text: string): Uint8Array<ArrayBuffer> {
  return new TextEncoder().encode(text);
}

function parseJSON(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

// crypto.subtle only exists in secure contexts (HTTPS, localhost).
function subtleCrypto(): SubtleCrypto {
  const subtle = globalThis.crypto?.subtle;
  if (!subtle) throw new Error(NO_WEBCRYPTO);
  return subtle;
}

function header(iterations: number, salt: string, iv: string): Header {
  return {
    format: FORMAT,
    version: VERSION,
    kdf: { name: "PBKDF2", hash: "SHA-256", iterations, salt },
    cipher: { name: "AES-GCM", iv },
  };
}

async function deriveKey(
  subtle: SubtleCrypto,
  passphrase: string,
  salt: Uint8Array<ArrayBuffer>,
  iterations: number,
  usage: "encrypt" | "decrypt",
): Promise<CryptoKey> {
  const material = await subtle.importKey(
    "raw",
    utf8(passphrase.normalize("NFC")),
    "PBKDF2",
    false,
    ["deriveKey"],
  );
  return subtle.deriveKey(
    { name: "PBKDF2", hash: "SHA-256", salt, iterations },
    material,
    { name: "AES-GCM", length: 256 },
    false,
    [usage],
  );
}

export function isPassphraseLongEnough(passphrase: string): boolean {
  return Array.from(passphrase.normalize("NFC")).length >= MIN_PASSPHRASE_LENGTH;
}

/** True when the file text is an encrypted export (any version), not a plaintext bundle. */
export function isEncryptedExport(text: string): boolean {
  const value = parseJSON(text);
  return isRecord(value) && value.format === FORMAT;
}

/**
 * Encrypts an export bundle's JSON with the passphrase and returns the file
 * text. `options.iterations` only exists to keep tests fast; the app always
 * uses the default of 600,000.
 */
export async function encryptExport(
  plaintext: string,
  passphrase: string,
  options: { iterations?: number } = {},
): Promise<string> {
  const subtle = subtleCrypto();
  if (!isPassphraseLongEnough(passphrase)) {
    throw new Error(`The passphrase needs at least ${MIN_PASSPHRASE_LENGTH} characters.`);
  }
  const iterations = options.iterations ?? ITERATIONS;
  const salt = crypto.getRandomValues(new Uint8Array(SALT_BYTES));
  const iv = crypto.getRandomValues(new Uint8Array(IV_BYTES));
  const head = header(iterations, toBase64(salt), toBase64(iv));
  const key = await deriveKey(subtle, passphrase, salt, iterations, "encrypt");
  const data = await subtle.encrypt(
    { name: "AES-GCM", iv, additionalData: utf8(JSON.stringify(head)) },
    key,
    utf8(plaintext),
  );
  return JSON.stringify({ ...head, data: toBase64(new Uint8Array(data)) }, null, 2);
}

function readEnvelope(value: unknown) {
  if (!isRecord(value) || !isRecord(value.kdf) || !isRecord(value.cipher)) return null;
  const { kdf, cipher, data } = value;
  const { iterations, salt, hash } = kdf;
  const { iv } = cipher;
  if (
    value.format !== FORMAT ||
    value.version !== VERSION ||
    kdf.name !== "PBKDF2" ||
    hash !== "SHA-256" ||
    cipher.name !== "AES-GCM" ||
    typeof iterations !== "number" ||
    !Number.isSafeInteger(iterations) ||
    iterations < 1 ||
    iterations > MAX_ITERATIONS ||
    typeof salt !== "string" ||
    typeof iv !== "string" ||
    typeof data !== "string"
  ) {
    return null;
  }
  const saltBytes = fromBase64(salt);
  const ivBytes = fromBase64(iv);
  const dataBytes = fromBase64(data);
  if (
    saltBytes?.length !== SALT_BYTES ||
    ivBytes?.length !== IV_BYTES ||
    !dataBytes ||
    dataBytes.length < TAG_BYTES
  ) {
    return null;
  }
  return {
    head: header(iterations, salt, iv),
    salt: saltBytes,
    iv: ivBytes,
    data: dataBytes,
  };
}

/**
 * Decrypts an encrypted export file and returns the bundle JSON inside it.
 * Throws "Wrong passphrase or damaged file." for any passphrase or file
 * problem.
 */
export async function decryptExport(text: string, passphrase: string): Promise<string> {
  const subtle = subtleCrypto();
  const envelope = readEnvelope(parseJSON(text));
  if (!envelope) throw new Error(DECRYPT_FAILED);
  try {
    const key = await deriveKey(
      subtle,
      passphrase,
      envelope.salt,
      envelope.head.kdf.iterations,
      "decrypt",
    );
    const plaintext = await subtle.decrypt(
      { name: "AES-GCM", iv: envelope.iv, additionalData: utf8(JSON.stringify(envelope.head)) },
      key,
      envelope.data,
    );
    return new TextDecoder("utf-8", { fatal: true }).decode(plaintext);
  } catch {
    throw new Error(DECRYPT_FAILED);
  }
}
