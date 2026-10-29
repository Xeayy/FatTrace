package logfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestRotateKeepsBoundedFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logs", "maintain.log")
	w, err := Open(path, 12, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"AAAAA", "BBBBB", "CC"} {
		if _, err := w.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	if got := read(t, path); got != "AAAAABBBBBCC" {
		t.Fatalf("current log = %q", got)
	}
	if _, err := w.Write([]byte("D")); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != "D" {
		t.Fatalf("current after rotate = %q", got)
	}
	if got := read(t, path+".1"); got != "AAAAABBBBBCC" {
		t.Fatalf("rotated log = %q", got)
	}

	if _, err := w.Write([]byte("EEEEEEEEEEEEEEE")); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != "EEEEEEEEEEEEEEE" {
		t.Fatalf("second current = %q", got)
	}
	if got := read(t, path+".1"); got != "D" {
		t.Fatalf("second rotated = %q", got)
	}
	if got := read(t, path+".2"); got != "AAAAABBBBBCC" {
		t.Fatalf("oldest rotated = %q", got)
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("keep=2 produced a third backup: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReopenAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "maintain.log")
	w, err := Open(path, 1<<20, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("one\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	w, err = Open(path, 1<<20, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("two\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); !strings.HasSuffix(got, "one\ntwo\n") {
		t.Fatalf("reopened log = %q", got)
	}
}
