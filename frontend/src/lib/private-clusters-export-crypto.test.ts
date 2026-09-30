import { afterEach, describe, expect, it, vi } from "vitest";
import {
  exportBundle,
  importBundle,
  listPrivateClusters,
  upsertPrivateCluster,
} from "./private-clusters";
import {
  MIN_PASSPHRASE_LENGTH,
  decryptExport,
  encryptExport,
  isEncryptedExport,
  isPassphraseLongEnough,
} from "./private-clusters-export-crypto";

const PASSPHRASE = "correct horse battery staple";
const WRONG = "Wrong passphrase or damaged file.";
// Far below the production 600,000 so the suite stays fast.
const FAST = { iterations: 1_000 };

interface Envelope {
  format: string;
  version: number;
  kdf: { name: string; hash: string; iterations: number; salt: string };
  cipher: { name: string; iv: string };
  data: string;
}

function seedClusters() {
  upsertPrivateCluster({
    name: "prod-eu",
    is_prod: true,
    brokers: ["kafka-1.example.com:9093"],
    auth: { type: "scram-sha-512", username: "svc-kafkito", password: "s3cret-Pa55word" },
    tls: { enabled: true },
    schema_registry: { url: "https://sr.example.com", username: "sr", password: "sr-s3cret" },
  });
  upsertPrivateCluster({
    name: "dev",
    brokers: ["10.0.0.1:9092"],
    auth: { type: "none" },
    tls: { enabled: false },
  });
}

function plaintextExport(): string {
  return JSON.stringify(exportBundle(), null, 2);
}

function bytes(b64: string): Uint8Array<ArrayBuffer> {
  return Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
}

function b64(data: Uint8Array): string {
  return btoa(String.fromCharCode(...data));
}

function flipByte(value: string, index: number): string {
  const data = bytes(value);
  data[(index + data.length) % data.length] ^= 0x01;
  return b64(data);
}

function edit(file: string, change: (envelope: Envelope) => void): string {
  const envelope = JSON.parse(file) as Envelope;
  change(envelope);
  return JSON.stringify(envelope);
}

afterEach(() => {
  localStorage.clear();
  vi.unstubAllGlobals();
});

