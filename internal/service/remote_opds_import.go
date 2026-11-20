package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const remoteOPDSFileLimit = 512 << 20

// ImportRemoteOPDS downloads a publication into a server-selected library root.
func ImportRemoteOPDS(ctx context.Context, address, username, password, root string, allowed func(string) bool) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	resp, err := requestRemoteOPDS(ctx, address, username, password, "*/*")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.ContentLength > remoteOPDSFileLimit {
		return "", errors.New("publication exceeds 512 MiB")
	}
	original, _ := url.Parse(address) // Already validated by requestRemoteOPDS.
	name := opdsImportFilename(resp.Header, resp.Request.URL.Path, original.Path, allowed)
	if name == "" {
		return "", errors.New("no supported filename for the selected library type")
	}
	if err := saveOPDSFile(root, name, resp.Body, resp.ContentLength, remoteOPDSFileLimit); err != nil {
		return "", err
	}
	return name, nil
}

func opdsImportFilename(header http.Header, finalPath, originalPath string, allowed func(string) bool) string {
	_, params, _ := mime.ParseMediaType(header.Get("Content-Disposition"))
	for _, candidate := range []string{params["filename"], finalPath, originalPath} {
		name := path.Base(strings.ReplaceAll(strings.TrimSpace(candidate), `\`, "/"))
		if !filepath.IsLocal(name) || strings.HasPrefix(name, ".") || strings.ContainsAny(name, `<>:"|?*`) ||
			strings.IndexFunc(name, unicode.IsControl) >= 0 || !allowed(name) {
			continue
		}
		return name
	}
	return ""
}

func saveOPDSFile(root, name string, body io.Reader, length, limit int64) error {
	dest := filepath.Join(root, name)
	if _, err := os.Lstat(dest); err == nil {
		return os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("could not check the destination file")
	}
	source := bufio.NewReader(body)
	header, _ := source.Peek(1024)
	if !validOPDSPublication(name, header) {
		return errors.New("upstream response does not match the publication format")
	}
	tmp, err := os.CreateTemp(root, ".nowen-opds-*.part")
	if err != nil {
		return errors.New("could not write to the selected library folder")
	}
	defer os.Remove(tmp.Name())
	n, copyErr := io.Copy(tmp, io.LimitReader(source, limit+1))
	closeErr := tmp.Close()
	if n > limit {
		return errors.New("publication exceeds 512 MiB")
	}
	if copyErr != nil || closeErr != nil || (length >= 0 && n != length) {
		return errors.New("publication download did not complete")
	}
	// Publishing by link is atomic and refuses to replace a file created concurrently.
	if err := os.Link(tmp.Name(), dest); errors.Is(err, os.ErrExist) {
		return os.ErrExist
	} else if err != nil {
		return errors.New("could not publish file: the library folder must support hard links")
	}
	return nil
}

func validOPDSPublication(name string, header []byte) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".cbz", ".zip", ".epub":
		return bytes.HasPrefix(header, []byte("PK\x03\x04"))
	case ".cbr", ".rar":
		return bytes.HasPrefix(header, []byte("Rar!\x1a\x07"))
	case ".cb7", ".7z":
		return bytes.HasPrefix(header, []byte("7z\xbc\xaf\x27\x1c"))
	case ".pdf":
		return bytes.Contains(header, []byte("%PDF-"))
	case ".mobi", ".azw3":
		return len(header) >= 68 && bytes.Equal(header[60:68], []byte("BOOKMOBI"))
	case ".txt":
		return len(header) > 0 && strings.HasPrefix(http.DetectContentType(header), "text/plain")
	case ".html", ".htm":
		return strings.HasPrefix(http.DetectContentType(header), "text/html")
	default:
		return false
	}
}
