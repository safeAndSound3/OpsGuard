package service

import "testing"

func TestConfigureOutboundPolicyRejectsInvalidCIDR(t *testing.T) {
	if err := ConfigureOutboundPolicy(OutboundPolicy{AllowedCIDRs: []string{"not-a-cidr"}}); err == nil {
		t.Fatal("expected invalid CIDR to be rejected")
	}
}
