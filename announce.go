package main

import (
	"fmt"
	"log"
	"net"
	"strings"

	"github.com/hashicorp/mdns"
)

// Same DNS-SD name the Android app browses via NsdManager.
// NsdManager writes it as "_cathy._tcp." (trailing dot); this library stores
// it without the dot — keep both sides in sync if you ever rename this.
const mdnsServiceType = "_cathy._tcp"

// advertise registers the mDNS record for this backend on the LAN.
// Called from program.Start() with the actual bound port (in case
// listenAddr uses port 0 / an OS-assigned port), and torn down via
// server.Shutdown() from program.Stop().
func advertise(port int) (*mdns.Server, error) {
	ips, err := lanIPv4()
	if err != nil {
		return nil, fmt.Errorf("discover LAN IPv4: %w", err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no usable LAN IPv4 address found to advertise")
	}

	instance := strings.ToLower(serviceName) // "cathy", from main.go's serviceName const
	host := instance + ".local."

	svc, err := mdns.NewMDNSService(
		instance,
		mdnsServiceType,
		"local.",
		host,
		port,
		ips,
		[]string{"app=" + instance},
	)
	if err != nil {
		return nil, fmt.Errorf("build mdns service: %w", err)
	}

	server, err := mdns.NewServer(&mdns.Config{Zone: svc})
	if err != nil {
		return nil, fmt.Errorf("start mdns server: %w", err)
	}

	log.Printf("mdns: advertising %s instance %q on %v port %d (host=%s)",
		mdnsServiceType, instance, ips, port, host)
	return server, nil
}

// lanIPv4 returns every non-loopback, non-link-local IPv4 address on
// interfaces that are up, skipping virtual adapters that would otherwise
// get advertised and confuse a phone trying to connect (Docker, WSL,
// Hyper-V, VPN clients, etc).
func lanIPv4() ([]net.IP, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	var ips []net.IP
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if skipIface(iface.Name) {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			log.Printf("mdns: skip iface %q, cannot read addrs: %v", iface.Name, err)
			continue
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			v4 := ipNet.IP.To4()
			if v4 == nil || v4.IsLoopback() || v4.IsLinkLocalUnicast() {
				continue
			}
			ips = append(ips, v4)
		}
	}
	return ips, nil
}

func skipIface(name string) bool {
	n := strings.ToLower(name)
	for _, bad := range []string{
		"virtual", "vethernet", "vbox", "docker", "vmware",
		"bluetooth", "npcap", "wsl", "hyper-v", "tailscale", "tap", "tun",
	} {
		if strings.Contains(n, bad) {
			return true
		}
	}
	return false
}
