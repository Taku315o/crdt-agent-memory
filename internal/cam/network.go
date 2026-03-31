package cam

import (
	"fmt"
	"net"
)

func ensurePortsAvailable(addresses ...string) error {
	for _, addr := range addresses {
		if addr == "" {
			continue
		}
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("address %s is unavailable: %w", addr, err)
		}
		_ = ln.Close()
	}
	return nil
}
