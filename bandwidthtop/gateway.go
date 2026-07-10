package bandwidthtop

import (
	"net"
	"net/url"
	"strings"
)

const monitorPort = "8080"

// MonitorServerURL applies explicit-server and discovery opt-out precedence.
// The bool reports whether the returned URL came from gateway discovery.
func MonitorServerURL(explicit string, disableDiscovery bool, gateway net.IP, zone string) (string, bool) {
	if explicit != "" {
		return explicit, false
	}
	if disableDiscovery {
		return "", false
	}
	discovered, ok := gatewayMonitorURL(gateway, zone)
	return discovered, ok
}

func gatewayMonitorURL(gateway net.IP, zone string) (string, bool) {
	if gateway == nil || gateway.IsUnspecified() || gateway.IsLoopback() || gateway.IsMulticast() {
		return "", false
	}
	host := gateway.String()
	if gateway.To4() == nil && gateway.IsLinkLocalUnicast() {
		if zone == "" {
			return "", false
		}
		host += "%" + zone
	}
	u := &url.URL{Scheme: "http", Host: net.JoinHostPort(host, monitorPort)}
	return u.String(), true
}

func discoveryURLAllowed(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Port() != monitorPort ||
		u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	host := u.Hostname()
	if zoneAt := strings.LastIndexByte(host, '%'); zoneAt >= 0 {
		host = host[:zoneAt]
	}
	return net.ParseIP(host) != nil
}
