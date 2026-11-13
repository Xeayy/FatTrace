//go:build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Xeayy/FatTrace/internal/report"
	"github.com/Xeayy/FatTrace/internal/store"
)

func reportCmd(args []string) {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	fromID := fs.Int64("from", 0, "from snapshot id (default: previous complete)")
	toID := fs.Int64("to", 0, "to snapshot id (default: latest complete)")
	volume := fs.String("volume", "", `volume root prefix, e.g. "D:"`)
	week := fs.Bool("week", false, "warn when the pair is not about 7 days apart")
	topN := fs.Int("top", 20, "rows per ranking list")
	crossVolume := fs.Bool("cross-volume", false, "annotate added files that match other volumes")
	out := fs.String("out", "fattrace-report.html", "output HTML file")
	dbPath := fs.String("db", "", "database file")
	_ = fs.Parse(args)
	if fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: fat report [flags]")
		os.Exit(2)
	}

	path, err := resolveDB(*dbPath)
	if err != nil {
		fail(err)
	}
	st, err := store.OpenReader(path)
	if err != nil {
		fail(err)
	}
	defer st.Close()

	started := time.Now()
	page, err := report.Run(context.Background(), st, report.Options{
		FromID: *fromID, ToID: *toID, RootPrefix: *volume, TopN: *topN,
		CrossVolume: *crossVolume, Week: *week,
	})
	if err != nil {
		fail(err)
	}

	abs, err := filepath.Abs(*out)
	if err != nil {
		abs = *out
	}
	if err := os.WriteFile(abs, page, 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("wrote %s (%s) in %.2fs\n", abs, formatBytes(int64(len(page))), time.Since(started).Seconds())
	fmt.Println("sharing note: the report lists full file paths and sizes; review it before sending it to anyone")
}
