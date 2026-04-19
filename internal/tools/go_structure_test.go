/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testGoFile = `package example

// UserRole is the role type.
type UserRole string

const (
	// RoleAdmin is admin.
	RoleAdmin UserRole = "admin"
	roleGuest UserRole = "guest"
)

var DefaultTimeout = 30

// User represents a user.
type User struct {
	ID   int
	Name string
	role UserRole
}

// Repo is the repository interface.
type Repo interface {
	FindByID(id int) (*User, error)
	save(u *User) error
}

// NewUser creates a new user.
func NewUser(id int, name string) *User {
	return &User{ID: id, Name: name}
}

func (u *User) GetName() string {
	return u.Name
}

func (u *User) setRole(r UserRole) {
	u.role = r
}
`

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

func TestGoStructureTool_File(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "example.go", testGoFile)

	tool := NewGoStructureTool(dir)
	result, err := tool.Execute(context.Background(), map[string]any{
		"path": "example.go",
	})
	require.NoError(t, err)

	out := result.Output
	assert.Contains(t, out, "package example")
	assert.Contains(t, out, "type UserRole string")
	assert.Contains(t, out, "const RoleAdmin")
	assert.Contains(t, out, "const roleGuest")
	assert.Contains(t, out, "var DefaultTimeout")
	assert.Contains(t, out, "type User struct {")
	assert.Contains(t, out, "ID int")
	assert.Contains(t, out, "role UserRole")
	assert.Contains(t, out, "type Repo interface {")
	assert.Contains(t, out, "FindByID(id int) (*User, error)")
	assert.Contains(t, out, "save(u *User) error")
	assert.Contains(t, out, "func NewUser(id int, name string) *User")
	assert.Contains(t, out, "func (u *User) GetName() string")
	assert.Contains(t, out, "func (u *User) setRole(r UserRole)")
	// no function bodies
	assert.NotContains(t, out, "return &User")
	assert.NotContains(t, out, "return u.Name")
}

func TestGoStructureTool_ExportedOnly(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "example.go", testGoFile)

	tool := NewGoStructureTool(dir)
	result, err := tool.Execute(context.Background(), map[string]any{
		"path":          "example.go",
		"exported_only": true,
	})
	require.NoError(t, err)

	out := result.Output
	assert.Contains(t, out, "type User struct {")
	assert.Contains(t, out, "ID int")
	assert.Contains(t, out, "Name string")
	assert.NotContains(t, out, "role UserRole")
	assert.Contains(t, out, "const RoleAdmin")
	assert.NotContains(t, out, "const roleGuest")
	assert.Contains(t, out, "func NewUser")
	assert.Contains(t, out, "func (u *User) GetName")
	assert.NotContains(t, out, "setRole")
	assert.Contains(t, out, "FindByID")
	assert.NotContains(t, out, "save(u *User)")
}

func TestGoStructureTool_Comments(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "example.go", testGoFile)

	tool := NewGoStructureTool(dir)

	// Without comments
	result, err := tool.Execute(context.Background(), map[string]any{
		"path":     "example.go",
		"comments": false,
	})
	require.NoError(t, err)
	assert.NotContains(t, result.Output, "// UserRole is the role type.")
	assert.NotContains(t, result.Output, "// NewUser creates a new user.")

	// With comments
	result, err = tool.Execute(context.Background(), map[string]any{
		"path":     "example.go",
		"comments": true,
	})
	require.NoError(t, err)
	assert.Contains(t, result.Output, "// UserRole is the role type.")
	assert.Contains(t, result.Output, "// NewUser creates a new user.")
	assert.Contains(t, result.Output, "// User represents a user.")
}

func TestGoStructureTool_Directory(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.go", `package pkg

type Alpha struct{}
`)
	writeTestFile(t, dir, "b.go", `package pkg

func Beta() {}
`)
	// test file should be excluded
	writeTestFile(t, dir, "c_test.go", `package pkg

func TestGamma(t *testing.T) {}
`)

	tool := NewGoStructureTool(dir)
	result, err := tool.Execute(context.Background(), map[string]any{
		"path": ".",
	})
	require.NoError(t, err)

	out := result.Output
	assert.Contains(t, out, "=== a.go ===")
	assert.Contains(t, out, "=== b.go ===")
	assert.NotContains(t, out, "c_test.go")
	assert.Contains(t, out, "type Alpha struct {")
	assert.Contains(t, out, "func Beta()")
}

func TestGoStructureTool_NotFound(t *testing.T) {
	dir := t.TempDir()
	tool := NewGoStructureTool(dir)

	_, err := tool.Execute(context.Background(), map[string]any{
		"path": "nonexistent.go",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path not found")
}

func TestGoStructureTool_LineNumbers(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "example.go", testGoFile)

	tool := NewGoStructureTool(dir)
	result, err := tool.Execute(context.Background(), map[string]any{
		"path": "example.go",
	})
	require.NoError(t, err)

	lines := strings.Split(result.Output, "\n")
	// Every non-empty, non-header line should start with a line number
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "===") {
			continue
		}
		assert.Regexp(t, `^\d+:`, l, "line should start with line number: %q", l)
	}
}

func TestGoStructureTool_EmptyPath(t *testing.T) {
	tool := NewGoStructureTool(t.TempDir())
	_, err := tool.Execute(context.Background(), map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path is required")
}
