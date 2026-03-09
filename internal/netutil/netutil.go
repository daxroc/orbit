package netutil

import "net"

// StripPort returns the host portion of a host:port address.
// If addr does not contain a port, the original string is returned unchanged.
func StripPort(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}
