package common

import (
	"net"
	"strings"
)

// Security whitelist knobs shared by the middleware layer. Both values are
// operator options (see model/option.go) and are deliberately kept as raw
// strings: parsing happens on every check so an option change takes effect
// immediately without restarting the process.
var (
	// CorsAllowOrigins is a comma-separated origin whitelist, e.g.
	// "https://console.example.com,https://admin.example.com". When it is
	// empty only same-origin browser requests pass the CORS layer; "*" keeps
	// the legacy allow-all behaviour (without credentials).
	CorsAllowOrigins = ""
	// AdminIpWhitelist is a comma-separated list of IPs/CIDRs allowed to call
	// the admin API. Empty disables the restriction.
	AdminIpWhitelist = ""
	// APISignatureRequired forces access-token authenticated admin write
	// requests to carry the anti-replay signature (X-Signature/X-Timestamp/
	// X-Nonce). Default false keeps unsigned legacy clients working; requests
	// that DO carry a signature header are always verified.
	APISignatureRequired = false
	// APISignatureWindowSeconds is the max |server time - X-Timestamp| skew
	// accepted for signed admin write requests. Nonces are remembered for the
	// same window. Option updates clamp this into [30, 86400].
	APISignatureWindowSeconds = 300
	// MetricsEnabled serves /metrics when true (v1.1.0 M1 · B08). Disabling it
	// makes the endpoint answer 404 as if it did not exist.
	MetricsEnabled = true
	// MetricsToken, when non-empty, requires `Authorization: Bearer <token>`
	// on /metrics. Empty leaves the endpoint open to whoever can reach it.
	MetricsToken = ""
)

// ParseOriginList splits a raw whitelist into normalised origins. Invalid
// entries are dropped: a typo must never silently widen access.
func ParseOriginList(raw string) []string {
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if part == "*" {
			origins = append(origins, "*")
			continue
		}
		origin, err := NormalizeOrigin(part)
		if err != nil {
			continue
		}
		origins = append(origins, origin)
	}
	return origins
}

// ParseCIDRList parses IPs and CIDRs into networks. A bare IP becomes a /32
// (or /128) host route so operators can mix single addresses and ranges.
func ParseCIDRList(raw string) []*net.IPNet {
	parts := strings.Split(raw, ",")
	nets := make([]*net.IPNet, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.Contains(part, "/") {
			if _, network, err := net.ParseCIDR(part); err == nil {
				nets = append(nets, network)
			}
			continue
		}
		ip := net.ParseIP(part)
		if ip == nil {
			continue
		}
		bits := 128
		if ip.To4() != nil {
			ip = ip.To4()
			bits = 32
		}
		nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
	}
	return nets
}

// IPInNetworks reports whether ip (as reported by the HTTP layer) belongs to
// any of the networks.
func IPInNetworks(ip string, networks []*net.IPNet) bool {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return false
	}
	for _, network := range networks {
		if network.Contains(parsed) {
			return true
		}
	}
	return false
}
