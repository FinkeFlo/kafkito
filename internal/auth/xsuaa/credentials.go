//go:build btp

// Copyright 2026 The kafkito Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package xsuaa

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Credentials holds the XSUAA fields kafkito needs at runtime. The shape mirrors
// VCAP_SERVICES.xsuaa[0].credentials on SAP BTP Cloud Foundry.
type Credentials struct {
	ClientID       string `json:"clientid"`
	ClientSecret   string `json:"clientsecret"`
	CredentialType string `json:"credential-type"`
	URL            string `json:"url"`            // issuer base
	UAADomain      string `json:"uaadomain"`      // jku host suffix allow-list
	XSAppName      string `json:"xsappname"`      // e.g. kafkito!t12345
	IdentityZoneID string `json:"identityzoneid"` // expected zid
	TenantMode     string `json:"tenantmode"`
}

// LocalScopePrefix returns the prefix that must be stripped from token scopes
// to obtain local scope names (e.g. "kafkito!t12345.Display" -> "Display").
func (c Credentials) LocalScopePrefix() string {
	return c.XSAppName + "."
}

// ParseCredentialsFromVCAP parses a VCAP_SERVICES JSON blob and returns the first
// xsuaa binding's credentials.
func ParseCredentialsFromVCAP(vcap string) (Credentials, error) {
	var doc struct {
		XSUAA []struct {
			Credentials Credentials `json:"credentials"`
		} `json:"xsuaa"`
	}
	if err := json.Unmarshal([]byte(vcap), &doc); err != nil {
		return Credentials{}, fmt.Errorf("parse VCAP_SERVICES: %w", err)
	}
	if len(doc.XSUAA) == 0 {
		return Credentials{}, errors.New("no xsuaa binding in VCAP_SERVICES")
	}
	return doc.XSUAA[0].Credentials, nil
}
