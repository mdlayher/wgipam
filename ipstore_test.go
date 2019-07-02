package wgipam_test

import (
	"fmt"
	"net"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/wgipam"
	"github.com/mikioh/ipaddr"
)

func TestIPStoreAllocate(t *testing.T) {
	var (
		sub4 = mustCIDR("192.0.2.0/30")
		sub6 = mustCIDR("2001:db8::/126")
	)

	tests := []struct {
		name    string
		subnets []*net.IPNet
		ok      bool
		check   func(t *testing.T, ipn *net.IPNet)
	}{
		{
			name: "no IPs",
		},
		{
			name:    "mixed IPv4",
			subnets: []*net.IPNet{sub4, sub6},
		},
		{
			name:    "mixed IPv6",
			subnets: []*net.IPNet{sub6, sub4},
		},
		{
			name:    "IPv4",
			subnets: []*net.IPNet{sub4},
			ok:      true,
			check: func(t *testing.T, ipn *net.IPNet) {
				if ipn.IP.To4() == nil {
					t.Fatalf("not an IPv4 address: %v", ipn.IP)
				}

				if diff := cmp.Diff(sub4.Mask, ipn.Mask); diff != "" {
					t.Fatalf("unexpected CIDR mask (-want +got):\n%s", diff)
				}
			},
		},
		{
			name:    "IPv6",
			subnets: []*net.IPNet{sub6},
			ok:      true,
			check: func(t *testing.T, ipn *net.IPNet) {
				if ipn.IP.To4() != nil {
					t.Fatalf("not an IPv6 address: %v", ipn.IP)
				}

				if diff := cmp.Diff(sub6.Mask, ipn.Mask); diff != "" {
					t.Fatalf("unexpected CIDR mask (-want +got):\n%s", diff)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ips, err := wgipam.NewIPStore(tt.subnets)
			if tt.ok && err != nil {
				t.Fatalf("failed to create IPStore: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("expected an error, but none occurred")
			}
			if err != nil {
				return
			}

			var ipns []*net.IPNet
			for {
				ip, ok, err := ips.Allocate()
				if err != nil {
					t.Fatalf("failed to allocate IPs: %v", err)
				}
				if !ok {
					break
				}

				tt.check(t, ip)
				ipns = append(ipns, ip)
			}

			want := make([]ipaddr.Prefix, 0, len(tt.subnets))
			for _, s := range tt.subnets {
				want = append(want, *ipaddr.NewPrefix(s))
			}

			var got []ipaddr.Prefix
			if len(ipns) > 0 {
				got = ipaddr.Summarize(ipns[0].IP, ipns[len(ipns)-1].IP)
			}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Fatalf("unexpected IP addresses (-want +got):\n%s", diff)
			}
		})
	}
}

func mustCIDR(s string) *net.IPNet {
	_, ipn, err := net.ParseCIDR(s)
	if err != nil {
		panicf("failed to parse CIDR: %v", err)
	}

	return ipn
}

func panicf(format string, a ...interface{}) {
	panic(fmt.Sprintf(format, a...))
}
