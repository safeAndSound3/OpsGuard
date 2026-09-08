package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// OutboundPolicy limits where configured checks and data sources may connect.
// CIDR and host allowlists are optional for backwards-compatible intranet use;
// unsafe special-purpose addresses are always rejected.
type OutboundPolicy struct {
	AllowedHosts  []string
	AllowedCIDRs  []string
	AllowLoopback bool
}

var outboundPolicy = struct {
	sync.RWMutex
	allowHosts []string
	allowCIDRs []netip.Prefix
	loopback   bool
}{}

func ConfigureOutboundPolicy(policy OutboundPolicy) error {
	prefixes := make([]netip.Prefix, 0, len(policy.AllowedCIDRs))
	for _, raw := range policy.AllowedCIDRs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("invalid OUTBOUND_ALLOWED_CIDRS entry %q", raw)
		}
		prefixes = append(prefixes, prefix)
	}
	hosts := make([]string, 0, len(policy.AllowedHosts))
	for _, host := range policy.AllowedHosts {
		if host = strings.ToLower(strings.TrimSpace(host)); host != "" {
			hosts = append(hosts, strings.TrimPrefix(host, "."))
		}
	}
	outboundPolicy.Lock()
	outboundPolicy.allowHosts, outboundPolicy.allowCIDRs, outboundPolicy.loopback = hosts, prefixes, policy.AllowLoopback
	outboundPolicy.Unlock()
	return nil
}

func validateOutboundAddress(host string) error {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" {
		return errors.New("outbound target host is required")
	}
	outboundPolicy.RLock()
	hosts, prefixes, loopback := append([]string(nil), outboundPolicy.allowHosts...), append([]netip.Prefix(nil), outboundPolicy.allowCIDRs...), outboundPolicy.loopback
	outboundPolicy.RUnlock()
	if len(hosts) > 0 {
		matched := false
		lower := strings.ToLower(host)
		for _, allowed := range hosts {
			if lower == allowed || strings.HasSuffix(lower, "."+allowed) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("outbound host %q is not allowlisted", host)
		}
	}
	addresses, err := net.DefaultResolver.LookupNetIP(context.Background(), "ip", host)
	if err != nil {
		return fmt.Errorf("resolve outbound host %q: %w", host, err)
	}
	for _, ip := range addresses {
		if !loopback && ip.IsLoopback() {
			return fmt.Errorf("outbound loopback target %q is not allowed", host)
		}
		if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("unsafe outbound target %q is not allowed", ip)
		}
		if len(prefixes) > 0 {
			matched := false
			for _, prefix := range prefixes {
				if prefix.Contains(ip) {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("outbound IP %q is outside OUTBOUND_ALLOWED_CIDRS", ip)
			}
		}
	}
	return nil
}

func safeHTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	dialer := &net.Dialer{}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if err := validateOutboundAddress(host); err != nil {
			return nil, err
		}
		return dialer.DialContext(ctx, network, address)
	}
	return &http.Client{Timeout: timeout, Transport: transport, CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		if err := validateOutboundAddress(req.URL.Hostname()); err != nil {
			return err
		}
		return nil
	}}
}

func safeDialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if err := validateOutboundAddress(host); err != nil {
		return nil, err
	}
	return net.DialTimeout(network, address, timeout)
}