describe("encryptExport / decryptExport", () => {
  it("round-trips the export bundle without the credentials in the file", async () => {
    seedClusters();
    const plaintext = plaintextExport();

    const file = await encryptExport(plaintext, PASSPHRASE, FAST);

    for (const secret of ["s3cret-Pa55word", "sr-s3cret", "svc-kafkito", "prod-eu"]) {
      expect(file).not.toContain(secret);
    }
    expect(await decryptExport(file, PASSPHRASE)).toBe(plaintext);
  });

  it("writes the documented envelope", async () => {
    const plaintext = '{"schema":"kafkito.private-clusters/v1","clusters":[]}';
    const file = await encryptExport(plaintext, PASSPHRASE, FAST);
    const envelope = JSON.parse(file) as Envelope;

    expect(Object.keys(envelope)).toEqual(["format", "version", "kdf", "cipher", "data"]);
    expect(envelope).toEqual({
      format: "kafkito.private-clusters.export",
      version: 2,
      kdf: { name: "PBKDF2", hash: "SHA-256", iterations: 1_000, salt: expect.any(String) },
      cipher: { name: "AES-GCM", iv: expect.any(String) },
      data: expect.any(String),
    });
    expect(bytes(envelope.kdf.salt)).toHaveLength(16);
    expect(bytes(envelope.cipher.iv)).toHaveLength(12);
    // Ciphertext plus the 16-byte GCM tag.
    expect(bytes(envelope.data)).toHaveLength(new TextEncoder().encode(plaintext).length + 16);
  });

  it("derives the key with 600,000 PBKDF2 iterations by default", async () => {
    const file = await encryptExport("{}", PASSPHRASE);
    expect((JSON.parse(file) as Envelope).kdf.iterations).toBe(600_000);
    expect(await decryptExport(file, PASSPHRASE)).toBe("{}");
  });

  it("draws a new salt and IV for every export", async () => {
    const a = JSON.parse(await encryptExport("{}", PASSPHRASE, FAST)) as Envelope;
    const b = JSON.parse(await encryptExport("{}", PASSPHRASE, FAST)) as Envelope;
    expect(a.kdf.salt).not.toBe(b.kdf.salt);
    expect(a.cipher.iv).not.toBe(b.cipher.iv);
    expect(a.data).not.toBe(b.data);
  });

  it("refuses a wrong passphrase with the generic message", async () => {
    const file = await encryptExport("{}", PASSPHRASE, FAST);
    await expect(decryptExport(file, `${PASSPHRASE}!`)).rejects.toThrow(WRONG);
    await expect(decryptExport(file, "")).rejects.toThrow(WRONG);
  });

  it("treats the NFC and NFD forms of a passphrase alike", async () => {
    const composed = "Kennwort für café".normalize("NFC");
    const file = await encryptExport("{}", composed, FAST);
    expect(await decryptExport(file, composed.normalize("NFD"))).toBe("{}");
  });

  it.each<[string, (e: Envelope) => void]>([
    [
      "a ciphertext byte",
      (e) => {
        e.data = flipByte(e.data, 0);
      },
    ],
    [
      "the GCM tag",
      (e) => {
        e.data = flipByte(e.data, -1);
      },
    ],
    [
      "the IV",
      (e) => {
        e.cipher.iv = flipByte(e.cipher.iv, 0);
      },
    ],
    [
      "the salt",
      (e) => {
        e.kdf.salt = flipByte(e.kdf.salt, 0);
      },
    ],
    [
      "the iteration count",
      (e) => {
        e.kdf.iterations += 1;
      },
    ],
    [
      "the version",
      (e) => {
        e.version = 3;
      },
    ],
    [
      "the KDF hash",
      (e) => {
        e.kdf.hash = "SHA-512";
      },
    ],
    [
      "the KDF name",
      (e) => {
        e.kdf.name = "scrypt";
      },
    ],
    [
      "the cipher name",
      (e) => {
        e.cipher.name = "AES-CBC";
      },
    ],
    [
      "a truncated ciphertext",
      (e) => {
        e.data = b64(bytes(e.data).subarray(0, 15));
      },
    ],
    [
      "a short salt",
      (e) => {
        e.kdf.salt = b64(bytes(e.kdf.salt).subarray(0, 8));
      },
    ],
    [
      "a long IV",
      (e) => {
        e.cipher.iv = b64(new Uint8Array(16));
      },
    ],
    [
      "non-base64 data",
      (e) => {
        e.data = `${e.data.slice(0, -4)}!!!!`;
      },
    ],
    [
      "a zero iteration count",
      (e) => {
        e.kdf.iterations = 0;
      },
    ],
    [
      "a fractional iteration count",
      (e) => {
        e.kdf.iterations = 1000.5;
      },
    ],
    // Refused before any key derivation, or this test would time out.
    [
      "an excessive iteration count",
      (e) => {
        e.kdf.iterations = 1e9;
      },
    ],
    [
      "a missing salt",
      (e) => {
        Reflect.deleteProperty(e.kdf, "salt");
      },
    ],
    [
      "missing data",
      (e) => {
        Reflect.deleteProperty(e, "data");
      },
    ],
  ])("fails with the generic message after changing %s", async (_, change) => {
    const file = await encryptExport('{"clusters":[]}', PASSPHRASE, FAST);
    await expect(decryptExport(edit(file, change), PASSPHRASE)).rejects.toThrow(WRONG);
  });

  it.each(["not json", "null", "[]", '{"format":"kafkito.private-clusters.export"}'])(
    "fails with the generic message for %s",
    async (text) => {
      await expect(decryptExport(text, PASSPHRASE)).rejects.toThrow(WRONG);
    },
  );

  // Rebuilds the ciphertext with the key and IV of a real file. Only the
  // documented header as additional data decrypts; none or another fails.
  it("authenticates the header as additional data", async () => {
    const file = await encryptExport("{}", PASSPHRASE, FAST);
    const envelope = JSON.parse(file) as Envelope;
    const head = {
      format: envelope.format,
      version: envelope.version,
      kdf: envelope.kdf,
      cipher: envelope.cipher,
    };
    const material = await crypto.subtle.importKey(
      "raw",
      new TextEncoder().encode(PASSPHRASE),
      "PBKDF2",
      false,
      ["deriveKey"],
    );
    const key = await crypto.subtle.deriveKey(
      { name: "PBKDF2", hash: "SHA-256", salt: bytes(head.kdf.salt), iterations: 1_000 },
      material,
      { name: "AES-GCM", length: 256 },
      false,
      ["encrypt"],
    );
    const seal = async (additionalData?: Uint8Array<ArrayBuffer>) =>
      JSON.stringify({
        ...head,
        data: b64(
          new Uint8Array(
            await crypto.subtle.encrypt(
              { name: "AES-GCM", iv: bytes(head.cipher.iv), additionalData },
              key,
              new TextEncoder().encode('{"forged":true}'),
            ),
          ),
        ),
      });

    const documented = new TextEncoder().encode(JSON.stringify(head));
    expect(await decryptExport(await seal(documented), PASSPHRASE)).toBe('{"forged":true}');
    await expect(decryptExport(await seal(), PASSPHRASE)).rejects.toThrow(WRONG);
    const other = new TextEncoder().encode(JSON.stringify({ ...head, version: 1 }));
    await expect(decryptExport(await seal(other), PASSPHRASE)).rejects.toThrow(WRONG);
  });

  it("refuses a passphrase shorter than the minimum", async () => {
    expect(MIN_PASSPHRASE_LENGTH).toBe(12);
    expect(isPassphraseLongEnough("a".repeat(11))).toBe(false);
    expect(isPassphraseLongEnough("a".repeat(12))).toBe(true);
    // Counts characters, not UTF-16 code units.
    expect(isPassphraseLongEnough("\u{1D400}".repeat(6))).toBe(false);
    expect(isPassphraseLongEnough("\u{1D400}".repeat(12))).toBe(true);
    await expect(encryptExport("{}", "a".repeat(11), FAST)).rejects.toThrow(
      "The passphrase needs at least 12 characters.",
    );
  });

  it("says that it needs a secure connection when WebCrypto is missing", async () => {
    const file = await encryptExport("{}", PASSPHRASE, FAST);
    vi.stubGlobal("crypto", { getRandomValues: crypto.getRandomValues.bind(crypto) });
    const message = "Encrypted exports need a secure connection (HTTPS or localhost).";
    await expect(encryptExport("{}", PASSPHRASE, FAST)).rejects.toThrow(message);
    await expect(decryptExport(file, PASSPHRASE)).rejects.toThrow(message);
  });
});

