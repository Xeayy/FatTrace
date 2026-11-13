//go:build windows

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Xeayy/FatTrace/internal/doctor"
)

func doctorCmd(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "output JSON")
	dbPath := fs.String("db", "", "database file")
	skipTasks := fs.Bool("skip-tasks", false, "skip Task Scheduler checks")
	strict := fs.Bool("strict", false, "exit 1 on warnings as well as failures")
	_ = fs.Parse(args)
	if fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: fat doctor [flags]")
		os.Exit(2)
	}

	path, err := resolveDB(*dbPath)
	if err != nil {
		fail(err)
	}
	rep := doctor.Run(context.Background(), doctor.Options{
		DBPath: path, Version: version, SkipTasks: *skipTasks,
	})

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fail(err)
		}
	} else {
		printDoctor(rep)
	}
	if rep.Failed() || (*strict && rep.Count(doctor.LevelWarn) > 0) {
		os.Exit(1)
	}
}

func printDoctor(rep doctor.Report) {
	user := "standard user"
	if rep.Elevated {
		user = "elevated"
	}
	fmt.Printf("FatTrace doctor · %s · %s\n", formatMillis(rep.CheckedAt), user)
	fmt.Printf("database:       %s\n\n", rep.DBPath)
	for _, c := range rep.Checks {
		fmt.Printf("[%-4s] %-26s %s\n", c.Level, c.Name, c.Summary)
		if c.Hint != "" {
			fmt.Printf("       %s\n", c.Hint)
		}
	}
	fmt.Printf("\n%d ok · %d warn · %d fail · %d skipped\n",
		rep.Count(doctor.LevelOK), rep.Count(doctor.LevelWarn),
		rep.Count(doctor.LevelFail), rep.Count(doctor.LevelSkip))
}
