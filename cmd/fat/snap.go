//go:build windows

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fattrace/internal/snap"
	"fattrace/internal/store"
)

// The Go syscall package does not export SIGBREAK; the Windows runtime still
// delivers CTRL_BREAK_EVENT as signal 21.
const sigBreak = syscall.Signal(21)

func snapCmd(args []string) {
	fs := flag.NewFlagSet("snap", flag.ExitOnError)
	kind := fs.String("kind", "manual", "snapshot kind: manual, daily or weekly")
	dbPath := fs.String("db", "", "database file")
	maxDepth := fs.Int("max-depth", 0, "maximum directory depth (0 = unlimited)")
	noDedup := fs.Bool("no-dedup", false, "do not deduplicate hard links")
	quiet := fs.Bool("quiet", false, "suppress periodic progress output")
	asJSON := fs.Bool("json", false, "output JSON")
	_ = fs.Parse(args)

	root := "."
	if fs.NArg() > 0 {
		root = fs.Arg(0)
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

	// CTRL_BREAK is included so the scan can be cancelled by tooling that can
	// only target a process group (CTRL_C does not reach process groups).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, sigBreak)
	defer stop()

	opts := snap.Options{Root: root, Kind: *kind, MaxDepth: *maxDepth, NoDedup: *noDedup}
	if !*quiet && !*asJSON {
		opts.ProgressInterval = 2 * time.Second
		opts.Progress = func(files, dirs int64) {
			fmt.Fprintf(os.Stderr, "scanning: %d files, %d dirs\n", files, dirs)
		}
	}

	start := time.Now()
	res, err := snap.Run(ctx, st, opts)
	if err != nil {
		if *asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(struct {
				snap.Result
				Error string `json:"error"`
			}{res, err.Error()})
		} else {
			status := res.Status
			if status == "" {
				status = "failed"
			}
			fmt.Fprintf(os.Stderr, "snapshot %s after %s: %v\n",
				status, time.Since(start).Round(time.Millisecond), err)
		}
		if ctx.Err() != nil {
			os.Exit(130)
		}
		os.Exit(1)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			fail(err)
		}
		return
	}
	printSnapResult(res)
}

func printSnapResult(r snap.Result) {
	fmt.Printf("snapshot:      #%d %s (%s)\n", r.SnapshotID, r.Kind, r.Status)
	fmt.Printf("root:          %s\n", r.Root)
	fmt.Printf("volume:        %s\n", r.Volume)
	baseline := "kept (subdirectory scan)"
	if r.BaselineUpdated {
		baseline = "updated"
	}
	fmt.Printf("baseline:      %s\n", baseline)
	fmt.Printf("files:         %d\n", r.Files)
	fmt.Printf("dirs:          %d\n", r.Dirs)
	fmt.Printf("unique alloc:  %s\n", formatBytes(r.Stats.UniqueAllocBytes))
	if r.Stats.HardlinkDupes > 0 {
		fmt.Printf("hardlink dupes:%d\n", r.Stats.HardlinkDupes)
	}
	fmt.Printf("errors:        %d\n", r.Stats.Errors)
	if r.Stats.CoveragePct > 0 {
		fmt.Printf("coverage:      %.2f%% of used bytes\n", r.Stats.CoveragePct)
	}
	fmt.Printf("staged rows:   %d\n", r.StagedRows)
	fmt.Printf("snapshot dirs: %d\n", r.SnapshotDirs)
	fmt.Printf("duration:      %s", time.Duration(r.DurationMS)*time.Millisecond)
	if r.DurationMS > 0 {
		rate := float64(r.Files) / (float64(r.DurationMS) / 1000)
		fmt.Printf(" (%.0f files/s)", rate)
	}
	fmt.Println()
	if len(r.Stats.ErrorSamples) > 0 {
		fmt.Println("error samples:")
		for i, e := range r.Stats.ErrorSamples {
			if i >= 10 {
				fmt.Printf("  ... (%d more)\n", len(r.Stats.ErrorSamples)-10)
				break
			}
			fmt.Printf("  [%s] %s: %s\n", e.Op, e.Path, e.Msg)
		}
	}
}
