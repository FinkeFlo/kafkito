// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

// export_test.go exposes internal helpers for white-box tests in the external
// netguard_test package. Nothing here is compiled into the production binary.

package netguard

import (
	"context"
	"net"
	"net/netip"
)

// GuardedDialWithForTest wraps guardedDialWith for use in tests. Both the
// resolver and the per-IP dial function can be replaced with stubs, enabling
// fully hermetic failover tests without real DNS or network connectivity. SSRF
// blocking rules are still enforced: every resolved address must pass BlockedIP.
func GuardedDialWithForTest(
	resolve func(ctx context.Context, network, host string) ([]netip.Addr, error),
	dialOne func(ctx context.Context, network, addr string) (net.Conn, error),
) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return guardedDialWith(resolve, dialOne)
}
