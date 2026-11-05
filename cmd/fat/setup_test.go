//go:build windows

package main

import (
	"testing"

	"fattrace/internal/store"
)

func TestSplitRoots(t *testing.T) {
	got := splitRoots(` D:\ , , E:\data\ , `)
	want := []string{`D:\`, `E:\data\`}
	if len(got) != len(want) {
		t.Fatalf("splitRoots = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitRoots[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if roots := splitRoots("  "); len(roots) != 0 {
		t.Fatalf("splitRoots spaces = %v, want empty", roots)
	}
}

func TestSameRoots(t *testing.T) {
	if !sameRoots([]string{`D:\`}, []string{`d:\`}) {
		t.Fatal("sameRoots should compare case-insensitively")
	}
	if !sameRoots([]string{`D:\data\`}, []string{`D:\data`}) {
		t.Fatal("sameRoots should compare cleaned paths")
	}
	if sameRoots([]string{`D:\`}, []string{`D:\`, `E:\`}) {
		t.Fatal("sameRoots accepted different lengths")
	}
	if sameRoots([]string{`D:\`}, []string{`E:\`}) {
		t.Fatal("sameRoots accepted different roots")
	}
}

func TestIsVolumeRoot(t *testing.T) {
	for _, root := range []string{`D:\`, `d:\`, `D:/`} {
		if !isVolumeRoot(root) {
			t.Errorf("isVolumeRoot(%q) = false, want true", root)
		}
	}
	for _, root := range []string{`D:\data`, `D:\data\`, `D:`, ``} {
		if isVolumeRoot(root) {
			t.Errorf("isVolumeRoot(%q) = true, want false", root)
		}
	}
}

func TestTableRows(t *testing.T) {
	tables := []store.TableCount{{Name: "snapshots", Rows: 15}, {Name: "volumes", Rows: 2}}
	if got := tableRows(tables, "snapshots"); got != 15 {
		t.Fatalf("tableRows(snapshots) = %d, want 15", got)
	}
	if got := tableRows(tables, "missing"); got != 0 {
		t.Fatalf("tableRows(missing) = %d, want 0", got)
	}
}

func TestOpenBrowserRejectsUnsafeURLs(t *testing.T) {
	cases := []string{
		"file:///C:/Windows/System32/calc.exe",
		"http://example.com/",
		"https://127.0.0.1.evil.test/",
		"javascript:alert(1)",
	}
	for _, raw := range cases {
		if err := openBrowser(raw); err == nil {
			t.Errorf("openBrowser(%q) accepted an unsafe URL", raw)
		}
	}
}
