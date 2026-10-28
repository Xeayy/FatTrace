//go:build windows

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"fattrace/internal/diff"
	"fattrace/internal/model"
	"fattrace/internal/store"
)

func diffCmd(args []string) {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	fromID := fs.Int64("from", 0, "from snapshot id (default: previous complete)")
	toID := fs.Int64("to", 0, "to snapshot id (default: latest complete)")
	volume := fs.String("volume", "", `volume root prefix, e.g. "D:"`)
	week := fs.Bool("week", false, "warn when the pair is not about 7 days apart")
	topN := fs.Int("top", 20, "rows per ranking list")
	crossVolume := fs.Bool("cross-volume", false, "annotate added files that match other volumes")
	asJSON := fs.Bool("json", false, "output JSON")
	dbPath := fs.String("db", "", "database file")
	_ = fs.Parse(args)
	if fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: fat diff [flags]")
		os.Exit(2)
	}

	path, err := resolveDB(*dbPath)
	if err != nil {
		fail(err)
	}
	st, err := store.Open(path)
	if err != nil {
		fail(err)
	}
	defer st.Close()

	res, err := diff.Run(context.Background(), st, diff.Options{
		FromID: *fromID, ToID: *toID, RootPrefix: *volume, TopN: *topN,
		CrossVolume: *crossVolume,
	})
	if err != nil {
		fail(err)
	}
	if *week {
		span := res.ToSnapshot.StartedAtMS - res.FromSnapshot.StartedAtMS
		const dayMS = int64(24 * time.Hour / time.Millisecond)
		if span < 5*dayMS || span > 9*dayMS {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"--week: snapshots are %.1f days apart", float64(span)/float64(dayMS)))
		}
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			fail(err)
		}
		return
	}
	printDiff(res)
}

func printDiff(r model.DiffResult) {
	t := r.Totals
	const dayMS = int64(24 * time.Hour / time.Millisecond)
	span := float64(r.ToSnapshot.StartedAtMS-r.FromSnapshot.StartedAtMS) / float64(dayMS)

	fmt.Printf("snapshots: #%d %s → #%d %s  (%.1f days)\n",
		r.FromSnapshot.ID, formatMillis(r.FromSnapshot.StartedAtMS),
		r.ToSnapshot.ID, formatMillis(r.ToSnapshot.StartedAtMS), span)
	fmt.Printf("root:      %s\n", r.Root)
	fmt.Printf("files:     %s → %s  (%+d)\n",
		formatCount(r.FromSnapshot.Files), formatCount(r.ToSnapshot.Files),
		r.ToSnapshot.Files-r.FromSnapshot.Files)
	fmt.Printf("dirs:      %s → %s  (%+d)\n",
		formatCount(r.FromSnapshot.Dirs), formatCount(r.ToSnapshot.Dirs),
		r.ToSnapshot.Dirs-r.FromSnapshot.Dirs)
	fmt.Printf("unique:    %s → %s  (%s)\n",
		formatBytes(r.FromSnapshot.UniqueAllocBytes), formatBytes(r.ToSnapshot.UniqueAllocBytes),
		signedBytes(r.ToSnapshot.UniqueAllocBytes-r.FromSnapshot.UniqueAllocBytes))

	fmt.Println("\nchanges:")
	fmt.Printf("  files     %s added, %s deleted, %s grown, %s shrunk, %s moved, %s renamed\n",
		formatCount(t.FilesAdded), formatCount(t.FilesDeleted), formatCount(t.FilesGrown),
		formatCount(t.FilesShrunk), formatCount(t.FilesMoved), formatCount(t.FilesRenamed))
	fmt.Printf("  dirs      %s added, %s deleted, %s moved, %s renamed\n",
		formatCount(t.DirsAdded), formatCount(t.DirsDeleted),
		formatCount(t.DirsMoved), formatCount(t.DirsRenamed))
	fmt.Printf("  net       %s logical / %s allocated\n",
		signedBytes(t.DeltaBytes), signedBytes(t.DeltaAlloc))

	printFileList("top growth (allocated delta)", r.TopGrowth)
	printFileList("top deleted (allocated)", r.TopDeleted)
	printFileMoves(r.Moves)
	printDirList("top growth dirs (move-adjusted)", r.TopDirs)
	printDirList("top shrunk dirs (move-adjusted)", r.ShrunkDirs)
	printDirMoves(r.DirMoves)

	fmt.Println("\nvolume check:")
	if r.Check.Skipped {
		fmt.Printf("  skipped: %s\n", r.Check.Message)
	} else {
		status := "OK"
		if !r.Check.OK {
			status = "WARN"
		}
		fmt.Printf("  root Δ %s vs volume Δ %s  (%.2f%%)  %s\n",
			signedBytes(r.Check.RootDeltaAlloc), signedBytes(r.Check.DiskDeltaAlloc),
			r.Check.DiffPct, status)
		if !r.Check.OK {
			fmt.Printf("  %s\n", r.Check.Message)
		}
	}
	if r.Check.UnknownDirs > 0 {
		fmt.Printf("  unknown dirs after snapshot: %d (unknown propagates to ancestors)\n", r.Check.UnknownDirs)
	}

	if len(r.Warnings) > 0 {
		fmt.Println("\nwarnings:")
		for _, w := range r.Warnings {
			fmt.Printf("  - %s\n", w)
		}
	}
}

