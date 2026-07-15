package control

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"waydict/pkg/api"
)

type Handler interface {
	HandleControl(context.Context, Request) Response
}

type peerUIDContextKey struct{}

func PeerUID(ctx context.Context) (int, bool) {
	uid, ok := ctx.Value(peerUIDContextKey{}).(int)
	return uid, ok
}

var ErrSocketPermission = errors.New("socket permission error")

const (
	MaxControlFrameBytes = 512 * 1024
	MaxInjectTextBytes   = 64 * 1024
)

type Server struct {
	socket  string
	handler Handler
}

func NewServer(socket string, handler Handler) *Server {
	return &Server{socket: socket, handler: handler}
}

func (s *Server) Serve(ctx context.Context) error {
	if err := prepareSocketPath(s.socket); err != nil {
		return err
	}
	if err := prepareSocket(s.socket); err != nil {
		return err
	}
	ln, err := net.Listen("unix", s.socket)
	if err != nil {
		return err
	}
	defer ln.Close()
	defer os.Remove(s.socket)
	if err := os.Chmod(s.socket, 0600); err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		uid, peerErr := peerUID(conn)
		if peerErr != nil || uid != os.Geteuid() {
			message := "cannot verify control peer"
			if peerErr == nil {
				message = fmt.Sprintf("control peer uid %d does not match current uid", uid)
			}
			writeResponse(conn, Fail("", "socket_permission", message, zeroStatus()))
			_ = conn.Close()
			continue
		}
		go s.handleConn(context.WithValue(ctx, peerUIDContextKey{}, uid), conn)
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	frame, err := readFrame(conn)
	if err != nil {
		writeResponse(conn, Fail("", "protocol_error", err.Error(), zeroStatus()))
		return
	}
	var req Request
	if err := json.Unmarshal(frame, &req); err != nil {
		writeResponse(conn, Fail("", "protocol_error", err.Error(), zeroStatus()))
		return
	}
	if req.Version != Version {
		writeResponse(conn, Fail(req.ID, "protocol_error", "unsupported protocol version", zeroStatus()))
		return
	}
	writeResponse(conn, s.handler.HandleControl(ctx, req))
}

func Send(ctx context.Context, socket string, req Request) (Response, error) {
	if err := ValidateSocketPath(socket); err != nil {
		return Response{}, err
	}
	if err := checkSocketOwner(socket); err != nil {
		return Response{}, err
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	frame, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	if len(frame) > MaxControlFrameBytes {
		return Response{}, fmt.Errorf("control request exceeds %d bytes", MaxControlFrameBytes)
	}
	frame = append(frame, '\n')
	if _, err := conn.Write(frame); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return Response{}, err
	}
	return resp, nil
}

func prepareSocketPath(socket string) error {
	if err := ValidateSocketPath(socket); err != nil {
		return err
	}
	dir := filepath.Dir(socket)
	st, err := os.Lstat(dir)
	if err == nil {
		if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			return fmt.Errorf("%s is not a directory", dir)
		}
		if err := checkOwnerInfo(dir, st); err != nil {
			return err
		}
		if err := os.Chmod(dir, 0700); err != nil {
			return err
		}
		return checkPrivateMode(dir, 0700)
	}
	if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if st, err := os.Lstat(dir); err != nil {
		return err
	} else if err := checkOwnerInfo(dir, st); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	return checkPrivateMode(dir, 0700)
}

func prepareSocket(socket string) error {
	st, err := os.Lstat(socket)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to remove non-socket path %s", socket)
	}
	if err := checkOwnerInfo(socket, st); err != nil {
		return err
	}
	conn, err := net.DialTimeout("unix", socket, 100*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return fmt.Errorf("control socket already has a listener at %s", socket)
	}
	return os.Remove(socket)
}

func checkSocketOwner(socket string) error {
	dir := filepath.Dir(socket)
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if dirInfo.Mode()&os.ModeSymlink != 0 || !dirInfo.IsDir() {
		return fmt.Errorf("%w: socket directory %s is not a directory", ErrSocketPermission, dir)
	}
	if err := checkOwnerInfo(dir, dirInfo); err != nil {
		return err
	}
	if dirInfo.Mode().Perm() != 0700 {
		return fmt.Errorf("%w: socket directory %s is not owner-only", ErrSocketPermission, dir)
	}
	st, err := os.Lstat(socket)
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 || st.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%w: %s is not a Unix socket", ErrSocketPermission, socket)
	}
	if err := checkOwnerInfo(socket, st); err != nil {
		return err
	}
	if st.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("%w: socket %s is not owner-only", ErrSocketPermission, socket)
	}
	return nil
}

func checkOwnerInfo(path string, st os.FileInfo) error {
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(sys.Uid) != os.Geteuid() {
		return fmt.Errorf("%w: %s is not owned by current user", ErrSocketPermission, path)
	}
	return nil
}

func checkPrivateMode(path string, want os.FileMode) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.Mode().Perm() != want {
		return fmt.Errorf("%w: %s mode is %o, want %o", ErrSocketPermission, path, st.Mode().Perm(), want)
	}
	return nil
}

func ValidateSocketPath(socket string) error {
	return ValidateSocketPathFor(runtime.GOOS, socket)
}

func ValidateSocketPathFor(platform, socket string) error {
	if socket == "" || strings.IndexByte(socket, 0) >= 0 {
		return fmt.Errorf("invalid empty or NUL-containing control socket path")
	}
	limit := 107
	if platform == "darwin" {
		limit = 103
	}
	if len([]byte(socket)) > limit {
		return fmt.Errorf("control socket path is %d bytes; %s limit is %d", len([]byte(socket)), platform, limit)
	}
	return nil
}

func readFrame(r io.Reader) ([]byte, error) {
	reader := bufio.NewReader(io.LimitReader(r, MaxControlFrameBytes+2))
	line, err := reader.ReadBytes('\n')
	if len(line) > MaxControlFrameBytes+1 {
		return nil, fmt.Errorf("control frame exceeds %d bytes", MaxControlFrameBytes)
	}
	if err != nil || len(line) == 0 || line[len(line)-1] != '\n' {
		return nil, fmt.Errorf("control frame is missing its LF terminator")
	}
	frame := line[:len(line)-1]
	if len(frame) > MaxControlFrameBytes {
		return nil, fmt.Errorf("control frame exceeds %d bytes", MaxControlFrameBytes)
	}
	if !utf8.Valid(frame) {
		return nil, fmt.Errorf("control frame is not valid UTF-8")
	}
	return frame, nil
}

func writeResponse(conn net.Conn, resp Response) {
	_ = json.NewEncoder(conn).Encode(resp)
}

func zeroStatus() api.Status {
	return api.Status{State: api.StateError, LastTranscriptRedacted: true}
}
