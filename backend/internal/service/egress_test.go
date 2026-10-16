package service

import (
	"net"
	"testing"
	"time"
)

func TestConfigureOutboundPolicyRejectsInvalidCIDR(t *testing.T) {
	if err := ConfigureOutboundPolicy(OutboundPolicy{AllowedCIDRs: []string{"not-a-cidr"}}); err == nil {
		t.Fatal("expected invalid CIDR to be rejected")
	}
}

func TestSafeDialTimeoutUsesValidatedAddress(t *testing.T) {
	if err := ConfigureOutboundPolicy(OutboundPolicy{AllowLoopback: true, AllowedCIDRs: []string{"127.0.0.0/8"}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ConfigureOutboundPolicy(OutboundPolicy{}) })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	connection, err := safeDialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	connection.Close()
	if _, err := safeDialTimeout("tcp", "[::1]:1", time.Second); err == nil {
		t.Fatal("address outside the allowed CIDR must be rejected")
	}
}
