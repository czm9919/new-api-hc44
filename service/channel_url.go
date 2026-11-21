package service

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// metadataHosts are the well-known credentials endpoints reachable from inside
// a cloud instance. They are never a legitimate model upstream.
var metadataHosts = map[string]struct{}{
	"metadata.google.internal": {},
	"metadata.goog":            {},
}

// ValidateChannelBaseURL is the static SSRF guard for channel upstream URLs
// (version spec B76). It deliberately does NOT resolve DNS: a gateway legitimately
// points at loopback sidecars (ollama on 127.0.0.1) and private LAN vLLM
// clusters, so the check only rejects the targets that can never be a model
// upstream — the cloud metadata range, the unspecified address and metadata
// hostnames — plus malformed URLs and embedded credentials.
//
// Deeper filtering for platform-initiated fetches lives in
// common.ValidateURLWithFetchSetting; that policy is about what the server
// downloads, not where operators may route traffic.
func ValidateChannelBaseURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("base URL is not a valid URL: %v", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("base URL must use http or https")
	}
	if parsed.Host == "" {
		return fmt.Errorf("base URL must contain a host")
	}
	if parsed.User != nil {
		return fmt.Errorf("base URL must not embed credentials")
	}

	host := strings.ToLower(parsed.Hostname())
	if _, blocked := metadataHosts[host]; blocked {
		return fmt.Errorf("base URL points at a cloud metadata endpoint")
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsUnspecified() {
			return fmt.Errorf("base URL must not point at the unspecified address")
		}
		// Link-local: 169.254.0.0/16 on IPv4 (cloud metadata) and fe80::/10.
		if ip.To4() != nil {
			if ip.To4()[0] == 169 && ip.To4()[1] == 254 {
				return fmt.Errorf("base URL points at a link-local/metadata address")
			}
		} else if ip.IsLinkLocalUnicast() {
			return fmt.Errorf("base URL points at a link-local address")
		}
	}
	return nil
}
