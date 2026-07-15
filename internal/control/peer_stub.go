//go:build !linux && !darwin

package control

import (
	"fmt"
	"net"
)

func peerUID(net.Conn) (int, error) {
	return 0, fmt.Errorf("peer credentials are unsupported")
}
