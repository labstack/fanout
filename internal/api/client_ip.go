package api

import (
	"fmt"
	"net"
	"strings"

	"github.com/labstack/echo/v5"
)

// ConfigureClientIP trusts forwarded client addresses only from explicitly
// configured proxy networks. Direct clients cannot opt themselves into trust.
func ConfigureClientIP(e *echo.Echo, rawCIDRs string) error {
	if strings.TrimSpace(rawCIDRs) == "" {
		return nil
	}
	// Echo trusts loopback, link-local, and every RFC1918 range by default.
	// Disable those broad defaults; configured CIDRs are the complete trust set.
	options := []echo.TrustOption{
		echo.TrustLoopback(false),
		echo.TrustLinkLocal(false),
		echo.TrustPrivateNet(false),
	}
	for _, raw := range strings.Split(rawCIDRs, ",") {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return fmt.Errorf("invalid trusted proxy CIDR %q: %w", value, err)
		}
		options = append(options, echo.TrustIPRange(network))
	}
	e.IPExtractor = echo.ExtractIPFromXFFHeader(options...)
	return nil
}
