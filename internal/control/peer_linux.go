//go:build linux

package control

import (
	"fmt"
	"net"
	"syscall"
)

func peerUID(conn net.Conn) (int, error) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("control connection is not Unix")
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return 0, err
	}
	uid := 0
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		cred, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		if err != nil {
			socketErr = err
			return
		}
		uid = int(cred.Uid)
	}); err != nil {
		return 0, err
	}
	return uid, socketErr
}
