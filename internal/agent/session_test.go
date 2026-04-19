/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/raoptimus/kodrun/internal/llm"
)

func TestSaveSession_CreatesFile_Successfully(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	session := &Session{
		ID:        "20260412-abc123",
		CreatedAt: time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		Model:     "qwen3-coder",
		Mode:      "edit",
		Messages: []llm.Message{
			{Role: "system", Content: "you are an assistant"},
			{Role: "user", Content: "hello"},
		},
		WorkDir: "/tmp/test",
	}

	err := SaveSession(dir, session)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "20260412-abc123.json"))
	require.NoError(t, err)

	var loaded Session
	err = json.Unmarshal(data, &loaded)
	require.NoError(t, err)

	assert.Equal(t, "20260412-abc123", loaded.ID)
	assert.Equal(t, "qwen3-coder", loaded.Model)
	assert.Equal(t, "edit", loaded.Mode)
	assert.Len(t, loaded.Messages, 2)
	assert.Equal(t, "/tmp/test", loaded.WorkDir)
}

func TestSaveSession_CreatesDirectory_Successfully(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "nested", "sessions")
	session := &Session{
		ID:    "20260412-def456",
		Model: "test-model",
	}

	err := SaveSession(dir, session)
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(dir, "20260412-def456.json"))
	assert.NoError(t, err)
}

func TestLoadSession_ReadsFile_Successfully(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	session := &Session{
		ID:        "20260412-aaa111",
		CreatedAt: time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 4, 12, 11, 0, 0, 0, time.UTC),
		Model:     "test-model",
		Mode:      "plan",
		Messages: []llm.Message{
			{Role: "user", Content: "what is Go?"},
		},
		Plan:    "1. Read docs",
		WorkDir: "/project",
	}

	data, err := json.MarshalIndent(session, "", "  ")
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(dir, "20260412-aaa111.json"), data, 0o600)
	require.NoError(t, err)

	loaded, err := LoadSession(dir, "20260412-aaa111")
	require.NoError(t, err)

	assert.Equal(t, "20260412-aaa111", loaded.ID)
	assert.Equal(t, "test-model", loaded.Model)
	assert.Equal(t, "plan", loaded.Mode)
	assert.Len(t, loaded.Messages, 1)
	assert.Equal(t, "1. Read docs", loaded.Plan)
}

func TestLoadSession_FileNotFound_Failure(t *testing.T) {
	t.Parallel()

	_, err := LoadSession(t.TempDir(), "nonexistent")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "read session file")
}

func TestLoadSession_InvalidJSON_Failure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{invalid"), 0o600)
	require.NoError(t, err)

	_, err = LoadSession(dir, "bad")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal session")
}

func TestListSessions_ReturnsSortedByUpdatedAt_Successfully(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	sessions := []*Session{
		{
			ID:        "20260410-oldest",
			UpdatedAt: time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
			Model:     "model-a",
			Mode:      "plan",
			Messages:  []llm.Message{{Role: "user", Content: "old"}},
		},
		{
			ID:        "20260412-newest",
			UpdatedAt: time.Date(2026, 4, 12, 0, 0, 0, 0, time.UTC),
			Model:     "model-b",
			Mode:      "edit",
			Messages:  []llm.Message{{Role: "user", Content: "new1"}, {Role: "assistant", Content: "new2"}},
		},
		{
			ID:        "20260411-middle",
			UpdatedAt: time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC),
			Model:     "model-c",
			Mode:      "plan",
			Messages:  []llm.Message{},
		},
	}

	for _, s := range sessions {
		data, err := json.MarshalIndent(s, "", "  ")
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(dir, s.ID+".json"), data, 0o600)
		require.NoError(t, err)
	}

	summaries, err := ListSessions(dir)
	require.NoError(t, err)
	require.Len(t, summaries, 3)

	assert.Equal(t, "20260412-newest", summaries[0].ID)
	assert.Equal(t, 2, summaries[0].MessageCount)
	assert.Equal(t, "20260411-middle", summaries[1].ID)
	assert.Equal(t, 0, summaries[1].MessageCount)
	assert.Equal(t, "20260410-oldest", summaries[2].ID)
	assert.Equal(t, 1, summaries[2].MessageCount)
}

func TestListSessions_EmptyDirectory_Successfully(t *testing.T) {
	t.Parallel()

	summaries, err := ListSessions(t.TempDir())
	require.NoError(t, err)

	assert.Empty(t, summaries)
}

func TestListSessions_NonexistentDirectory_Successfully(t *testing.T) {
	t.Parallel()

	summaries, err := ListSessions(filepath.Join(t.TempDir(), "no-such-dir"))
	require.NoError(t, err)

	assert.Nil(t, summaries)
}

func TestListSessions_SkipsNonJSONFiles_Successfully(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create a non-JSON file.
	err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("not a session"), 0o600)
	require.NoError(t, err)

	// Create a valid session file.
	s := &Session{
		ID:        "20260412-valid",
		UpdatedAt: time.Date(2026, 4, 12, 0, 0, 0, 0, time.UTC),
		Model:     "m",
	}
	data, err := json.MarshalIndent(s, "", "  ")
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(dir, "20260412-valid.json"), data, 0o600)
	require.NoError(t, err)

	summaries, err := ListSessions(dir)
	require.NoError(t, err)

	assert.Len(t, summaries, 1)
	assert.Equal(t, "20260412-valid", summaries[0].ID)
}

func TestListSessions_SkipsCorruptFiles_Successfully(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create a corrupt JSON file.
	err := os.WriteFile(filepath.Join(dir, "corrupt.json"), []byte("{broken"), 0o600)
	require.NoError(t, err)

	summaries, err := ListSessions(dir)
	require.NoError(t, err)

	assert.Empty(t, summaries)
}

func TestLatestSession_ReturnsNewest_Successfully(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	sessions := []*Session{
		{
			ID:        "20260410-old",
			UpdatedAt: time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
			Model:     "model-old",
		},
		{
			ID:        "20260412-new",
			UpdatedAt: time.Date(2026, 4, 12, 0, 0, 0, 0, time.UTC),
			Model:     "model-new",
		},
	}

	for _, s := range sessions {
		data, err := json.MarshalIndent(s, "", "  ")
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(dir, s.ID+".json"), data, 0o600)
		require.NoError(t, err)
	}

	latest, err := LatestSession(dir)
	require.NoError(t, err)

	assert.Equal(t, "20260412-new", latest.ID)
	assert.Equal(t, "model-new", latest.Model)
}

func TestLatestSession_NoSessions_Failure(t *testing.T) {
	t.Parallel()

	_, err := LatestSession(t.TempDir())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no sessions found")
}

func TestNewSessionID_FormatValid_Successfully(t *testing.T) {
	t.Parallel()

	id := NewSessionID()

	// Should be "YYYYMMDD-XXXXXX" format.
	assert.Len(t, id, 15) // 8 + 1 + 6
	assert.Equal(t, '-', rune(id[8]))
}
