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

// Host ports of the fixture broker (docker-compose.yml). Both reach the same
// broker; they differ in the address the broker advertises in its metadata.
// 39092 advertises localhost:39092, which the backend refuses for private
// clusters, so Test connection reports that broker (issue #126). 39093
// advertises this host address, which private clusters may use.
export const LOOPBACK_ADVERTISED_PORT = 39092;
const HOST_ADVERTISED_PORT = 39093;

/** Seed broker for a private cluster that works end to end. */
export function privateClusterBroker(): string {
  return `${hostAddress()}:${HOST_ADVERTISED_PORT}`;
}
