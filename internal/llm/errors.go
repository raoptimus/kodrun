/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package llm

import (
	"encoding/json"
	"net"
	"strings"

	"github.com/pkg/errors"
)

// DetectErrorJSON returns a non-empty message when content is an error envelope
// like {"error":{"type":"...","message":"..."}} or {"error":"..."}.
// Returns "" for normal content.
func DetectErrorJSON(content string) string {
	s := strings.TrimSpace(content)
	if len(s) < 2 || s[0] != '{' {
		return ""
	}
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(s), &envelope); err != nil || len(envelope.Error) == 0 {
		return ""
	}
	var asStr string
	if err := json.Unmarshal(envelope.Error, &asStr); err == nil && asStr != "" {
		return asStr
	}
	var asObj struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(envelope.Error, &asObj); err == nil {
		if asObj.Message != "" {
			return asObj.Message
		}
		if asObj.Type != "" {
			return asObj.Type
		}
	}
	return ""
}

// IsDialError returns true when the error is a TCP dial failure (connection
// refused, DNS resolution error, etc.). These indicate that the server is down
// and retrying immediately is pointless.
func IsDialError(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return true
	}
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr)
}
