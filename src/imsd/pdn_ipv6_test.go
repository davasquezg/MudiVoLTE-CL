package main

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

// --- PDN: IPv6 in CGCONTRDP and on the interfaces (docs/PROTOCOLS.md §2.3, "IPv6") ---

func TestCGCONTRDPAddrForms(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"198.51.100.20", "198.51.100.20"},
		{"198.51.100.20/30", "198.51.100.20"},
		{"10.1.2.3.255.255.255.0", "10.1.2.3"},
		{"10.1.2.3 255.255.255.0", "10.1.2.3"},
		// IPv6 in the dot notation of +CGPIAF: 16 octets, 32 with the mask.
		{"32.1.13.184.0.0.0.0.0.0.0.0.0.0.0.5", "2001:db8::5"},
		{"32.1.13.184.0.0.0.0.0.0.0.0.0.0.0.5.255.255.255.255.255.255.255.255.0.0.0.0.0.0.0.0", "2001:db8::5"},
		// IPv6 in the colon notation, with and without the prefix length.
		{"2001:db8::5", "2001:db8::5"},
		{"2001:DB8:0:0:0:0:0:5/64", "2001:db8::5"},
		{"2001:db8::5 ffff:ffff:ffff:ffff::", "2001:db8::5"},
		{"", ""},
		{"0.0.0.0", ""},
		{"::", ""},
		{"::1", ""},
		{"127.0.0.1", ""},
		{"1.2.3", ""},
		{"32.1.13.184.0.0.0.0.0.0.0.0.0.0.0.256", ""},
		{"32.1.13.184.0.0.0.0.0.0.0.0.0.0.0.+5", ""},
	} {
		ip := cgcontrdpAddr(tc.in)
		got := ""
		if ip != nil {
			got = ip.String()
		}
		if got != tc.want {
			t.Errorf("%q: %q, want %q", tc.in, got, tc.want)
		}
		if ip != nil && strings.Contains(tc.want, ".") && len(ip) != net.IPv4len {
			t.Errorf("%q: an IPv4 address of %d bytes", tc.in, len(ip))
		}
	}
}

func TestParseCGCONTRDPIPv6(t *testing.T) {
	// A dual-stack PDN (TS 27.007 §10.1.23): the IPv4 line, then the IPv6 one,
	// each with the P-CSCFs of its family.
	out := "+CGCONTRDP: 2,6,\"ims\",\"198.51.100.20\",,\"203.0.113.105\",\"203.0.113.106\",\"192.0.2.20\",\"192.0.2.28\"\r\n" +
		"+CGCONTRDP: 2,6,\"ims\",\"32.1.13.184.0.0.0.0.0.0.0.0.0.0.0.5.255.255.255.255.255.255.255.255.0.0.0.0.0.0.0.0\",," +
		"\"32.1.13.184.0.0.0.0.0.0.0.0.0.0.0.53\",,\"2001:db8:1::20\",\"2001:db8:1::28\"\r\nOK\r\n"
	info := parseCGCONTRDP(out)
	if !info.Local.Equal(net.ParseIP("198.51.100.20")) || !info.Local6.Equal(net.ParseIP("2001:db8::5")) {
		t.Errorf("local: %v / %v", info.Local, info.Local6)
	}
	if fmtIPs(info.PCSCF) != "192.0.2.20, 192.0.2.28" {
		t.Errorf("IPv4 P-CSCF: %v", info.PCSCF)
	}
	if fmtIPs(info.PCSCF6) != "2001:db8:1::20, 2001:db8:1::28" {
		t.Errorf("IPv6 P-CSCF (the DNS server must not get here): %v", info.PCSCF6)
	}
	// An IPv6-only PDN: one line, no IPv4 anywhere.
	info = parseCGCONTRDP("+CGCONTRDP: 2,6,\"ims\",\"2001:db8::5/64\",,,,\"2001:db8:1::20\"\r\nOK")
	if info.Local != nil || info.PCSCF != nil || !info.Local6.Equal(net.ParseIP("2001:db8::5")) || len(info.PCSCF6) != 1 {
		t.Errorf("IPv6 only: %+v", info)
	}
}

