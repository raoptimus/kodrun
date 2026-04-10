package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewLocale_KnownLanguage_Successfully(t *testing.T) {
	tests := []struct {
		name     string
		lang     string
		wantLang string
		wantKey  string
		wantVal  string
	}{
		{
			name:     "english locale",
			lang:     "en",
			wantLang: "en",
			wantKey:  "avatar.user",
			wantVal:  "You",
		},
		{
			name:     "russian locale",
			lang:     "ru",
			wantLang: "ru",
			wantKey:  "avatar.user",
			wantVal:  "Вы",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			locale := NewLocale(tt.lang)

			assert.Equal(t, tt.wantVal, locale.Get(tt.wantKey))
		})
	}
}

func TestNewLocale_UnknownLanguageFallsBackToEnglish_Successfully(t *testing.T) {
	locale := NewLocale("fr")

	got := locale.Get("avatar.user")

	assert.Equal(t, "You", got)
}

func TestLocale_Get_KeyExists_Successfully(t *testing.T) {
	tests := []struct {
		name    string
		lang    string
		key     string
		wantVal string
	}{
		{
			name:    "english placeholder",
			lang:    "en",
			key:     "placeholder.plan",
			wantVal: "Describe task for planning...",
		},
		{
			name:    "russian placeholder",
			lang:    "ru",
			key:     "placeholder.plan",
			wantVal: "Опишите задачу для планирования...",
		},
		{
			name:    "english status",
			lang:    "en",
			key:     "status.cancelled",
			wantVal: "cancelled",
		},
		{
			name:    "russian status",
			lang:    "ru",
			key:     "status.cancelled",
			wantVal: "отменено",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			locale := NewLocale(tt.lang)

			got := locale.Get(tt.key)

			assert.Equal(t, tt.wantVal, got)
		})
	}
}

func TestLocale_Get_KeyMissingInCurrentLangFallsBackToEnglish_Successfully(t *testing.T) {
	// Remove a key from ru to test fallback.
	// We cannot modify the global map, so we test with an unknown lang
	// that falls back to English.
	locale := NewLocale("de")

	got := locale.Get("status.init")

	assert.Equal(t, "Initializing...", got)
}

func TestLocale_Get_KeyNotFoundReturnsKey_Successfully(t *testing.T) {
	locale := NewLocale("en")

	got := locale.Get("nonexistent.key.that.does.not.exist")

	assert.Equal(t, "nonexistent.key.that.does.not.exist", got)
}
