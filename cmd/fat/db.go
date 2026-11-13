//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Xeayy/FatTrace/internal/store"
)

func dbCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: fat db init [--db path]")
		os.Exit(2)
	}
	switch args[0] {
	case "init":
		dbInitCmd(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown db subcommand %q\n", args[0])
		os.Exit(2)
	}
}

func dbInitCmd(args []string) {
	fs := flag.NewFlagSet("db init", flag.ExitOnError)
	dbPath := fs.String("db", "", "database file")
	_ = fs.Parse(args)

	path, err := resolveDB(*dbPath)
	if err != nil {
		fail(err)
	}
	_, statErr := os.Stat(path)
	fresh := errors.Is(statErr, os.ErrNotExist)

	s, err := store.Open(path)
	if err != nil {
		fail(err)
	}
	defer s.Close()

	st, err := s.Status(context.Background())
	if err != nil {
		fail(err)
	}
	if fresh {
		fmt.Printf("created:  %s\n", s.Path())
	} else {
		fmt.Printf("database: %s\n", s.Path())
	}
	fmt.Printf("schema:   v%d\n", st.SchemaVersion)
	fmt.Printf("size:     %s\n", formatBytes(st.SizeBytes+st.WALBytes))
	fmt.Printf("tables:   %d\n", len(st.Tables))
}

func statusCmd(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "output JSON")
	dbPath := fs.String("db", "", "database file")
	_ = fs.Parse(args)

	path, err := resolveDB(*dbPath)
	if err != nil {
		fail(err)
	}
	s, err := store.OpenReader(path)
	if err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			fmt.Fprintf(os.Stderr, "no database at %s\nrun: fat db init\n", path)
			os.Exit(1)
		case errors.Is(err, store.ErrNotInitialized):
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		default:
			fail(err)
		}
	}
	defer s.Close()

	st, err := s.Status(context.Background())
	if err != nil {
		fail(err)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(st); err != nil {
			fail(err)
		}
		return
	}
	printStatus(st)
}

func printStatus(st *store.Status) {
	fmt.Printf("database: %s\n", st.Path)
	fmt.Printf("size:     %s (db %s + wal %s)\n",
		formatBytes(st.SizeBytes+st.WALBytes), formatBytes(st.SizeBytes), formatBytes(st.WALBytes))
	fmt.Printf("schema:   v%d\n", st.SchemaVersion)

	if len(st.Volumes) == 0 {
		fmt.Println("volumes:  none")
	} else {
		fmt.Printf("volumes:  %d\n", len(st.Volumes))
		for _, v := range st.Volumes {
			label := v.Label
			if label == "" {
				label = "-"
			}
			fmt.Printf("  0x%08X  %-5s  label=%-12s  last seen %s\n",
				v.Serial, v.FSType, label, formatMillis(v.LastSeen))
		}
	}

	if len(st.RecentSnapshots) == 0 {
		fmt.Println("snapshots: none")
	} else {
		fmt.Printf("snapshots: %d recent\n", len(st.RecentSnapshots))
		for _, sn := range st.RecentSnapshots {
			fmt.Printf("  #%-4d %-6s %-8s %s  files=%d dirs=%d alloc=%s coverage=%.2f%% errors=%d\n",
				sn.ID, sn.Kind, sn.Status, formatMillis(sn.StartedAt), sn.Files, sn.Dirs,
				formatBytes(sn.UniqueAllocBytes), sn.CoveragePct, sn.Errors)
		}
	}

	fmt.Println("tables:")
	for _, t := range st.Tables {
		fmt.Printf("  %-16s %d\n", t.Name, t.Rows)
	}
}

func resolveDB(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	return store.DefaultPath()
}

func formatMillis(ms int64) string {
	if ms == 0 {
		return "-"
	}
	return time.UnixMilli(ms).Local().Format("2006-01-02 15:04:05")
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
