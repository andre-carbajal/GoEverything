package app

import (
	"bytes"
	"strings"
	"testing"

	"goeverything/internal/db"
)

func TestWriteTable(t *testing.T) {
	t.Parallel()

	in := []db.Entry{
		{Name: "a.txt", Path: "/tmp/a.txt", Size: 10, Ext: "txt", IsDir: false},
		{Name: "docs", Path: "/tmp/docs", Size: 0, IsDir: true},
	}

	var out bytes.Buffer
	writeTable(&out, in)
	s := out.String()
	if !strings.Contains(s, "NAME") || !strings.Contains(s, "a.txt") || !strings.Contains(s, "docs") {
		t.Fatalf("unexpected table output: %q", s)
	}
}

func TestWriteJSON(t *testing.T) {
	t.Parallel()

	in := []db.Entry{
		{Name: "a.txt", Path: "/tmp/a.txt", Size: 10, Ext: "txt"},
	}

	var out bytes.Buffer
	if err := writeJSON(&out, in); err != nil {
		t.Fatalf("write json: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "\"Name\"") || !strings.Contains(s, "\"a.txt\"") {
		t.Fatalf("unexpected json output: %q", s)
	}
}
