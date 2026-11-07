package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// newRequestID returns a 128-bit random identifier.
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is unrecoverable; fall back to a constant so the
		// service keeps answering (IDs become non-unique but requests work).
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(b[:])
}

// ClientIP resolves the client address, honouring X-Forwarded-For only when the
// direct peer is a trusted proxy.
//
// trusted contains CIDR blocks (e.g. "10.0.0.0/8") of proxies in front of the
// API. Without a match the header is ignored, which prevents clients from
// spoofing their address to bypass rate limits.
func ClientIP(r *http.Request, trusted []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(strings.TrimSpace(host))
	if peer == nil {
		return host
	}
	if !isTrusted(peer, trusted) {
		return peer.String()
	}

	// Walk the chain from the closest hop outwards and return the first
	// untrusted address.
	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded == "" {
		return peer.String()
	}
	parts := strings.Split(forwarded, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		ip := net.ParseIP(candidate)
		if ip == nil {
			continue
		}
		if !isTrusted(ip, trusted) {
			return ip.String()
		}
	}
	return peer.String()
}

func isTrusted(ip net.IP, trusted []*net.IPNet) bool {
	for _, network := range trusted {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// ParseTrustedProxies converts CIDR strings into networks. A bare IP is
// treated as a single-host network.
func ParseTrustedProxies(values []string) ([]*net.IPNet, error) {
	out := make([]*net.IPNet, 0, len(values))
	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !strings.Contains(raw, "/") {
			ip := net.ParseIP(raw)
			if ip == nil {
				return nil, fmt.Errorf("invalid trusted proxy %q", raw)
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, network, err := net.ParseCIDR(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy CIDR %q: %w", raw, err)
		}
		out = append(out, network)
	}
	return out, nil
}

// ipResolver is a small helper so middleware can resolve client IPs without
// threading the trusted list through every call site.
type ipResolver struct {
	trusted []*net.IPNet
}

func (i ipResolver) clientIP(r *http.Request) string { return ClientIP(r, i.trusted) }
