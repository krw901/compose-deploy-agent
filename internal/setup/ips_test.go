package setup

import (
	"net/netip"
	"slices"
	"testing"
)

func TestFilterCandidates(t *testing.T) {
	in := []CandidateIP{
		{
			Interface: "eth0",
			Addr:      netip.MustParseAddr("10.0.0.5"),
		},
		{
			Interface: "eth0",
			Addr:      netip.MustParseAddr("fe80::1"),
		},
		{
			Interface: "eth1",
			Addr:      netip.MustParseAddr("192.168.1.10"),
		},
		{
			Interface: "eth1",
			Addr:      netip.MustParseAddr("2001:db8::1"),
		},
		{
			Interface: "lo",
			Addr:      netip.MustParseAddr("127.0.0.1"),
		},
		{
			Interface: "eth2",
			Addr:      netip.MustParseAddr("169.254.10.1"),
		},
		{
			Interface: "docker0",
			Addr:      netip.MustParseAddr("172.17.0.1"),
		},
		{
			Interface: "br-1a2b3c",
			Addr:      netip.MustParseAddr("172.18.0.1"),
		},
		{
			Interface: "veth12ab",
			Addr:      netip.MustParseAddr("172.18.0.5"),
		},
	}
	got := FilterCandidates(in)
	want := []CandidateIP{
		{
			Interface: "eth0",
			Addr:      netip.MustParseAddr("10.0.0.5"),
		},
		{
			Interface: "eth1",
			Addr:      netip.MustParseAddr("192.168.1.10"),
		},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("FilterCandidates = %v, want %v", got, want)
	}
}

func TestParseIPList(t *testing.T) {
	got, err := ParseIPList(" 10.0.0.5, ,10.0.0.6,10.0.0.5 ", english)
	if err != nil {
		t.Fatalf("ParseIPList: %v", err)
	}
	if want := []string{"10.0.0.5", "10.0.0.6"}; !slices.Equal(addrStrings(got), want) {
		t.Fatalf("ParseIPList = %v, want %v", got, want)
	}

	for _, bad := range []string{"10.0.0", "::1", "example.com"} {
		if _, err := ParseIPList(bad, english); err == nil {
			t.Errorf("ParseIPList(%q): expected error", bad)
		}
	}
}
