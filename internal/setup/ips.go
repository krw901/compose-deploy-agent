// Package setup implements `deploy-agent setup`, the interactive first-run
// wizard that writes serve.env, initializes the local CA (or reissues the
// server certificate) and optionally generates services.yaml from the
// docker compose projects found on the host.
package setup

import (
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
)

// dockerInterfacePrefixes are interface name prefixes created by docker for
// bridge networks and container veth pairs; their addresses are never
// reachable from other machines.
var dockerInterfacePrefixes = []string{"docker", "br-", "veth"}

// CandidateIP is an IPv4 address assigned to a local network interface.
type CandidateIP struct {
	Interface string
	Addr      netip.Addr
}

func (c CandidateIP) String() string {
	return fmt.Sprintf("%s (%s)", c.Addr, c.Interface)
}

// DetectIPs returns the IPv4 addresses of this host that another machine
// could plausibly reach the agent at.
func DetectIPs() ([]CandidateIP, error) {
	addrs, err := interfaceAddrs()
	if err != nil {
		return nil, err
	}
	return FilterCandidates(addrs), nil
}

func interfaceAddrs() ([]CandidateIP, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list network interfaces: %w", err)
	}
	var out []CandidateIP
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, fmt.Errorf("list addresses of %s: %w", iface.Name, err)
		}
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if addr, ok := netip.AddrFromSlice(ipNet.IP); ok {
				out = append(out, CandidateIP{
					Interface: iface.Name,
					Addr:      addr.Unmap(),
				})
			}
		}
	}
	return out, nil
}

// FilterCandidates keeps the IPv4 addresses that are neither loopback nor
// link-local and do not belong to a docker-managed interface.
func FilterCandidates(addrs []CandidateIP) []CandidateIP {
	var out []CandidateIP
	for _, c := range addrs {
		if !c.Addr.Is4() || c.Addr.IsLoopback() || c.Addr.IsLinkLocalUnicast() || c.Addr.IsUnspecified() {
			continue
		}
		if isDockerInterface(c.Interface) {
			continue
		}
		out = append(out, c)
	}
	return out
}

func isDockerInterface(name string) bool {
	return slices.ContainsFunc(dockerInterfacePrefixes, func(prefix string) bool {
		return strings.HasPrefix(name, prefix)
	})
}

// ParseIPList parses a comma-separated list of IPv4 addresses, dropping
// empty entries and duplicates while preserving order. Errors are worded in
// the language of text.
func ParseIPList(s string, text Text) ([]netip.Addr, error) {
	var out []netip.Addr
	for field := range strings.SplitSeq(s, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		addr, err := ParseIPv4(field, text)
		if err != nil {
			return nil, err
		}
		out = appendUnique(out, addr)
	}
	return out, nil
}

// ParseIPv4 parses a single IPv4 address. The agent's bind address is
// IPv4-only; certificate SANs may also carry other IPs and DNS names.
func ParseIPv4(s string, text Text) (netip.Addr, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return netip.Addr{}, fmt.Errorf(text.ErrBadIP, s)
	}
	if !addr.Is4() {
		return netip.Addr{}, fmt.Errorf(text.ErrNotIPv4, s)
	}
	return addr, nil
}

func appendUnique(list []netip.Addr, addrs ...netip.Addr) []netip.Addr {
	for _, addr := range addrs {
		if !slices.Contains(list, addr) {
			list = append(list, addr)
		}
	}
	return list
}

func addrStrings(addrs []netip.Addr) []string {
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.String())
	}
	return out
}
