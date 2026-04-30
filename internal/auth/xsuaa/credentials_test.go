//go:build btp

// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package xsuaa_test

import (
	"testing"

	"github.com/FinkeFlo/kafkito/internal/auth/xsuaa"
)

const fixtureVCAP = `{
  "xsuaa": [{
    "credentials": {
      "clientid":         "sb-kafkito!t12345",
      "clientsecret":     "secret",
      "credential-type":  "binding-secret",
      "url":              "https://example-tenant.authentication.eu10.hana.ondemand.com",
      "uaadomain":        "authentication.eu10.hana.ondemand.com",
      "xsappname":        "kafkito!t12345",
      "identityzoneid":   "abc-123",
      "tenantmode":       "dedicated"
    },
    "label": "xsuaa", "name": "kafkito-uaa", "plan": "application", "tags": ["xsuaa"]
  }]
}`

func TestParseCredentialsFromVCAP(t *testing.T) {
	c, err := xsuaa.ParseCredentialsFromVCAP(fixtureVCAP)
	if err != nil {
		t.Fatalf("ParseCredentialsFromVCAP: %v", err)
	}
	if c.ClientID != "sb-kafkito!t12345" {
		t.Errorf("ClientID = %q", c.ClientID)
	}
	if c.UAADomain != "authentication.eu10.hana.ondemand.com" {
		t.Errorf("UAADomain = %q", c.UAADomain)
	}
	if c.XSAppName != "kafkito!t12345" {
		t.Errorf("XSAppName = %q", c.XSAppName)
	}
	if c.LocalScopePrefix() != "kafkito!t12345." {
		t.Errorf("LocalScopePrefix = %q", c.LocalScopePrefix())
	}
}

func TestParseCredentialsFromVCAP_NoXSUAABinding(t *testing.T) {
	if _, err := xsuaa.ParseCredentialsFromVCAP(`{"xsuaa": []}`); err == nil {
		t.Errorf("expected error for empty xsuaa array")
	}
}