// fakeIfaces puts rmnet_data0 (IMS) and rmnet_data1 (internet, the default
// route) in place of the system's interfaces.
func fakeIfaces(t *testing.T, ims []net.Addr) {
	oldIf, oldAd, oldDef := netIfaces, netAddrs, defaultRouteIface
	t.Cleanup(func() { netIfaces, netAddrs, defaultRouteIface = oldIf, oldAd, oldDef })
	netIfaces = func() ([]net.Interface, error) {
		return []net.Interface{{Name: "rmnet_data0"}, {Name: "rmnet_data1"}}, nil
	}
	netAddrs = func(name string) ([]net.Addr, error) {
		switch name {
		case "rmnet_data0":
			return ims, nil
		case "rmnet_data1":
			return []net.Addr{&net.IPNet{IP: net.ParseIP("10.181.173.198")}, &net.IPNet{IP: net.ParseIP("2001:db8:ff::1")}}, nil
		}
		return nil, errors.New("no such interface")
	}
	defaultRouteIface = func() string { return "rmnet_data1" }
}

func TestFindIMSIfaceIPv6Only(t *testing.T) {
	fakeIfaces(t, []net.Addr{&net.IPNet{IP: net.ParseIP("fe80::1")}, &net.IPNet{IP: net.ParseIP("2001:db8::5")}})
	// Auto mode names the IPv6-only interface as a hint, without blaming
	// IPv6: it may be another PDN.
	_, _, err := findIMSIface("auto", nil)
	if err == nil || errors.Is(err, errIPv6Only) || !strings.Contains(err.Error(), "(rmnet_data0 has only IPv6 (2001:db8::5), no IPv4 address)") {
		t.Errorf("auto: %v", err)
	}
	if _, _, err := findIMSIface("rmnet_data0", nil); !errors.Is(err, errIPv6Only) {
		t.Errorf("a named interface: %v", err)
	}
	// Link-local alone is no IPv6 PDN: the old error stays.
	fakeIfaces(t, []net.Addr{&net.IPNet{IP: net.ParseIP("fe80::1")}})
	if _, _, err := findIMSIface("auto", nil); err == nil || errors.Is(err, errIPv6Only) {
		t.Errorf("link-local only: %v", err)
	}
}

// An IPv6-only rmnet_data* beside one with no address yet (the IMS PDN coming
// up) is a hint, not errIPv6Only.
func TestFindIMSIfaceIPv6Beside(t *testing.T) {
	oldIf, oldAd, oldDef := netIfaces, netAddrs, defaultRouteIface
	t.Cleanup(func() { netIfaces, netAddrs, defaultRouteIface = oldIf, oldAd, oldDef })
	netIfaces = func() ([]net.Interface, error) {
		return []net.Interface{{Name: "rmnet_data0"}, {Name: "rmnet_data1"}, {Name: "rmnet_data2"}}, nil
	}
	netAddrs = func(name string) ([]net.Addr, error) {
		switch name {
		case "rmnet_data0":
			return nil, nil
		case "rmnet_data1":
			return []net.Addr{&net.IPNet{IP: net.ParseIP("10.181.173.198")}}, nil
		case "rmnet_data2":
			return []net.Addr{&net.IPNet{IP: net.ParseIP("2001:db8:ff::1")}}, nil
		}
		return nil, errors.New("no such interface")
	}
	defaultRouteIface = func() string { return "rmnet_data1" }
	_, _, err := findIMSIface("auto", nil)
	if err == nil || errors.Is(err, errIPv6Only) || !strings.Contains(err.Error(), "rmnet_data2 has only IPv6 (2001:db8:ff::1)") {
		t.Errorf("two rmnet_data* off the default route: %v", err)
	}
}

// cgcontrdpLink answers CGCONTRDP with reply, as the modem does on the line's
// subscription.
func cgcontrdpLink(reply string) *ATLink {
	return &ATLink{Retries: 1, Run: func(ctx context.Context, cmd string) (string, error) {
		if strings.Contains(cmd, "CGCONTRDP") {
			return "+QUIMSUB: 1\r\n" + reply + "\r\nOK\r\n", nil
		}
		return "+QUIMSUB: 1\r\nOK\r\n", nil
	}}
}

