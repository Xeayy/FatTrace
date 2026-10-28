//go:build windows

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	"fattrace/internal/model"
	"fattrace/internal/scan"
)

const version = "0.1.0-dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "scan":
		scanCmd(os.Args[2:])
	case "snap":
		snapCmd(os.Args[2:])
	case "diff":
		diffCmd(os.Args[2:])
	case "report":
		reportCmd(os.Args[2:])
	case "serve":
		serveCmd(os.Args[2:])
	case "fileid":
		fileIDCmd(os.Args[2:])
	case "db":
		dbCmd(os.Args[2:])
	case "status":
		statusCmd(os.Args[2:])
	case "version":
		fmt.Println(version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `FatTrace - disk growth tracker

usage:
  fat scan [flags] <root>
  fat snap [flags] <root>
  fat diff [flags]
  fat report [flags]
  fat serve [flags]
  fat fileid <path>
  fat db init [--db path]
  fat status [--json] [--db path]
  fat version

scan flags:
  -json            output JSON
  -max-depth int   maximum directory depth (0 = unlimited)
  -no-dedup        do not deduplicate hard links

snap flags:
  -kind string     snapshot kind: manual, daily or weekly (default "manual")
  -db string       database file (overrides FATTRACE_HOME)
  -json            output JSON
  -max-depth int   maximum directory depth (0 = unlimited)
  -no-dedup        do not deduplicate hard links
  -quiet           suppress periodic progress output

diff flags:
  -from int        from snapshot id (default: previous complete)
  -to int          to snapshot id (default: latest complete)
  -volume string   volume root prefix, e.g. "D:"
  -week            warn when the pair is not about 7 days apart
  -top int         rows per ranking list (default 20)
  -cross-volume    annotate added files that match other volumes
  -db string       database file (overrides FATTRACE_HOME)
  -json            output JSON

report flags:
  -from int        from snapshot id (default: previous complete)
  -to int          to snapshot id (default: latest complete)
  -volume string   volume root prefix, e.g. "D:"
  -week            warn when the pair is not about 7 days apart
  -top int         rows per ranking list (default 20)
  -cross-volume    annotate added files that match other volumes
  -out string      output HTML file (default "fattrace-report.html")
  -db string       database file (overrides FATTRACE_HOME)

serve flags:
  -addr string     listen address, loopback only (default "127.0.0.1:0")
  -top int         ranking rows per diff API call (default 20)
  -db string       database file (overrides FATTRACE_HOME)

The database defaults to %LOCALAPPDATA%\FatTrace\fattrace.db.
FATTRACE_HOME overrides the directory; --db overrides the file.`)
}

func scanCmd(args []string) {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "output JSON")
	maxDepth := fs.Int("max-depth", 0, "maximum directory depth (0 = unlimited)")
	noDedup := fs.Bool("no-dedup", false, "do not deduplicate hard links")
	_ = fs.Parse(args)

	root := "."
	if fs.NArg() > 0 {
		root = fs.Arg(0)
	}

	var peak atomic.Uint64
	done := make(chan struct{})
	go sampleHeap(&peak, done)

	stats, err := scan.Walk(context.Background(), root, scan.Options{
		MaxDepth:         *maxDepth,
		DedupeHardlinks:  !*noDedup,
		ErrorSampleLimit: 200,
	}, nil)
	close(done)
	stats.HeapPeakBytes = peak.Load()

	if err != nil {
		fmt.Fprintf(os.Stderr, "scan failed: %v\n", err)
		os.Exit(1)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(stats); err != nil {
			fmt.Fprintf(os.Stderr, "encode: %v\n", err)
			os.Exit(1)
		}
		return
	}
	printStats(stats)
}

func fileIDCmd(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: fat fileid <path>")
		os.Exit(2)
	}
	id, err := scan.FileID(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "fileid: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("%s\nFRN: %d (0x%X)\n", args[0], id, id)
}

func sampleHeap(peak *atomic.Uint64, done <-chan struct{}) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var m runtime.MemStats
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			runtime.ReadMemStats(&m)
			for {
				p := peak.Load()
				if m.HeapAlloc <= p || peak.CompareAndSwap(p, m.HeapAlloc) {
					break
				}
			}
		}
	}
}

func printStats(s model.ScanStats) {
	fmt.Printf("root:            %s\n", s.Root)
	fmt.Printf("volume:          %s (serial 0x%08X)\n", s.Volume, s.VolumeSerial)
	fmt.Printf("files:           %d\n", s.Files)
	fmt.Printf("dirs:            %d\n", s.Dirs)
	fmt.Printf("logical:         %s\n", formatBytes(s.Bytes))
	fmt.Printf("allocated:       %s\n", formatBytes(s.AllocBytes))
	fmt.Printf("unique:          %s\n", formatBytes(s.UniqueBytes))
	fmt.Printf("unique alloc:    %s\n", formatBytes(s.UniqueAllocBytes))
	fmt.Printf("hardlink dupes:  %d\n", s.HardlinkDupes)
	fmt.Printf("reparse skipped: %d\n", s.ReparseSkipped)
	fmt.Printf("errors:          %d\n", s.Errors)
	fmt.Printf("duration:        %s", time.Duration(s.DurationMS)*time.Millisecond)
	if s.DurationMS > 0 {
		rate := float64(s.Files) / (float64(s.DurationMS) / 1000)
		fmt.Printf(" (%.0f files/s)", rate)
	}
	fmt.Println()
	fmt.Printf("heap peak:       %s\n", formatBytes(int64(s.HeapPeakBytes)))
	if s.VolumeUsed > 0 {
		fmt.Printf("volume used:     %s / %s (free %s)\n",
			formatBytes(s.VolumeUsed), formatBytes(s.VolumeTotal), formatBytes(s.VolumeFree))
	}
	if s.CoveragePct > 0 {
		fmt.Printf("coverage:        %.2f%% of used bytes\n", s.CoveragePct)
	}
	if len(s.ErrorSamples) > 0 {
		fmt.Println("error samples:")
		for i, e := range s.ErrorSamples {
			if i >= 10 {
				fmt.Printf("  ... (%d more)\n", len(s.ErrorSamples)-10)
				break
			}
			fmt.Printf("  [%s] %s: %s\n", e.Op, e.Path, e.Msg)
		}
	}
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n2 := n / unit; n2 >= unit; n2 /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
