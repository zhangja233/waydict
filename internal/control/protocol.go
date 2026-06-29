package control

import (
	"crypto/rand"
	"encoding/hex"

	"waydict/pkg/api"
)

const Version = 1

type Request struct {
	Version int            `json:"version"`
	ID      string         `json:"id"`
	Command string         `json:"command"`
	Args    map[string]any `json:"args"`
}

type Response struct {
	Version int            `json:"version"`
	ID      string         `json:"id"`
	OK      bool           `json:"ok"`
	Error   *api.ErrorInfo `json:"error"`
	Status  api.Status     `json:"status"`
}

func NewRequest(command string, args map[string]any) Request {
	return Request{
		Version: Version,
		ID:      randomID(),
		Command: command,
		Args:    args,
	}
}

func OK(id string, status api.Status) Response {
	return Response{Version: Version, ID: id, OK: true, Status: status}
}

func Fail(id, code, msg string, status api.Status) Response {
	return Response{
		Version: Version,
		ID:      id,
		OK:      false,
		Error:   &api.ErrorInfo{Code: code, Message: msg},
		Status:  status,
	}
}

func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "request"
	}
	return hex.EncodeToString(b[:])
}
