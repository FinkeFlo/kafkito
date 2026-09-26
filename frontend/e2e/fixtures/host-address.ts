import { networkInterfaces } from "node:os";

// The backend refuses loopback brokers for private clusters (SSRF guard), so
// the connection test dials the fixture broker through a private host
// address. The docker compose broker is published on all host interfaces.
export function hostAddress(): string {
  const override = process.env.KAFKITO_E2E_HOST_IP;
  if (override) return override;
  const privateV4 = /^(10\.|172\.(1[6-9]|2\d|3[01])\.|192\.168\.)/;
  for (const addrs of Object.values(networkInterfaces())) {
    for (const a of addrs ?? []) {
      if (a.family === "IPv4" && !a.internal && privateV4.test(a.address)) return a.address;
    }
  }
  throw new Error("no private IPv4 host address found; set KAFKITO_E2E_HOST_IP");
}
