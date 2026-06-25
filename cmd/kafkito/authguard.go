// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package main

import (
	"errors"
	"net"
	"strings"
)

// guardAuthMode returns an error when running with mode "off" (no
// authentication) would be unsafe. "off" is allowed only when the bind address
// is loopback, unless the operator explicitly acknowledges the risk via
// KAFKITO_INSECURE_AUTH_OFF=true. Any Cloud Foundry deployment (VCAP_APPLICATION
// present) is always refused regardless of the acknowledgement.
func guardAuthMode(mode, addr string, env func(string) string) error {
	if mode != "off" {
		return nil
	}
	if env("VCAP_APPLICATION") != "" {
		return errors.New("KAFKITO_AUTH_MODE=off is forbidden when running on Cloud Foundry")
	}
	if strings.EqualFold(env("KAFKITO_INSECURE_AUTH_OFF"), "true") {
		return nil
	}
	if isLoopbackBind(addr) {
		return nil
	}
	return errors.New("KAFKITO_AUTH_MODE=off requires a loopback bind address or KAFKITO_INSECURE_AUTH_OFF=true")
}

// isLoopbackBind reports whether addr binds only to the loopback interface.
func isLoopbackBind(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = strings.TrimSuffix(addr, ":")
	}
	host = strings.TrimSpace(host)
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
