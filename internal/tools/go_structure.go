package tools

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/raoptimus/kodrun/internal/llm"
)

// goStructureTool extracts the structural outline of Go source files
// (types, functions, constants, variables) with line numbers — without
// reading full function bodies. Analogous to "go doc" but for any *.go file.
type goStructureTool struct {
	workDir string
}

func (t *goStructureTool) Name() string { return "go_structure" }
func (t *goStructureTool) Description() string {
	return "Show Go file/package structure: types (with fields), interfaces, functions, constants, variables — with line numbers. Like go doc but for any .go file."
}

func (t *goStructureTool) Schema() llm.JSONSchema {
	return llm.JSONSchema{
		Type: "object",
		Properties: map[string]llm.JSONSchema{
			"path":          {Type: "string", Description: "Path to a .go file or directory (package)"},
			"exported_only": {Type: "boolean", Description: "Show only exported symbols (default: false)"},
			"comments":      {Type: "boolean", Description: "Include doc comments (default: false)"},
		},
		Required: []string{"path"},
	}
}

func (t *goStructureTool) Execute(_ context.Context, params map[string]any) (*ToolResult, error) {
	rawPath := stringParam(params, "path")
	if rawPath == "" {
		return nil, &ToolError{Msg: "path is required"}
	}

	exportedOnly := boolParam(params, "exported_only")
	comments := boolParam(params, "comments")

	absPath := rawPath
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(t.workDir, absPath)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("path not found: %s: %w", absPath, err)
	}

	var files []string
	if info.IsDir() {
		entries, err := os.ReadDir(absPath)
		if err != nil {
			return nil, fmt.Errorf("read directory: %w", err)
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
				files = append(files, filepath.Join(absPath, name))
			}
		}
		sort.Strings(files)
	} else {
		if !strings.HasSuffix(absPath, ".go") {
			return nil, &ToolError{Msg: "path must be a .go file or directory"}
		}
		files = []string{absPath}
	}

	if len(files) == 0 {
		return &ToolResult{Output: "no .go files found"}, nil
	}

	var buf strings.Builder
	for i, f := range files {
		if i > 0 {
			buf.WriteByte('\n')
		}

		relPath, relErr := filepath.Rel(t.workDir, f)
		if relErr != nil || relPath == "" || strings.HasPrefix(relPath, "..") {
			relPath = f
		}

		out, err := extractStructure(f, exportedOnly, comments)
		if err != nil {
			fmt.Fprintf(&buf, "=== %s ===\n# error: %s\n", relPath, err)
			continue
		}
		fmt.Fprintf(&buf, "=== %s ===\n%s", relPath, out)
	}

	return &ToolResult{
		Output: buf.String(),
	}, nil
}

// extractStructure parses a single Go file and returns its structural outline.
func extractStructure(filename string, exportedOnly, includeComments bool) (string, error) {
	fset := token.NewFileSet()

	mode := parser.ParseComments
	f, err := parser.ParseFile(fset, filename, nil, mode)
	if err != nil {
		return "", fmt.Errorf("parse error: %w", err)
	}

	var buf strings.Builder

	// package declaration
	line := fset.Position(f.Package).Line
	fmt.Fprintf(&buf, "%4d: package %s\n", line, f.Name.Name)

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			formatGenDecl(&buf, fset, d, exportedOnly, includeComments)
		case *ast.FuncDecl:
			formatFuncDecl(&buf, fset, d, exportedOnly, includeComments)
		}
	}

	return buf.String(), nil
}

// formatGenDecl handles const, var, type declarations.
func formatGenDecl(buf *strings.Builder, fset *token.FileSet, d *ast.GenDecl, exportedOnly, includeComments bool) {
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if exportedOnly && !ast.IsExported(s.Name.Name) {
				continue
			}
			formatTypeSpec(buf, fset, d, s, exportedOnly, includeComments)

		case *ast.ValueSpec:
			formatValueSpec(buf, fset, d, s, exportedOnly, includeComments)
		}
	}
}

