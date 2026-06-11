package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadPropertiesMissingFile(t *testing.T) {
	m, err := readPropertiesFile("/tmp/does-not-exist-carbonio-preview.properties")
	if err != nil {
		t.Fatalf("missing file should not return error, got: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("missing file: expected empty map, got %v", m)
	}
}

func TestReadPropertiesComments(t *testing.T) {
	content := `# this is a comment
! another comment
carbonio.service.host=1.2.3.4
# inline not parsed as comment
carbonio.service.port=9999
`
	f := writeTemp(t, content)
	m, err := readPropertiesFile(f)
	if err != nil {
		t.Fatalf("readPropertiesFile: %v", err)
	}
	if m["carbonio.service.host"] != "1.2.3.4" {
		t.Errorf("host: got %q, want %q", m["carbonio.service.host"], "1.2.3.4")
	}
	if m["carbonio.service.port"] != "9999" {
		t.Errorf("port: got %q, want %q", m["carbonio.service.port"], "9999")
	}
	if _, ok := m["# this is a comment"]; ok {
		t.Error("comment line parsed as key")
	}
}

func TestReadPropertiesWhitespaceTrimming(t *testing.T) {
	content := "  key1  =  value1  \nkey2=value2\n  key3 = value3\n"
	f := writeTemp(t, content)
	m, err := readPropertiesFile(f)
	if err != nil {
		t.Fatalf("readPropertiesFile: %v", err)
	}
	if m["key1"] != "value1" {
		t.Errorf("key1: got %q, want %q", m["key1"], "value1")
	}
	if m["key2"] != "value2" {
		t.Errorf("key2: got %q, want %q", m["key2"], "value2")
	}
	if m["key3"] != "value3" {
		t.Errorf("key3: got %q, want %q", m["key3"], "value3")
	}
}

func TestReadPropertiesEmptyLines(t *testing.T) {
	content := "\n\nkey=val\n\n"
	f := writeTemp(t, content)
	m, err := readPropertiesFile(f)
	if err != nil {
		t.Fatalf("readPropertiesFile: %v", err)
	}
	if m["key"] != "val" {
		t.Errorf("key: got %q, want %q", m["key"], "val")
	}
	if len(m) != 1 {
		t.Errorf("expected 1 entry, got %d", len(m))
	}
}

func TestReadPropertiesUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.properties")
	if err := os.WriteFile(f, []byte("key=val\n"), 0o000); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if os.Getuid() == 0 {
		t.Skip("running as root — permission test not meaningful")
	}
	_, err := readPropertiesFile(f)
	if err == nil {
		t.Fatal("unreadable file: expected error, got nil")
	}
}

// writeTemp creates a temp file with the given content and returns its path.
func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "config.properties")
	if err := os.WriteFile(f, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return f
}
