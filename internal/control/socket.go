package control

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"

	"sway-voice/pkg/api"
)

type Handler interface {
	HandleControl(context.Context, Request) Response
}

var ErrSocketPermission = errors.New("socket permission error")

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
	_ = os.Remove(s.socket)
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
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	line, err := r.ReadBytes('\n')
	if err != nil {
		writeResponse(conn, Fail("", "protocol_error", err.Error(), zeroStatus()))
		return
	}
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
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
	if err := checkSocketOwner(socket); err != nil {
		return Response{}, err
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return Response{}, err
	}
	return resp, nil
}

func prepareSocketPath(socket string) error {
	dir := filepath.Dir(socket)
	st, err := os.Stat(dir)
	if err == nil {
		if !st.IsDir() {
			return fmt.Errorf("%s is not a directory", dir)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.Chmod(dir, 0700)
}

func checkSocketOwner(socket string) error {
	st, err := os.Stat(socket)
	if err != nil {
		return err
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(sys.Uid) != os.Getuid() {
		return fmt.Errorf("%w: socket %s is not owned by current user", ErrSocketPermission, socket)
	}
	return nil
}

func writeResponse(conn net.Conn, resp Response) {
	_ = json.NewEncoder(conn).Encode(resp)
}

func zeroStatus() api.Status {
	return api.Status{State: api.StateError, LastTranscriptRedacted: true}
}
