//go:build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"fattrace/internal/config"
	"fattrace/internal/schedule"
)

func scheduleCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: fat schedule install|uninstall|status [flags]")
		os.Exit(2)
	}
	switch args[0] {
	case "install":
		scheduleInstallCmd(args[1:])
	case "uninstall":
		scheduleUninstallCmd(args[1:])
	case "status":
		scheduleStatusCmd(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown schedule subcommand %q\n", args[0])
		os.Exit(2)
	}
}

func scheduleInstallCmd(args []string) {
	fs := flag.NewFlagSet("schedule install", flag.ExitOnError)
	dbPath := fs.String("db", "", "database file")
	dryRun := fs.Bool("dry-run", false, "write XML and print commands only")
	force := fs.Bool("force", false, "replace existing tasks")
	_ = fs.Parse(args)

	path, err := resolveDB(*dbPath)
	if err != nil {
		fail(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		fail(err)
	}
	exe, err := os.Executable()
	if err != nil {
		fail(err)
	}
	tasks, err := schedule.Build(schedule.Options{
		Exe: exe, DBPath: path, Roots: cfg.Schedule.Roots,
		DailyAt: cfg.Schedule.DailyAt, WeeklyAt: cfg.Schedule.WeeklyAt,
		ACOnly: cfg.Schedule.ACOnly,
	})
	if err != nil {
		fail(err)
	}
	ctx := context.Background()
	if err := schedule.Install(ctx, tasks, *force, *dryRun, os.Stdout); err != nil {
		fail(err)
	}
	if !*dryRun {
		fmt.Println("tasks run as the current user (least privilege); scans see the same files as a manual fat snap.")
	}
}

func scheduleUninstallCmd(args []string) {
	fs := flag.NewFlagSet("schedule uninstall", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "print commands only")
	_ = fs.Parse(args)

	if err := schedule.Uninstall(context.Background(),
		[]string{schedule.DailyName, schedule.WeeklyName}, *dryRun, os.Stdout); err != nil {
		fail(err)
	}
}

func scheduleStatusCmd(args []string) {
	fs := flag.NewFlagSet("schedule status", flag.ExitOnError)
	dbPath := fs.String("db", "", "database file")
	_ = fs.Parse(args)

	path, err := resolveDB(*dbPath)
	if err != nil {
		fail(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		fail(err)
	}
	fmt.Printf("config:    %s\n", cfg.Path)
	fmt.Printf("daily:     %s\n", cfg.Schedule.DailyAt)
	fmt.Printf("weekly:    %s\n", cfg.Schedule.WeeklyAt)
	fmt.Printf("ac_only:   %v\n", cfg.Schedule.ACOnly)
	if len(cfg.Schedule.Roots) == 0 {
		fmt.Println("roots:     none (schedule install will refuse)")
	} else {
		fmt.Printf("roots:     %v\n", cfg.Schedule.Roots)
	}
	for _, name := range []string{schedule.DailyName, schedule.WeeklyName} {
		r, err := schedule.Query(context.Background(), name)
		if err != nil {
			fail(err)
		}
		if !r.Exists {
			fmt.Printf("%-10s missing\n", name)
			continue
		}
		fmt.Printf("%-10s installed  %s  run=%s  ac_only=%v  start=%s\n",
			name, r.Command, r.RunLevel, r.DisallowStartIfOnBatteries, r.StartBoundary)
		if cfg.Schedule.ACOnly && !r.DisallowStartIfOnBatteries {
			fmt.Println("           config wants AC-only but the installed task is not; reinstall with fat schedule install --force")
		}
		if !r.StartWhenAvailable {
			fmt.Println("           missed runs are not caught up; reinstall with fat schedule install --force")
		}
		if r.Command == "" {
			fmt.Println("           task has no executable")
		}
	}
}