func TestDiscoverPDNIPv6(t *testing.T) {
	v4 := []net.Addr{&net.IPNet{IP: net.ParseIP("198.51.100.20")}, &net.IPNet{IP: net.ParseIP("2001:db8::5")}}
	ctx := context.Background()

	// Dual stack: the IPv4 P-CSCFs are used, the IPv6 ones left out.
	fakeIfaces(t, v4)
	link := cgcontrdpLink("+CGCONTRDP: 2,6,\"ims\",\"198.51.100.20\",,,,\"192.0.2.20\"\r\n" +
		"+CGCONTRDP: 2,6,\"ims\",\"2001:db8::5\",,,,\"2001:db8:1::20\",\"2001:db8:1::28\"")
	env, err := discoverPDN(ctx, defaultConfig(), link, 2, quietLog())
	if err != nil || env.iface != "rmnet_data0" || fmtIPs(env.pcscf) != "192.0.2.20" {
		t.Errorf("dual stack: %+v, %v", env, err)
	}

	// CGCONTRDP shows only IPv6 (link-local or global) while the IMS
	// interface has IPv4: the PDN is still found on the interfaces.
	for _, local6 := range []string{"254.128.0.0.0.0.0.0.0.0.0.0.0.0.0.5", "2001:db8::5"} {
		link = cgcontrdpLink("+CGCONTRDP: 2,6,\"ims\",\"" + local6 + "\",,,,\"192.0.2.20\"")
		env, err = discoverPDN(ctx, defaultConfig(), link, 2, quietLog())
		if err != nil || env.iface != "rmnet_data0" || !env.local.Equal(net.ParseIP("198.51.100.20")) || fmtIPs(env.pcscf) != "192.0.2.20" {
			t.Errorf("IPv6 in CGCONTRDP, IPv4 on the interface (%s): %+v, %v", local6, env, err)
		}
	}

	// The PDN is IPv6 only in CGCONTRDP and on the interface.
	fakeIfaces(t, []net.Addr{&net.IPNet{IP: net.ParseIP("2001:db8::5")}})
	link = cgcontrdpLink("+CGCONTRDP: 2,6,\"ims\",\"2001:db8::5\",,,,\"2001:db8:1::20\"")
	_, err = discoverPDN(ctx, defaultConfig(), link, 2, quietLog())
	if !errors.Is(err, errIPv6Only) || !strings.Contains(err.Error(), "only the IPv6 address 2001:db8::5") {
		t.Errorf("IPv6-only PDN: %v", err)
	}

	// Link-local in CGCONTRDP is no IPv6 PDN: the interface error stays.
	link = cgcontrdpLink("+CGCONTRDP: 2,6,\"ims\",\"fe80::5\",,,,\"2001:db8:1::20\"")
	if _, err = discoverPDN(ctx, defaultConfig(), link, 2, quietLog()); err == nil || errors.Is(err, errIPv6Only) {
		t.Errorf("link-local in CGCONTRDP: %v", err)
	}

	// An IPv4 address, IPv6 P-CSCFs only.
	fakeIfaces(t, v4)
	link = cgcontrdpLink("+CGCONTRDP: 2,6,\"ims\",\"198.51.100.20\",,,,\"2001:db8:1::20\"")
	_, err = discoverPDN(ctx, defaultConfig(), link, 2, quietLog())
	if !errors.Is(err, errIPv6Only) || !strings.Contains(err.Error(), "P-CSCFs from CGCONTRDP are IPv6 only (2001:db8:1::20)") {
		t.Errorf("IPv6 P-CSCFs only: %v", err)
	}

	// The same from the config.
	cfg := defaultConfig()
	cfg.PCSCF = []string{"2001:db8:1::20"}
	link = cgcontrdpLink("+CGCONTRDP: 2,6,\"ims\",\"198.51.100.20\",,,,\"192.0.2.20\"")
	if _, err = discoverPDN(ctx, cfg, link, 2, quietLog()); !errors.Is(err, errIPv6Only) || !strings.Contains(err.Error(), "the config") {
		t.Errorf("IPv6 P-CSCFs in the config: %v", err)
	}

	// No CGCONTRDP, an interface with IPv6 only: the interface is named as
	// a hint, without blaming IPv6 (it may be another PDN).
	fakeIfaces(t, []net.Addr{&net.IPNet{IP: net.ParseIP("2001:db8::5")}})
	link = &ATLink{Retries: 1, Run: func(ctx context.Context, cmd string) (string, error) {
		return "", errors.New("no modem")
	}}
	if _, err = discoverPDN(ctx, defaultConfig(), link, 2, quietLog()); err == nil || errors.Is(err, errIPv6Only) ||
		!strings.Contains(err.Error(), "rmnet_data0 has only IPv6 (2001:db8::5)") {
		t.Errorf("IPv6 only on the interface: %v", err)
	}
}
