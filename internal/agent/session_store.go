/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package agent

import "time"

// sessionStore owns the on-disk persistence settings for the agent. When dir
// is empty auto-save is disabled. The id is generated lazily by SetDir or
// supplied explicitly by SetID when restoring a previous session.
type sessionStore struct {
	dir string
	id  string
}

// SetDir enables auto-save and stores sessions in dir. Generates a fresh ID
// when none was set, so subsequent saves overwrite the same file.
func (s *sessionStore) SetDir(dir string) {
	s.dir = dir
	if s.id == "" {
		s.id = NewSessionID()
	}
}

// SetID overrides the session ID, used when resuming a saved session.
func (s *sessionStore) SetID(id string) {
	s.id = id
}

// ID returns the current session ID. Empty when no session has been started.
func (s *sessionStore) ID() string {
	return s.id
}

// Dir returns the directory used for session files. Empty when auto-save is
// disabled.
func (s *sessionStore) Dir() string {
	return s.dir
}

// save persists the supplied session under the configured directory and ID.
// Returns nil when auto-save is disabled. Preserves CreatedAt from any
// existing on-disk session, falling back to time.Now for fresh sessions.
func (s *sessionStore) save(session *Session) error {
	if s.dir == "" {
		return nil
	}
	session.ID = s.id
	if existing, err := LoadSession(s.dir, s.id); err == nil {
		session.CreatedAt = existing.CreatedAt
	} else {
		session.CreatedAt = time.Now()
	}
	return SaveSession(s.dir, session)
}
