package service

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOPDSImportFilenameUsesSafeBasename(t *testing.T) {
	header := http.Header{"Content-Disposition": {`attachment; filename*=UTF-8''..%2F%E4%B9%A6.epub`}}
	allow := func(name string) bool { return strings.HasSuffix(name, ".epub") }
	if got := opdsImportFilename(header, "/download", "/book.epub", allow); got != "书.epub" {
		t.Fatalf("filename = %q", got)
	}
	if got := opdsImportFilename(header, "/download", "/book.epub", func(string) bool { return false }); got != "" {
		t.Fatalf("accepted a filename incompatible with the library: %q", got)
	}
}

func TestOPDSImportPublishesOnlyCompleteNewFiles(t *testing.T) {
	root := t.TempDir()
	const pdf = "%PDF-1.6 test publication"
	const name = "book.pdf"
	for _, tc := range []struct {
		name   string
		body   string
		length int64
		limit  int64
	}{
		{"wrong format", "<html>sign in</html>", -1, 200},
		{"truncated", pdf, int64(len(pdf) + 10), 200},
		{"oversized", pdf, -1, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := saveOPDSFile(root, name, strings.NewReader(tc.body), tc.length, tc.limit); err == nil {
				t.Fatal("published an invalid or incomplete download")
			}
			if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
				t.Fatalf("download left files behind: %v, %v", entries, err)
			}
		})
	}
	if err := saveOPDSFile(root, name, strings.NewReader(pdf), int64(len(pdf)), 200); err != nil {
		t.Fatal(err)
	}
	if err := saveOPDSFile(root, name, strings.NewReader(pdf+" replacement"), -1, 200); !errors.Is(err, os.ErrExist) {
		t.Fatalf("same-name import should refuse to overwrite, got: %v", err)
	}
	files, err := os.ReadDir(root)
	if err != nil || len(files) != 1 || files[0].Name() != name {
		t.Fatalf("unexpected saved files: %v, %v", files, err)
	}
	if contents, err := os.ReadFile(filepath.Join(root, name)); err != nil || string(contents) != pdf {
		t.Fatalf("file = %q, err = %v", contents, err)
	}
}