// formatTypeSpec formats a type declaration (struct, interface, alias).
func formatTypeSpec(buf *strings.Builder, fset *token.FileSet, gen *ast.GenDecl, s *ast.TypeSpec, exportedOnly, includeComments bool) {
	line := fset.Position(s.Pos()).Line

	if includeComments {
		writeDocComment(buf, fset, docForTypeSpec(gen, s))
	}

	switch st := s.Type.(type) {
	case *ast.StructType:
		fmt.Fprintf(buf, "%4d: type %s struct {\n", line, s.Name.Name)
		if st.Fields != nil {
			for _, field := range st.Fields.List {
				formatStructField(buf, fset, field, exportedOnly, includeComments)
			}
		}
		endLine := fset.Position(st.Fields.Closing).Line
		fmt.Fprintf(buf, "%4d: }\n", endLine)

	case *ast.InterfaceType:
		fmt.Fprintf(buf, "%4d: type %s interface {\n", line, s.Name.Name)
		if st.Methods != nil {
			for _, m := range st.Methods.List {
				formatInterfaceMethod(buf, fset, m, exportedOnly, includeComments)
			}
		}
		endLine := fset.Position(st.Methods.Closing).Line
		fmt.Fprintf(buf, "%4d: }\n", endLine)

	default:
		// type alias or other
		fmt.Fprintf(buf, "%4d: type %s %s\n", line, s.Name.Name, typeString(s.Type))
	}
}

// formatStructField formats a single struct field.
func formatStructField(buf *strings.Builder, fset *token.FileSet, field *ast.Field, exportedOnly, includeComments bool) {
	if len(field.Names) == 0 {
		// embedded field
		name := typeString(field.Type)
		if exportedOnly && !ast.IsExported(name) {
			return
		}
		line := fset.Position(field.Pos()).Line
		if includeComments {
			writeDocComment(buf, fset, field.Doc)
		}
		fmt.Fprintf(buf, "%4d:     %s\n", line, name)
		return
	}

	for _, n := range field.Names {
		if exportedOnly && !ast.IsExported(n.Name) {
			continue
		}
		line := fset.Position(n.Pos()).Line
		if includeComments {
			writeDocComment(buf, fset, field.Doc)
		}
		fmt.Fprintf(buf, "%4d:     %s %s\n", line, n.Name, typeString(field.Type))
	}
}

// formatInterfaceMethod formats a single interface method or embedded type.
func formatInterfaceMethod(buf *strings.Builder, fset *token.FileSet, m *ast.Field, exportedOnly, includeComments bool) {
	line := fset.Position(m.Pos()).Line

	if len(m.Names) == 0 {
		// embedded interface
		name := typeString(m.Type)
		if exportedOnly && !ast.IsExported(name) {
			return
		}
		if includeComments {
			writeDocComment(buf, fset, m.Doc)
		}
		fmt.Fprintf(buf, "%4d:     %s\n", line, name)
		return
	}

	for _, n := range m.Names {
		if exportedOnly && !ast.IsExported(n.Name) {
			continue
		}
		if includeComments {
			writeDocComment(buf, fset, m.Doc)
		}
		ft, ok := m.Type.(*ast.FuncType)
		if !ok {
			fmt.Fprintf(buf, "%4d:     %s\n", line, n.Name)
			continue
		}
		fmt.Fprintf(buf, "%4d:     %s%s\n", line, n.Name, funcSignature(ft))
	}
}

// formatValueSpec formats const/var declarations.
func formatValueSpec(buf *strings.Builder, fset *token.FileSet, gen *ast.GenDecl, s *ast.ValueSpec, exportedOnly, includeComments bool) {
	keyword := "var"
	if gen.Tok == token.CONST {
		keyword = "const"
	}

	for _, n := range s.Names {
		if exportedOnly && !ast.IsExported(n.Name) {
			continue
		}

		line := fset.Position(n.Pos()).Line
		if includeComments {
			writeDocComment(buf, fset, docForValueSpec(gen, s))
		}

		if s.Type != nil {
			fmt.Fprintf(buf, "%4d: %s %s %s\n", line, keyword, n.Name, typeString(s.Type))
		} else {
			fmt.Fprintf(buf, "%4d: %s %s\n", line, keyword, n.Name)
		}
	}
}

// formatFuncDecl formats a function or method declaration (signature only).
func formatFuncDecl(buf *strings.Builder, fset *token.FileSet, d *ast.FuncDecl, exportedOnly, includeComments bool) {
	if exportedOnly && !ast.IsExported(d.Name.Name) {
		return
	}

	line := fset.Position(d.Pos()).Line

	if includeComments {
		writeDocComment(buf, fset, d.Doc)
	}

	var recv string
	if d.Recv != nil && len(d.Recv.List) > 0 {
		r := d.Recv.List[0]
		recvType := typeString(r.Type)
		if len(r.Names) > 0 {
			recv = fmt.Sprintf("(%s %s) ", r.Names[0].Name, recvType)
		} else {
			recv = fmt.Sprintf("(%s) ", recvType)
		}
	}

	fmt.Fprintf(buf, "%4d: func %s%s%s\n", line, recv, d.Name.Name, funcSignature(d.Type))
}