func printFileList(title string, items []model.FileChange) {
	if len(items) == 0 {
		return
	}
	fmt.Printf("\n%s:\n", title)
	for _, c := range items {
		switch c.Kind {
		case model.ChangeAdded:
			line := fmt.Sprintf("  %12s  %s", signedBytes(c.DeltaAlloc), c.Path)
			if c.SuspectedFromVolume != 0 {
				line += fmt.Sprintf("  [suspected cross-volume move from 0x%08X]", c.SuspectedFromVolume)
			}
			fmt.Println(line)
		case model.ChangeGrown:
			fmt.Printf("  %12s  %s  (%s → %s)\n", signedBytes(c.DeltaAlloc), c.Path,
				formatBytes(c.AllocBefore), formatBytes(c.AllocAfter))
		default:
			fmt.Printf("  %12s  %s  (was %s)\n", signedBytes(c.DeltaAlloc), c.Path,
				formatBytes(c.AllocBefore))
		}
	}
}

func printFileMoves(items []model.FileChange) {
	if len(items) == 0 {
		return
	}
	fmt.Println("\nfile moves/renames:")
	for _, c := range items {
		label := "move"
		if c.Kind == model.ChangeRenamed {
			label = "rename"
		}
		oldPath := c.OldPath
		if oldPath == "" {
			oldPath = c.Path
		}
		fmt.Printf("  %12s  %s\n                 → %s  (%s)\n",
			formatBytes(c.AllocAfter), oldPath, c.Path, label)
	}
}

func printDirList(title string, items []model.DirChange) {
	if len(items) == 0 {
		return
	}
	fmt.Printf("\n%s:\n", title)
	for _, d := range items {
		fmt.Printf("  %12s  %s  (%s files)\n", signedBytes(d.DeltaAlloc), d.Path,
			formatCount(d.FilesAfter))
	}
}

func printDirMoves(items []model.DirChange) {
	if len(items) == 0 {
		return
	}
	fmt.Println("\ndirectory moves/renames:")
	for _, d := range items {
		label := "move"
		if d.Kind == model.ChangeRenamed {
			label = "rename"
		}
		oldPath := d.OldPath
		if oldPath == "" {
			oldPath = d.Path
		}
		fmt.Printf("  %12s  %s\n                 → %s  (%s)\n",
			formatBytes(d.TotalAllocAfter), oldPath, d.Path, label)
	}
}

func signedBytes(n int64) string {
	if n > 0 {
		return "+" + formatBytes(n)
	}
	return formatBytes(n)
}

// formatCount renders an integer with thousands separators.
func formatCount(n int64) string {
	sign := ""
	u := uint64(n)
	if n < 0 {
		sign = "-"
		u = uint64(-n)
	}
	s := fmt.Sprintf("%d", u)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return sign + b.String()
}