describe("isEncryptedExport", () => {
  it("recognises an encrypted export", async () => {
    expect(isEncryptedExport(await encryptExport("{}", PASSPHRASE, FAST))).toBe(true);
  });

  it.each([
    ["a plaintext export", JSON.stringify(exportBundle(), null, 2)],
    ["invalid JSON", "{"],
    ["null", "null"],
    ["an array", "[]"],
    ["another format", '{"format":"something-else","version":2}'],
  ])("rejects %s", (_, text) => {
    expect(isEncryptedExport(text)).toBe(false);
  });
});

describe("plaintext exports from earlier versions", () => {
  it("still import, and an encrypted export imports the same clusters", async () => {
    seedClusters();
    const clusters = listPrivateClusters();
    const plaintext = plaintextExport();
    const file = await encryptExport(plaintext, PASSPHRASE, FAST);

    localStorage.clear();
    expect(isEncryptedExport(plaintext)).toBe(false);
    expect(importBundle(plaintext)).toEqual({ added: 2, updated: 0, skipped: 0 });
    expect(listPrivateClusters()).toEqual(clusters);

    localStorage.clear();
    expect(importBundle(await decryptExport(file, PASSPHRASE))).toEqual({
      added: 2,
      updated: 0,
      skipped: 0,
    });
    expect(listPrivateClusters()).toEqual(clusters);
  });
});
