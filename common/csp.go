package common

import (
	"regexp"
	"strings"
)

// SecurityCSPDirective overrides the outgoing Content-Security-Policy. The
// literal token {nonce} is replaced per response with the request nonce, e.g.
//
//	script-src 'self' {nonce} https:
//
// Empty means DefaultCSP. Operators that embed third-party widgets can widen
// the policy here instead of disabling CSP outright.
var SecurityCSPDirective = ""

// DefaultCSP keeps the dashboard usable out of the box: the bundle is
// same-origin, inline theme bootstrap scripts carry a nonce, and images /
// fonts / API calls are allowed over https so avatars and proxied providers
// keep working. object-src/base-uri/frame-ancestors close the classic
// injection niches.
const DefaultCSP = "default-src 'self'; " +
	"script-src 'self' {nonce} https:; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob: https:; " +
	"font-src 'self' data: https:; " +
	"connect-src 'self' https: wss:; " +
	"media-src 'self' data: blob: https:; " +
	"frame-src 'self' https:; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'self'"

// BuildCSP renders the policy for one response. A missing nonce degrades to a
// policy without nonce support rather than emitting a broken directive.
func BuildCSP(nonce string) string {
	policy := strings.TrimSpace(SecurityCSPDirective)
	if policy == "" {
		policy = DefaultCSP
	}
	if nonce == "" {
		return strings.Join(strings.Fields(strings.ReplaceAll(policy, "{nonce}", "")), " ")
	}
	return strings.ReplaceAll(policy, "{nonce}", "'nonce-"+nonce+"'")
}

var (
	scriptOpenTagRe = regexp.MustCompile(`(?i)<script(?:\s[^>]*)?>`)
	styleOpenTagRe  = regexp.MustCompile(`(?i)<style(?:\s[^>]*)?>`)
	nonceAttrRe     = regexp.MustCompile(`(?i)\snonce\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
	srcAttrRe       = regexp.MustCompile(`(?i)\ssrc\s*=`)
)

// InjectCSPNonce stamps the per-response nonce onto every inline <script> and
// <style> block in server-rendered HTML. External scripts (those with src=)
// are governed by the source list in the policy and must not carry a nonce;
// custom-head snippets injected by the operator are covered automatically
// because they pass through here as well.
func InjectCSPNonce(html []byte, nonce string) []byte {
	if nonce == "" || len(html) == 0 {
		return html
	}
	attr := ` nonce="` + nonce + `"`
	out := scriptOpenTagRe.ReplaceAllFunc(html, func(tag []byte) []byte {
		if srcAttrRe.Match(tag) {
			return tag
		}
		return tagWithNonce(tag, attr)
	})
	out = styleOpenTagRe.ReplaceAllFunc(out, func(tag []byte) []byte {
		return tagWithNonce(tag, attr)
	})
	return out
}

// IndexPageHTMLWithNonce returns the cached index page with the nonce applied.
func IndexPageHTMLWithNonce(base []byte, nonce string) []byte {
	return InjectCSPNonce(IndexPageHTML(base), nonce)
}

func tagWithNonce(tag []byte, attr string) []byte {
	if nonceAttrRe.Match(tag) {
		return nonceAttrRe.ReplaceAll(tag, []byte(attr))
	}
	end := len(tag) - 1 // index of '>'
	if end > 0 && tag[end-1] == '/' {
		end--
	}
	out := make([]byte, 0, len(tag)+len(attr))
	out = append(out, tag[:end]...)
	out = append(out, attr...)
	return append(out, tag[end:]...)
}
