package swayipc

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

const (
	magic      = "i3-ipc"
	msgGetTree = 4
)

type Client struct {
	socket string
}

func New(socket string) *Client {
	if socket == "" {
		socket = os.Getenv("SWAYSOCK")
	}
	return &Client{socket: socket}
}

func (c *Client) Available(ctx context.Context) error {
	if c.socket == "" {
		return fmt.Errorf("SWAYSOCK is not set")
	}
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	conn, err := dial(ctx, c.socket)
	if err != nil {
		return err
	}
	return conn.Close()
}

func (c *Client) Focused(ctx context.Context) (FocusedContainer, error) {
	var root Node
	if err := c.requestJSON(ctx, msgGetTree, nil, &root); err != nil {
		return FocusedContainer{}, err
	}
	f, ok := FindFocused(root)
	if !ok {
		return FocusedContainer{}, fmt.Errorf("focused container not found")
	}
	return f, nil
}

func (c *Client) requestJSON(ctx context.Context, typ uint32, payload []byte, out any) error {
	if c.socket == "" {
		return fmt.Errorf("SWAYSOCK is not set")
	}
	conn, err := dial(ctx, c.socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := writeMessage(conn, typ, payload); err != nil {
		return err
	}
	respTyp, resp, err := readMessage(conn)
	if err != nil {
		return err
	}
	if respTyp != typ {
		return fmt.Errorf("unexpected sway response type %d", respTyp)
	}
	return json.Unmarshal(resp, out)
}

func dial(ctx context.Context, socket string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", socket)
}

func writeMessage(w io.Writer, typ uint32, payload []byte) error {
	var buf bytes.Buffer
	buf.WriteString(magic)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(payload)))
	_ = binary.Write(&buf, binary.LittleEndian, typ)
	buf.Write(payload)
	_, err := w.Write(buf.Bytes())
	return err
}

func readMessage(r io.Reader) (uint32, []byte, error) {
	header := make([]byte, 14)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	if string(header[:6]) != magic {
		return 0, nil, fmt.Errorf("invalid sway IPC magic")
	}
	length := binary.LittleEndian.Uint32(header[6:10])
	typ := binary.LittleEndian.Uint32(header[10:14])
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return typ, payload, nil
}
