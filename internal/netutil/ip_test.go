package netutil

import (
	"net"
	"testing"
)

func TestUsableIPv4(t *testing.T) {
	cidr := func(s string) net.Addr {
		ip, n, err := net.ParseCIDR(s)
		if err != nil {
			t.Fatal(err)
		}
		n.IP = ip
		return n
	}
	addrs := []net.Addr{
		cidr("127.0.0.1/8"),                         // loopback: o celular não alcança
		cidr("192.168.0.12/24"),                     // Wi-Fi de casa
		cidr("8.8.8.8/32"),                          // público (vale: há redes assim)
		cidr("fe80::1/64"),                          // IPv6 link-local
		cidr("10.0.0.5/8"),                          // rede da empresa / faculdade
		cidr("172.20.10.2/28"),                      // roteador do iPhone
		&net.IPAddr{IP: net.ParseIP("169.254.1.1")}, // sem DHCP
	}
	got := usableIPv4(addrs)
	// privados primeiro, na ordem em que apareceram; depois os públicos
	want := []string{"192.168.0.12", "10.0.0.5", "172.20.10.2", "8.8.8.8"}
	if len(got) != len(want) {
		t.Fatalf("got %v, queria %v", got, want)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Errorf("posição %d: got %s, queria %s", i, got[i], want[i])
		}
	}
}

func TestLocalIP(t *testing.T) {
	ip, err := LocalIP()
	if err != nil {
		t.Skipf("máquina sem rede: %v", err)
	}
	if !usable(ip) {
		t.Errorf("LocalIP devolveu %s, que outro aparelho não acessaria", ip)
	}
}
