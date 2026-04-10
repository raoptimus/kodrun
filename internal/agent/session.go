package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/raoptimus/kodrun/internal/ollama"
)

const sessionFilePermission = 0o600

// Session represents a saved conversation session.
type Session struct {
	ID        string           `json:"id"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
	Model     string           `json:"model"`
	Mode      string           `json:"mode"`
	Messages  []ollama.Message `json:"messages"`
	Plan      string           `json:"plan,omitempty"`
	Stats     SessionStats     `json:"stats"`
	WorkDir   string           `json:"work_dir"`
}

// SessionSummary is a lightweight view of a session for listing.
type SessionSummary struct {
	ID           string    `json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Model        string    `json:"model"`
	Mode         string    `json:"mode"`
	MessageCount int       `json:"message_count"`
}

const (
	sessionIDRandomBytes = 3 // bytes of randomness in session ID
	sessionDirPermission = 0o755
)

// NewSessionID generates a short unique ID like "20260404-abc123".
func NewSessionID() string {
	ts := time.Now().Format("20060102")
	b := make([]byte, sessionIDRandomBytes)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based suffix if crypto/rand fails.
		return ts + "-" + time.Now().Format("150405")
	}
	return ts + "-" + hex.EncodeToString(b)
}

// SaveSession writes the session to sessionsDir/{id}.json.
func SaveSession(sessionsDir string, session *Session) error {
	if err := os.MkdirAll(sessionsDir, sessionDirPermission); err != nil {
		return errors.WithMessage(err, "create sessions directory")
	}

	session.UpdatedAt = time.Now()

	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return errors.WithMessage(err, "marshal session")
	}

	path := filepath.Join(sessionsDir, session.ID+".json")
	if err := os.WriteFile(path, data, sessionFilePermission); err != nil {
		return errors.WithMessage(err, "write session file")
	}

	return nil
}

// LoadSession reads a session by ID from sessionsDir.
func LoadSession(sessionsDir, id string) (*Session, error) {
	path := filepath.Join(sessionsDir, id+".json")

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.WithMessage(err, "read session file")
	}

	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, errors.WithMessage(err, "unmarshal session")
	}

	return &s, nil
}

// ListSessions returns summaries of all saved sessions, sorted by UpdatedAt descending.
func ListSessions(sessionsDir string) ([]*SessionSummary, error) {
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, errors.WithMessage(err, "read sessions directory")
	}

	summaries := make([]*SessionSummary, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		id := strings.TrimSuffix(entry.Name(), ".json")
		s, err := LoadSession(sessionsDir, id)
		if err != nil {
			continue // skip corrupt files
		}

		summaries = append(summaries, &SessionSummary{
			ID:           s.ID,
			CreatedAt:    s.CreatedAt,
			UpdatedAt:    s.UpdatedAt,
			Model:        s.Model,
			Mode:         s.Mode,
			MessageCount: len(s.Messages),
		})
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].UpdatedAt.After(summaries[j].UpdatedAt)
	})

	return summaries, nil
}

// LatestSession loads the most recently updated session from sessionsDir.
func LatestSession(sessionsDir string) (*Session, error) {
	summaries, err := ListSessions(sessionsDir)
	if err != nil {
		return nil, err
	}
	if len(summaries) == 0 {
		return nil, errors.New("no sessions found")
	}

	return LoadSession(sessionsDir, summaries[0].ID)
}
