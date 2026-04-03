package projectlang

import "sync"

// State holds the currently detected language with lazy re-detection support.
// It is safe for concurrent use.
type State struct {
	mu       sync.RWMutex
	detector *Detector
	override Language
	current  Language
}

// NewState creates a new State backed by the given Detector.
// If override is not LangUnknown, it short-circuits detection and the
// override is always returned.
func NewState(detector *Detector, override Language) *State {
	return &State{detector: detector, override: override, current: override}
}

// Current returns the last known language without running detection.
func (s *State) Current() Language {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// EnsureDetected returns the current language and re-runs detection
// if it is still LangUnknown. The changed flag is true when this call
// transitioned the language from LangUnknown to a concrete value.
func (s *State) EnsureDetected() (lang Language, changed bool) {
	s.mu.RLock()
	if s.current != LangUnknown || s.override != LangUnknown {
		c := s.current
		s.mu.RUnlock()
		return c, false
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	// Re-check under write lock.
	if s.current != LangUnknown {
		return s.current, false
	}
	if s.detector == nil {
		return LangUnknown, false
	}
	detected := s.detector.Detect()
	if detected == LangUnknown {
		return LangUnknown, false
	}
	s.current = detected
	return detected, true
}
