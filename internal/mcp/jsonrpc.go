package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"

	"github.com/pkg/errors"
)

// Request is a JSON-RPC 2.0 request or notification.
type Request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// encode writes a JSON-RPC message followed by a newline.
func encode(w io.Writer, msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return errors.WithMessage(err, "marshal")
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

// decode reads a single newline-delimited JSON message.
func decode(r *bufio.Reader) (Response, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return Response{}, errors.WithMessage(err, "read")
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return Response{}, errors.WithMessage(err, "unmarshal")
	}
	return resp, nil
}
