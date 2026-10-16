package router

import (
	"archive/zip"
	"bytes"
	"mime/multipart"
	"strings"
	"testing"
)

func TestReadKnowledgeImportsRejectsExpandedZip(t *testing.T) {
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	entry, err := zw.Create("large.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(bytes.Repeat([]byte("a"), (8<<20)+1)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	var upload bytes.Buffer
	mw := multipart.NewWriter(&upload)
	part, err := mw.CreateFormFile("files", "docs.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(archive.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	reader := multipart.NewReader(&upload, mw.Boundary())
	form, err := reader.ReadForm(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	defer form.RemoveAll()
	items, err := readKnowledgeImports(form.File["files"])
	if err == nil || !strings.Contains(err.Error(), "大小限制") || len(items) != 0 {
		t.Fatalf("expected oversized ZIP rejection, got items=%d err=%v", len(items), err)
	}
}
