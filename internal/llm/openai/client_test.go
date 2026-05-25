/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package openai_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/raoptimus/kodrun/internal/llm"
	llmopenai "github.com/raoptimus/kodrun/internal/llm/openai"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) llm.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return llmopenai.New(srv.URL, "test-api-key", 5*time.Second)
}

// --- Ping ---

func TestClient_Ping_ServerReturns200_Successfully(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	})

	err := c.Ping(context.Background())

	assert.NoError(t, err)
}

func TestClient_Ping_ServerReturnsNon200_Failure(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{
			name:       "404 not found",
			statusCode: http.StatusNotFound,
		},
		{
			name:       "500 internal server error",
			statusCode: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
			})

			err := c.Ping(context.Background())

			assert.ErrorContains(t, err, fmt.Sprintf("openai-compatible server returned status %d", tt.statusCode))
		})
	}
}

func TestClient_Ping_CancelledContext_Failure(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := c.Ping(ctx)

	assert.Error(t, err)
}

func TestClient_Ping_InvalidURL_Failure(t *testing.T) {
	c := llmopenai.New("http://127.0.0.1:1", "key", 1*time.Second)

	err := c.Ping(context.Background())

	assert.ErrorContains(t, err, "openai-compatible server unreachable")
}

// --- Models ---

func TestClient_Models_ReturnsModels_Successfully(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		resp := struct {
			Data []struct {
				ID      string `json:"id"`
				Created int64  `json:"created"`
			} `json:"data"`
		}{
			Data: []struct {
				ID      string `json:"id"`
				Created int64  `json:"created"`
			}{
				{ID: "gpt-4", Created: 1700000000},
				{ID: "gpt-3.5-turbo", Created: 1680000000},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	models, err := c.Models(context.Background())

	require.NoError(t, err)
	require.Len(t, models, 2)
	assert.Equal(t, "gpt-4", models[0].Name)
	assert.Equal(t, "gpt-3.5-turbo", models[1].Name)
}

func TestClient_Models_EmptyList_Successfully(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	})

	models, err := c.Models(context.Background())

	require.NoError(t, err)
	assert.Empty(t, models)
}

func TestClient_Models_ServerReturnsNon200_Failure(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{
			name:       "401 unauthorized",
			statusCode: http.StatusUnauthorized,
		},
		{
			name:       "500 internal server error",
			statusCode: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(`not json`))
			})

			_, err := c.Models(context.Background())

			assert.Error(t, err)
		})
	}
}

func TestClient_Models_InvalidJSON_Failure(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{invalid json`))
	})

	_, err := c.Models(context.Background())

	assert.Error(t, err)
}

// --- Embed ---

func TestClient_Embed_ReturnsEmbeddings_Successfully(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/embeddings", r.URL.Path)
		assert.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))

		resp := struct {
			Model string `json:"model"`
			Data  []struct {
				Index     int       `json:"index"`
				Embedding []float64 `json:"embedding"`
			} `json:"data"`
		}{
			Model: "text-embedding-ada-002",
			Data: []struct {
				Index     int       `json:"index"`
				Embedding []float64 `json:"embedding"`
			}{
				{Index: 0, Embedding: []float64{0.1, 0.2, 0.3}},
				{Index: 1, Embedding: []float64{0.4, 0.5, 0.6}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	result, err := c.Embed(context.Background(), llm.EmbedRequest{
		Model: "text-embedding-ada-002",
		Input: []string{"hello", "world"},
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "text-embedding-ada-002", result.Model)
	require.Len(t, result.Embeddings, 2)
	assert.Equal(t, []float64{0.1, 0.2, 0.3}, result.Embeddings[0])
	assert.Equal(t, []float64{0.4, 0.5, 0.6}, result.Embeddings[1])
}

func TestClient_Embed_EmptyInput_Successfully(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		resp := struct {
			Model string `json:"model"`
			Data  []any  `json:"data"`
		}{
			Model: "text-embedding-ada-002",
			Data:  []any{},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	result, err := c.Embed(context.Background(), llm.EmbedRequest{
		Model: "text-embedding-ada-002",
		Input: []string{},
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.Embeddings)
}

func TestClient_Embed_ServerReturnsNon200_Failure(t *testing.T) {
	tests := []struct {
		name        string
		statusCode  int
		body        string
		errContains string
	}{
		{
			name:        "401 unauthorized",
			statusCode:  http.StatusUnauthorized,
			body:        "unauthorized",
			errContains: "embed error 401",
		},
		{
			name:        "500 internal server error",
			statusCode:  http.StatusInternalServerError,
			body:        "internal server error",
			errContains: "embed error 500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			})

			_, err := c.Embed(context.Background(), llm.EmbedRequest{
				Model: "text-embedding-ada-002",
				Input: []string{"hello"},
			})

			assert.ErrorContains(t, err, tt.errContains)
			assert.ErrorContains(t, err, tt.body)
		})
	}
}

func TestClient_Embed_InvalidJSON_Failure(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not valid json`))
	})

	_, err := c.Embed(context.Background(), llm.EmbedRequest{
		Model: "text-embedding-ada-002",
		Input: []string{"hello"},
	})

	assert.ErrorContains(t, err, "decode embed response")
}
