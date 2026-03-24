package ollama

import (
	"testing"
)

func TestParseToolCalls_Single(t *testing.T) {
	input := `{"name": "read_file", "arguments": {"path": "main.go"}}`

	calls, ok := ParseToolCalls(input)
	if !ok {
		t.Fatal("expected successful parse")
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Function.Name != "read_file" {
		t.Errorf("name = %q, want %q", calls[0].Function.Name, "read_file")
	}
}

func TestParseToolCalls_Array(t *testing.T) {
	input := `[{"name": "read_file", "arguments": {"path": "a.go"}}, {"name": "write_file", "arguments": {"path": "b.go", "content": "test"}}]`

	calls, ok := ParseToolCalls(input)
	if !ok {
		t.Fatal("expected successful parse")
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
}

func TestParseToolCalls_NotJSON(t *testing.T) {
	_, ok := ParseToolCalls("This is just a text response")
	if ok {
		t.Error("expected parse to fail for plain text")
	}
}

func TestParseToolCalls_EmptyName(t *testing.T) {
	input := `{"name": "", "arguments": {}}`
	_, ok := ParseToolCalls(input)
	if ok {
		t.Error("expected parse to fail for empty name")
	}
}

func TestParseToolCalls_XMLFormat(t *testing.T) {
	input := `<function=write_file>
<parameter=path>
hello.txt
</parameter>
<parameter=content>
Hello from KodRun!
</parameter>
</function>`

	calls, ok := ParseToolCalls(input)
	if !ok {
		t.Fatal("expected successful parse of XML format")
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Function.Name != "write_file" {
		t.Errorf("name = %q, want %q", calls[0].Function.Name, "write_file")
	}
	path, _ := calls[0].Function.Arguments["path"].(string)
	if path != "hello.txt" {
		t.Errorf("path = %q, want %q", path, "hello.txt")
	}
	content, _ := calls[0].Function.Arguments["content"].(string)
	if content != "Hello from KodRun!" {
		t.Errorf("content = %q, want %q", content, "Hello from KodRun!")
	}
}

func TestParseToolCalls_XMLMultiple(t *testing.T) {
	input := `<function=read_file>
<parameter=path>a.go</parameter>
</function>
<function=read_file>
<parameter=path>b.go</parameter>
</function>`

	calls, ok := ParseToolCalls(input)
	if !ok {
		t.Fatal("expected successful parse")
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
}
