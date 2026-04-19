/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	id := int64(42)
	req := Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "tools/list",
		Params:  map[string]any{"key": "value"},
	}

	var buf bytes.Buffer
	if err := encode(&buf, req); err != nil {
		t.Fatalf("encode: %v", err)
	}

	// Verify it ends with newline.
	data := buf.Bytes()
	if data[len(data)-1] != '\n' {
		t.Error("expected trailing newline")
	}

	// Verify it's valid JSON.
	var parsed Request
	if err := json.Unmarshal(data[:len(data)-1], &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Method != "tools/list" {
		t.Errorf("method: got %q, want %q", parsed.Method, "tools/list")
	}
	if parsed.ID == nil || *parsed.ID != 42 {
		t.Errorf("id: got %v, want 42", parsed.ID)
	}
}

func TestDecodeResponse(t *testing.T) {
	id := int64(1)
	resp := Response{
		JSONRPC: "2.0",
		ID:      &id,
		Result:  json.RawMessage(`{"tools":[]}`),
	}

	data, _ := json.Marshal(resp)
	data = append(data, '\n')

	reader := bufio.NewReader(bytes.NewReader(data))
	got, err := decode(reader)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID == nil || *got.ID != 1 {
		t.Errorf("id: got %v, want 1", got.ID)
	}
	if got.Error != nil {
		t.Errorf("unexpected error: %v", got.Error)
	}
}

func TestDecodeResponseWithError(t *testing.T) {
	raw := `{"jsonrpc":"2.0","id":5,"error":{"code":-32601,"message":"method not found"}}` + "\n"

	reader := bufio.NewReader(bytes.NewReader([]byte(raw)))
	got, err := decode(reader)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Error == nil {
		t.Fatal("expected error in response")
	}
	if got.Error.Code != -32601 {
		t.Errorf("error code: got %d, want -32601", got.Error.Code)
	}
	if got.Error.Error() != "rpc error -32601: method not found" {
		t.Errorf("error string: got %q", got.Error.Error())
	}
}

func TestEncodeNotification(t *testing.T) {
	req := Request{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}

	var buf bytes.Buffer
	if err := encode(&buf, req); err != nil {
		t.Fatalf("encode: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(buf.Bytes()[:buf.Len()-1], &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Notifications should not have "id" field.
	if _, ok := parsed["id"]; ok {
		t.Error("notification should not have id field")
	}
}
