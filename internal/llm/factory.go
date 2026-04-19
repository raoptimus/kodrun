/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package llm

import (
	"fmt"
	"time"
)

// ProviderConfig holds the configuration needed to create an LLM client.
type ProviderConfig struct {
	Type    string
	APIKey  string
	BaseURL string
	Timeout time.Duration
}

// ClientFactory is a function that creates an LLM client from a provider config.
type ClientFactory func(cfg ProviderConfig) (Client, error)

var factories = map[string]ClientFactory{}

// RegisterFactory registers a client factory for a given provider type.
func RegisterFactory(providerType string, f ClientFactory) {
	factories[providerType] = f
}

// NewClient creates an LLM client based on the provider config type.
func NewClient(cfg ProviderConfig) (Client, error) {
	t := cfg.Type
	if t == "" {
		t = "ollama"
	}
	f, ok := factories[t]
	if !ok {
		return nil, fmt.Errorf("unknown provider type: %q", t)
	}
	return f(cfg)
}
