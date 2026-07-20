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
var ErrAlreadyRunning = errors.New("control server already running")

const (
	MaxControlFrameBytes = 512 * 1024
	MaxInjectTextBytes   = 64 * 1024
	// A 20s segment of 16 kHz mono int16 is 640 KiB; the rest is headroom for
	// longer segments and for compressed codecs added later.
	MaxPayloadBytes = 4 * 1024 * 1024
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
		if errors.Is(err, syscall.EADDRINUSE) {
			return fmt.Errorf("%w: bind control socket %s: %v", ErrAlreadyRunning, s.socket, err)
		}
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
	// One reader for both the JSON line and any payload behind it: a second
	// reader would lose whatever the first buffered past the LF.
	reader := bufio.NewReader(conn)
	frame, err := readFrame(reader)
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
	if req.Payload, err = readPayload(reader, req.Args); err != nil {
		writeResponse(conn, Fail(req.ID, "protocol_error", err.Error(), zeroStatus()))
		return
	}
	writeResponse(conn, s.handler.HandleControl(ctx, req))
}

func Send(ctx context.Context, socket string, req Request) (Response, error) {
	return SendWithPayload(ctx, socket, req, nil, 0)
}

// SendWithPayload writes req followed by a length-declared binary body. A
// non-zero dialTimeout bounds connection setup separately from ctx, so an
// unreachable peer is reported in microseconds instead of at the request
// deadline — the difference between a snappy fallback and a stalled decode.
func SendWithPayload(ctx context.Context, socket string, req Request, payload []byte, dialTimeout time.Duration) (Response, error) {
	if err := ValidateSocketPath(socket); err != nil {
		return Response{}, err
	}
	if err := checkSocketOwner(socket); err != nil {
		return Response{}, err
	}
	if len(payload) > 0 {
		if len(payload) > MaxPayloadBytes {
			return Response{}, fmt.Errorf("control payload exceeds %d bytes", MaxPayloadBytes)
		}
		args := make(map[string]any, len(req.Args)+1)
		for key, value := range req.Args {
			args[key] = value
		}
		args["payload_bytes"] = len(payload)
		req.Args = args
	}
	dialCtx := ctx
	if dialTimeout > 0 {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, dialTimeout)
		defer cancel()
	}
	var d net.Dialer
	conn, err := d.DialContext(dialCtx, "unix", socket)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
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
	if len(payload) > 0 {
		if _, err := conn.Write(payload); err != nil {
			return Response{}, err
		}
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
		return fmt.Errorf("%w: control socket already has a listener at %s", ErrAlreadyRunning, socket)
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

// readFrame consumes one LF-terminated JSON line, leaving anything after the LF
// for readPayload. It accumulates through the reader's own buffer rather than an
// io.LimitReader so the cap applies to the line alone, not to the payload too.
func readFrame(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > MaxControlFrameBytes+1 {
			return nil, fmt.Errorf("control frame exceeds %d bytes", MaxControlFrameBytes)
		}
		if err == nil {
			break
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, fmt.Errorf("control frame is missing its LF terminator")
		}
	}
	if len(line) == 0 || line[len(line)-1] != '\n' {
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

// readPayload reads the binary body a request declared through payload_bytes.
// Requests without the arg read nothing, which keeps every existing command
// wire-compatible.
func readPayload(r *bufio.Reader, args map[string]any) ([]byte, error) {
	size, err := PayloadBytes(args)
	if err != nil || size == 0 {
		return nil, err
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, fmt.Errorf("control payload is shorter than its declared %d bytes", size)
	}
	return payload, nil
}

// PayloadBytes reads the declared payload length out of a request's args.
func PayloadBytes(args map[string]any) (int, error) {
	value, ok := args["payload_bytes"]
	if !ok {
		return 0, nil
	}
	var size int64
	switch value := value.(type) {
	case float64:
		size = int64(value)
		if float64(size) != value {
			return 0, fmt.Errorf("payload_bytes must be an integer")
		}
	case int:
		size = int64(value)
	case int64:
		size = value
	default:
		return 0, fmt.Errorf("payload_bytes must be an integer")
	}
	if size < 0 || size > MaxPayloadBytes {
		return 0, fmt.Errorf("payload_bytes must be between 0 and %d", MaxPayloadBytes)
	}
	return int(size), nil
}

func writeResponse(conn net.Conn, resp Response) {
	_ = json.NewEncoder(conn).Encode(resp)
}

func zeroStatus() api.Status {
	return api.Status{State: api.StateError, LastTranscriptRedacted: true}
}