// funcSignature returns the parameter and result signature of a function type.
func funcSignature(ft *ast.FuncType) string {
	var buf strings.Builder

	buf.WriteByte('(')
	if ft.Params != nil {
		buf.WriteString(fieldListString(ft.Params))
	}
	buf.WriteByte(')')

	if ft.Results != nil && len(ft.Results.List) > 0 {
		results := fieldListString(ft.Results)
		if len(ft.Results.List) == 1 && len(ft.Results.List[0].Names) == 0 {
			buf.WriteByte(' ')
			buf.WriteString(results)
		} else {
			buf.WriteString(" (")
			buf.WriteString(results)
			buf.WriteByte(')')
		}
	}

	return buf.String()
}

// fieldListString converts a field list to a string representation.
func fieldListString(fl *ast.FieldList) string {
	var parts []string
	for _, f := range fl.List {
		ts := typeString(f.Type)
		if len(f.Names) == 0 {
			parts = append(parts, ts)
		} else {
			names := make([]string, len(f.Names))
			for i, n := range f.Names {
				names[i] = n.Name
			}
			parts = append(parts, strings.Join(names, ", ")+" "+ts)
		}
	}
	return strings.Join(parts, ", ")
}

// typeString returns a string representation of a type expression.
func typeString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return typeString(t.X) + "." + t.Sel.Name
	case *ast.StarExpr:
		return "*" + typeString(t.X)
	case *ast.ArrayType:
		if t.Len == nil {
			return "[]" + typeString(t.Elt)
		}
		return "[...]" + typeString(t.Elt)
	case *ast.MapType:
		return "map[" + typeString(t.Key) + "]" + typeString(t.Value)
	case *ast.InterfaceType:
		if t.Methods == nil || len(t.Methods.List) == 0 {
			return "interface{}"
		}
		return "interface{ ... }"
	case *ast.FuncType:
		return "func" + funcSignature(t)
	case *ast.ChanType:
		switch t.Dir {
		case ast.SEND:
			return "chan<- " + typeString(t.Value)
		case ast.RECV:
			return "<-chan " + typeString(t.Value)
		default:
			return "chan " + typeString(t.Value)
		}
	case *ast.Ellipsis:
		return "..." + typeString(t.Elt)
	case *ast.ParenExpr:
		return "(" + typeString(t.X) + ")"
	case *ast.StructType:
		return "struct{ ... }"
	case *ast.IndexExpr:
		return typeString(t.X) + "[" + typeString(t.Index) + "]"
	case *ast.IndexListExpr:
		indices := make([]string, len(t.Indices))
		for i, idx := range t.Indices {
			indices[i] = typeString(idx)
		}
		return typeString(t.X) + "[" + strings.Join(indices, ", ") + "]"
	default:
		return "?"
	}
}

// writeDocComment writes a doc comment block with line numbers.
func writeDocComment(buf *strings.Builder, fset *token.FileSet, cg *ast.CommentGroup) {
	if cg == nil {
		return
	}
	for _, c := range cg.List {
		line := fset.Position(c.Pos()).Line
		fmt.Fprintf(buf, "%4d: %s\n", line, c.Text)
	}
}

// docForTypeSpec returns the doc comment for a type spec, falling back to
// the GenDecl doc if the spec has no doc of its own.
func docForTypeSpec(gen *ast.GenDecl, s *ast.TypeSpec) *ast.CommentGroup {
	if s.Doc != nil {
		return s.Doc
	}
	if len(gen.Specs) == 1 {
		return gen.Doc
	}
	return nil
}

// docForValueSpec returns the doc comment for a value spec, falling back to
// the GenDecl doc if the spec has no doc of its own.
func docForValueSpec(gen *ast.GenDecl, s *ast.ValueSpec) *ast.CommentGroup {
	if s.Doc != nil {
		return s.Doc
	}
	if len(gen.Specs) == 1 {
		return gen.Doc
	}
	return nil
}

// CachePolicy implements the Cacheable interface. go_structure parses
// Go source files, so results are keyed by path and invalidated by
// file-modifying tools.
func (t *goStructureTool) CachePolicy() CachePolicy {
	return CachePolicy{
		Cacheable:    true,
		PathParams:   []string{"path"},
		Invalidators: []string{"write_file", "edit_file", "delete_file", "move_file"},
	}
}

// ResolvePaths implements PathResolver. It resolves the path parameter
// to an absolute filesystem path for mtime-based cache invalidation.
func (t *goStructureTool) ResolvePaths(params map[string]any) []string {
	p := stringParam(params, "path")
	if p == "" {
		return nil
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(t.workDir, p)
	}
	return []string{p}
}

// NewGoStructureTool creates a go_structure tool.
func NewGoStructureTool(workDir string) *goStructureTool {
	return &goStructureTool{workDir: workDir}
}
