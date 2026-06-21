package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractTarGzRejectsTraversal(t *testing.T) {
	var buf bytes.Buffer
	writeArchive(t, &buf, []tarEntry{
		{name: "index.html", body: "ok"},
		{name: "../evil.txt", body: "bad"},
	})
	_, err := ExtractTarGz(bytes.NewReader(buf.Bytes()), t.TempDir(), 100)
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("expected traversal error, got %v", err)
	}
}

func TestExtractTarGzRejectsSymlink(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "index.html", Typeflag: tar.TypeReg, Size: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "index.html"}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := ExtractTarGz(bytes.NewReader(buf.Bytes()), t.TempDir(), 100)
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected symlink error, got %v", err)
	}
}

func TestCreateTarGzSingleHTML(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "demo.html")
	if err := os.WriteFile(source, []byte(`<link href="/assets/app.css">`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "artifact.tar.gz")
	warnings, err := CreateTarGz(source, out)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected root-absolute warning, got %d", len(warnings))
	}
	extractDir := filepath.Join(dir, "extract")
	if err := os.Mkdir(extractDir, 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	stats, err := ExtractTarGz(file, extractDir, 100)
	if err != nil {
		t.Fatal(err)
	}
	if stats.FileCount != 1 {
		t.Fatalf("got %d files", stats.FileCount)
	}
	if _, err := os.Stat(filepath.Join(extractDir, "index.html")); err != nil {
		t.Fatal(err)
	}
}

type tarEntry struct {
	name string
	body string
}

func writeArchive(t *testing.T, buf *bytes.Buffer, entries []tarEntry) {
	t.Helper()
	gz := gzip.NewWriter(buf)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: entry.name, Typeflag: tar.TypeReg, Size: int64(len(entry.body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}
