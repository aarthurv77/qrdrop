// Pacote netutil descobre o endereço da máquina na rede local.
package netutil

import (
	"errors"
	"net"
)

// LocalIP devolve o IPv4 que a máquina usa para falar com a rede local.
//
// O truque: "conectar" um socket UDP a um IP externo não envia nenhum pacote,
// mas faz o sistema operacional escolher a interface de saída. O endereço
// local desse socket é o IP que os outros aparelhos da rede enxergam.
func LocalIP() (net.IP, error) {
	conn, err := net.Dial("udp4", "8.8.8.8:80")
	if err == nil {
		defer conn.Close()
		if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && usable(addr.IP) {
			return addr.IP.To4(), nil
		}
	}
	// Sem rota padrão (rede sem internet, roteador do celular sem dados) ou
	// com VPN: procuramos um IPv4 de rede local nas interfaces.
	ips, err := Candidates()
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, errors.New("nenhum IPv4 encontrado: o computador está conectado a alguma rede?")
	}
	return ips[0], nil
}

// Candidates lista os IPv4 deste computador que outro aparelho da rede
// poderia acessar, os de rede privada primeiro. Útil quando há mais de uma
// rede (Wi-Fi e cabo, VirtualBox, WSL) e o QR aponta para a errada: o
// usuário escolhe outro com a flag -ip.
func Candidates() ([]net.IP, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	return usableIPv4(addrs), nil
}

// usable diz se outro aparelho poderia acessar o ip: IPv4, nem loopback
// (127.x) nem link-local (169.254.x, de quando não há DHCP). Endereços
// públicos valem: algumas universidades dão IP público a cada aparelho.
func usable(ip net.IP) bool {
	ip4 := ip.To4()
	return ip4 != nil && ip4.IsGlobalUnicast()
}

// usableIPv4 filtra os endereços com usable, colocando os privados
// (10.x, 172.16-31.x, 192.168.x) antes dos públicos.
func usableIPv4(addrs []net.Addr) []net.IP {
	var private, public []net.IP
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) { // type switch: o tipo concreto por trás da interface
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		default:
			continue
		}
		switch {
		case !usable(ip):
		case ip.IsPrivate():
			private = append(private, ip.To4())
		default:
			public = append(public, ip.To4())
		}
	}
	return append(private, public...)
}
