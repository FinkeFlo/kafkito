// Vitest setup: ensure a working localStorage/sessionStorage in the happy-dom
// environment. Recent Node versions (25+) ship with an experimental
// webstorage implementation on globalThis that is crippled without a
// --localstorage-file argument, and it can shadow the happy-dom version. We
// replace both globals with a small in-memory Storage so tests behave like a
// real browser regardless of the Node version running them.

import { beforeEach } from "vitest";
import "@testing-library/jest-dom/vitest";

class MemoryStorage implements Storage {
  private map = new Map<string, string>();
  get length(): number {
    return this.map.size;
  }
  clear(): void {
    this.map.clear();
  }
  getItem(key: string): string | null {
    return this.map.has(key) ? (this.map.get(key) as string) : null;
  }
  key(index: number): string | null {
    return Array.from(this.map.keys())[index] ?? null;
  }
  removeItem(key: string): void {
    this.map.delete(key);
  }
  setItem(key: string, value: string): void {
    this.map.set(key, String(value));
  }
}

function install(name: "localStorage" | "sessionStorage") {
  const storage = new MemoryStorage();
  Object.defineProperty(globalThis, name, {
    configurable: true,
    writable: true,
    value: storage,
  });
  if (typeof window !== "undefined") {
    Object.defineProperty(window, name, {
      configurable: true,
      writable: true,
      value: storage,
    });
  }
}

install("localStorage");
install("sessionStorage");

beforeEach(() => {
  install("localStorage");
  install("sessionStorage");
});
