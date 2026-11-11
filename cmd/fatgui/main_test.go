//go:build windows

package main

import (
	"reflect"
	"testing"
)

func TestBuildTrayArgs(t *testing.T) {
	cases := []struct {
		name  string
		extra []string
		want  []string
	}{
		{"no args opens dashboard", nil, []string{"tray", "--open"}},
		{"db flag keeps open", []string{"--db", `D:\x.db`}, []string{"tray", "--db", `D:\x.db`, "--open"}},
		{"explicit open not duplicated", []string{"--open", "--db", `D:\x.db`}, []string{"tray", "--open", "--db", `D:\x.db`}},
		{"open=false respected", []string{"--open=false"}, []string{"tray", "--open=false"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildTrayArgs(tc.extra)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("buildTrayArgs(%v) = %v, want %v", tc.extra, got, tc.want)
			}
		})
	}
}
