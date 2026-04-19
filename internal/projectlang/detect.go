/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

// Package projectlang detects the primary programming language of a project
// by looking for well-known marker files in the working directory.
package projectlang

import (
	"os"
	"path/filepath"
)

// Language identifies a programming language ecosystem.
type Language string

const (
	// LangUnknown means no marker file was found yet.
	LangUnknown Language = ""
	// LangGo means the project is a Go module.
	LangGo Language = "go"
	// LangPython means the project is a Python project.
	LangPython Language = "python"
	// LangJSTS means the project is a JavaScript/TypeScript project.
	LangJSTS Language = "jsts"
)

// String returns the string form of the language identifier.
func (l Language) String() string { return string(l) }

// Detector inspects a working directory and returns the primary language.
type Detector struct {
	workDir string
}

// New creates a Detector for the given working directory.
func New(workDir string) *Detector {
	return &Detector{workDir: workDir}
}

// markerRule maps a single marker file to its language.
type markerRule struct {
	file string
	lang Language
}

// orderedMarkers lists marker files in priority order.
// First match wins, so Go beats Python beats JSTS in mixed projects.
var orderedMarkers = []markerRule{
	{file: "go.mod", lang: LangGo},
	{file: "pyproject.toml", lang: LangPython},
	{file: "requirements.txt", lang: LangPython},
	{file: "setup.py", lang: LangPython},
	{file: "Pipfile", lang: LangPython},
	{file: "package.json", lang: LangJSTS},
}

// Detect returns the detected language or LangUnknown when no marker file is present.
// It only checks the immediate working directory; nested manifests are ignored.
func (d *Detector) Detect() Language {
	if d == nil || d.workDir == "" {
		return LangUnknown
	}
	for _, m := range orderedMarkers {
		if fileExists(filepath.Join(d.workDir, m.file)) {
			return m.lang
		}
	}
	return LangUnknown
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
