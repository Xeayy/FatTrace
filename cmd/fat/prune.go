//go:build windows

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"fattrace/internal/config"
	"fattrace/internal/prune"
	"fattrace/internal/store"
)

func pruneCmd(args []string) {
	fs := flag.NewFlagSet("prune", flag.ExitOnError)
	dbPath := fs.String("db", "", "database file")
	apply := fs.Bool("apply", false, "execute deletions (default is a dry run)")
	asJSON := fs.Bool("json", false, "output JSON")
	_ = fs.Parse(args)

	path, err := resolveDB(*dbPath)
	if err != nil {
		fail(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		fail(err)
	}
	st, err := store.Open(path)
	if err != nil {
		fail(err)
	}
	defer st.Close()

	rep, err := prune.Run(context.Background(), st, cfg.Retention, *apply, time.Now())
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
	printPrune(rep)
}

func printPrune(rep prune.Report) {
	mode := "dry run (no rows deleted; use --apply)"
	if rep.Applied {
		mode = "applied"
	}
	fmt.Printf("mode:             %s\n", mode)
	fmt.Printf("daily cutoff:     %s\n", orDash(rep.DailyCutoff))
	fmt.Printf("weekly cutoff:    %s\n", orDash(rep.WeeklyCutoff))
	fmt.Printf("daily files:      %s\n", orDash(rep.DailyFileCutoff))
	fmt.Printf("weekly files:     %s\n", orDash(rep.WeeklyFileCutoff))
	fmt.Printf("summary cutoff:   %s\n", orDash(rep.SummaryCutoff))
	if rep.EventsCutoffMS > 0 {
		fmt.Printf("events cutoff:    %s\n", formatMillis(rep.EventsCutoffMS))
	}
	fmt.Printf("expired snapshots:%d\n", len(rep.Snapshots))
	for _, sn := range rep.Snapshots {
		fmt.Printf("  #%-4d %-6s %-8s %s  files=%d dirs=%d alloc=%s\n",
			sn.ID, sn.Kind, sn.Root, formatMillis(sn.StartedAtMS), sn.Files, sn.Dirs,
			formatBytes(sn.UniqueAllocBytes))
	}
	fmt.Printf("file snapshots:   %d (%d rows)\n", len(rep.FileSnapshotIDs), rep.FileRows)
	fmt.Printf("daily summaries:  %d\n", rep.Summaries)
	fmt.Printf("daily top dirs:   %d\n", rep.TopDirs)
	fmt.Printf("file events:      %d\n", rep.FileEvents)
	if rep.Applied {
		fmt.Printf("deleted:          snapshots=%d file_rows=%d summaries=%d top_dirs=%d events=%d\n",
			rep.Deleted.Snapshots, rep.Deleted.FileRows,
			rep.Deleted.Summaries, rep.Deleted.TopDirs, rep.Deleted.FileEvents)
	}
	fmt.Println("note: retention only deletes FatTrace history; scanned files are never touched.")
}

func orDash(s string) string {
	if s == "" {
		return "disabled"
	}
	return s
}
