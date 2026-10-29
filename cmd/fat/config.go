//go:build windows

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"fattrace/internal/config"
)

func configCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: fat config init|show [flags]")
		os.Exit(2)
	}
	switch args[0] {
	case "init":
		configInitCmd(args[1:])
	case "show":
		configShowCmd(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown config subcommand %q\n", args[0])
		os.Exit(2)
	}
}

func configInitCmd(args []string) {
	fs := flag.NewFlagSet("config init", flag.ExitOnError)
	dbPath := fs.String("db", "", "database file")
	rootsFlag := fs.String("roots", "", `comma-separated scan roots, e.g. "D:\"`)
	force := fs.Bool("force", false, "overwrite an existing config file")
	_ = fs.Parse(args)

	path, err := resolveDB(*dbPath)
	if err != nil {
		fail(err)
	}
	cfgPath, err := config.PathFor(path)
	if err != nil {
		fail(err)
	}
	if _, err := os.Stat(cfgPath); err == nil && !*force {
		fmt.Fprintf(os.Stderr, "config exists: %s\nuse --force to overwrite\n", cfgPath)
		os.Exit(1)
	}
	var roots []string
	for _, r := range strings.Split(*rootsFlag, ",") {
		if r = strings.TrimSpace(r); r != "" {
			roots = append(roots, r)
		}
	}
	cfg := config.Defaults()
	cfg.Schedule.Roots = roots
	if err := cfg.Validate(); err != nil {
		fail(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		fail(err)
	}
	if err := os.WriteFile(cfgPath, []byte(config.Template(cfg.Schedule.Roots)), 0o600); err != nil {
		fail(err)
	}
	fmt.Printf("config: %s\n", cfgPath)
	if len(cfg.Schedule.Roots) == 0 {
		fmt.Println("roots:  none (edit schedule.roots before installing tasks)")
	} else {
		fmt.Printf("roots:  %s\n", strings.Join(cfg.Schedule.Roots, ", "))
	}
}

func configShowCmd(args []string) {
	fs := flag.NewFlagSet("config show", flag.ExitOnError)
	dbPath := fs.String("db", "", "database file")
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
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(cfg); err != nil {
			fail(err)
		}
		return
	}
	fmt.Printf("path:    %s\n", cfg.Path)
	exists := "no (built-in defaults)"
	if cfg.Exists {
		exists = "yes"
	}
	fmt.Printf("exists:  %s\n", exists)
	data, err := cfg.Marshal()
	if err != nil {
		fail(err)
	}
	os.Stdout.Write(data)
}
