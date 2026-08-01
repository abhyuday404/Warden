package config

import "testing"

func TestValidateRejectsSecretProviderConfig(t *testing.T) {
	m := Defaults()
	m.Deploy.Config = map[string]string{"api_token": "do-not-store"}
	if err := Validate(m); err == nil {
		t.Fatal("expected secret-bearing config to be rejected")
	}
}

func TestValidateAllowsNonSecretProviderConfig(t *testing.T) {
	m := Defaults()
	m.Deploy.Config = map[string]string{"app": "example", "region": "bom"}
	if err := Validate(m); err != nil {
		t.Fatal(err)
	}
}
