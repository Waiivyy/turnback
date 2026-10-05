package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateFileAcceptsTheFileAnotherCommandJustCreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gitignore")
	// Another turnback command creates the file first, and this one's
	// rename is refused, as Windows refuses it.
	racing := func(p string, data []byte) error {
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return errors.New("rename: Access is denied.")
	}
	if err := createFile(path, []byte(gitignore), racing); err != nil {
		t.Errorf("createFile = %v, want nil since the file was created meanwhile", err)
	}
}

func TestCreateFileReportsAWriteThatCreatedNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gitignore")
	failing := func(string, []byte) error { return errors.New("no space left on device") }
	if err := createFile(path, []byte(gitignore), failing); err == nil {
		t.Error("createFile = nil, want the write's error")
	}
}

func TestCreateFileLeavesAnExistingFileAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gitignore")
	if err := os.WriteFile(path, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unexpected := func(string, []byte) error {
		t.Error("createFile wrote over an existing file")
		return nil
	}
	if err := createFile(path, []byte(gitignore), unexpected); err != nil {
		t.Errorf("createFile = %v", err)
	}
}
