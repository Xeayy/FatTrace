//go:build windows

package main

import "testing"

func TestFormatBytesSigns(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1536, "1.50 KiB"},
		{-1536, "-1.50 KiB"},
		{-143027552864, "-133.20 GiB"},
	}
	for _, tc := range cases {
		if got := formatBytes(tc.in); got != tc.want {
			t.Fatalf("formatBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := signedBytes(1536); got != "+1.50 KiB" {
		t.Fatalf("signedBytes(1536) = %q", got)
	}
	if got := signedBytes(-1536); got != "-1.50 KiB" {
		t.Fatalf("signedBytes(-1536) = %q", got)
	}
}
