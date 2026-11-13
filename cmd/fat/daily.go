//go:build windows

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Xeayy/FatTrace/internal/daily"
	"github.com/Xeayy/FatTrace/internal/rules"
	"github.com/Xeayy/FatTrace/internal/store"
)

func dailyCmd(args []string) {
	fs := flag.NewFlagSet("daily", flag.ExitOnError)
	dbPath := fs.String("db", "", "database file")
	days := fs.Int("days", 14, "recompute this many recent days (0 = all)")
	top := fs.Int("top", 10, "ranked directories per day")
	asJSON := fs.Bool("json", false, "output JSON")
	_ = fs.Parse(args)

	path, err := resolveDB(*dbPath)
	if err != nil {
		fail(err)
	}
	st, err := store.Open(path)
	if err != nil {
		fail(err)
	}
	defer st.Close()
	set, err := rules.Load(path)
	if err != nil {
		fail(err)
	}

	opts := daily.Options{Days: *days, TopN: *top}
	if !*asJSON {
		opts.Progress = func(day string, from, to int64) {
			fmt.Fprintf(os.Stderr, "daily %s: #%d → #%d\n", day, from, to)
		}
	}
	rep, err := daily.Compute(context.Background(), st, set, opts)
	if err != nil {
		fail(err)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fail(err)
		}
		return
	}
	printDaily(rep)
	for _, w := range rep.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
}

func printDaily(rep daily.Report) {
	if len(rep.Days) == 0 {
		fmt.Println("no complete volume-root snapshot pairs in the selected window")
		return
	}
	fmt.Printf("%-10s  %-8s  %-14s  %-14s  %-9s  %-8s  %s\n",
		"day", "volume", "net delta", "growth", "explained", "files", "root")
	for _, d := range rep.Days {
		s := d.Summary
		explained := "—"
		if s.GrowthBytes > 0 {
			explained = fmt.Sprintf("%.2f%%", s.ExplainedPct)
		}
		fmt.Printf("%-10s  %-8d  %-14s  %-14s  %-9s  %-8d  %s\n",
			s.Day, s.VolumeID, formatBytes(s.DeltaBytes), formatBytes(s.GrowthBytes),
			explained, s.Files, s.Root)
		if len(d.TopDirs) > 0 {
			fmt.Print("  top: ")
			for i, td := range d.TopDirs {
				if i >= 3 {
					break
				}
				if i > 0 {
					fmt.Print(" · ")
				}
				fmt.Printf("%s %s", formatBytes(td.DeltaBytes), td.Path)
			}
			fmt.Println()
		}
	}
}
