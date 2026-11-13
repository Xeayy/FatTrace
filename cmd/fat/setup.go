//go:build windows

package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/Xeayy/FatTrace/internal/config"
	"github.com/Xeayy/FatTrace/internal/schedule"
	"github.com/Xeayy/FatTrace/internal/store"

	"github.com/mattn/go-isatty"
	"golang.org/x/sys/windows"
)

func setupCmd(args []string) {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	dbPath := fs.String("db", "", "database file")
	rootsFlag := fs.String("roots", "", `comma-separated scan roots, e.g. "D:\"`)
	installTasks := fs.Bool("install-tasks", false, "install or replace the scheduled tasks")
	noTasks := fs.Bool("no-tasks", false, "only initialize the database and config")
	open := fs.Bool("open", false, "open the dashboard when setup finishes")
	_ = fs.Parse(args)
	if fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: fat setup [flags]")
		os.Exit(2)
	}
	if *installTasks && *noTasks {
		fail(fmt.Errorf("setup: -install-tasks and -no-tasks are mutually exclusive"))
	}

	path, err := resolveDB(*dbPath)
	if err != nil {
		fail(err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		fail(err)
	}
	path = abs

	fmt.Println("FatTrace setup")
	fmt.Println()

	s, err := store.OpenWait(path, 30*time.Second)
	if err != nil {
		fail(err)
	}
	st, err := s.Status(context.Background())
	if err != nil {
		s.Close()
		fail(err)
	}
	s.Close()
	snapshots := tableRows(st.Tables, "snapshots")
	fmt.Printf("database:  %s\n", st.Path)
	fmt.Printf("schema:    v%d · %s · %d snapshots\n",
		st.SchemaVersion, formatBytes(st.SizeBytes+st.WALBytes), snapshots)
	fmt.Println()

	p := newPrompter()
	cfg, err := config.Load(path)
	if err != nil {
		fail(err)
	}
	roots := splitRoots(*rootsFlag)
	interactive := stdinIsTerminal()

	switch {
	case !cfg.Exists:
		if len(roots) == 0 && interactive {
			roots = promptRoots(p)
		}
		if len(roots) == 0 {
			fail(fmt.Errorf("setup: no scan roots; pass -roots %q", `D:\`))
		}
		cfg.Schedule.Roots = roots
		if err := cfg.Validate(); err != nil {
			fail(err)
		}
		if err := writeNewConfig(cfg); err != nil {
			fail(err)
		}
		fmt.Printf("config:    created %s\n", cfg.Path)
	case len(roots) > 0 && !sameRoots(cfg.Schedule.Roots, roots):
		cfg.Schedule.Roots = roots
		if err := cfg.Validate(); err != nil {
			fail(err)
		}
		if err := writeConfig(cfg); err != nil {
			fail(err)
		}
		fmt.Printf("config:    updated roots in %s (comments are normalized)\n", cfg.Path)
	case len(cfg.Schedule.Roots) == 0:
		if !interactive {
			fail(fmt.Errorf("setup: %s has no schedule.roots; pass -roots %q", cfg.Path, `D:\`))
		}
		cfg.Schedule.Roots = promptRoots(p)
		if err := cfg.Validate(); err != nil {
			fail(err)
		}
		if err := writeConfig(cfg); err != nil {
			fail(err)
		}
		fmt.Printf("config:    updated roots in %s (comments are normalized)\n", cfg.Path)
	default:
		fmt.Printf("config:    %s\n", cfg.Path)
	}
	fmt.Printf("roots:     %s\n", strings.Join(cfg.Schedule.Roots, ", "))
	for _, root := range cfg.Schedule.Roots {
		if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
			fmt.Printf("warning:   scan root %q is not available right now\n", root)
		}
		if !isVolumeRoot(root) {
			fmt.Printf("warning:   %q is not a volume root; dashboard diffs and daily summaries only use whole-volume scans like D:\\\n", root)
		}
	}
	fmt.Printf("schedule:  daily %s · weekly %s · ac_only=%v\n",
		cfg.Schedule.DailyAt, cfg.Schedule.WeeklyAt, cfg.Schedule.ACOnly)
	if !cfg.Exists {
		fmt.Println("           config file uses built-in defaults")
	}
	fmt.Println()

	if *noTasks {
		fmt.Println("tasks:     skipped (-no-tasks)")
	} else {
		if err := setupTasks(path, cfg, *installTasks, interactive, p); err != nil {
			fail(err)
		}
	}

	fmt.Println()
	fmt.Println("next:")
	if snapshots == 0 {
		fmt.Println("  first scan   fat maintain --kind daily")
	}
	fmt.Println("  dashboard    fat serve --open")
	fmt.Println("  health       fat doctor")
	if *open {
		fmt.Println()
		serveAfterSetup(path)
	}
}

func setupTasks(path string, cfg config.Config, install, interactive bool, p *prompter) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	tasks, err := schedule.Build(schedule.Options{
		Exe: exe, DBPath: path, Roots: cfg.Schedule.Roots,
		DailyAt: cfg.Schedule.DailyAt, WeeklyAt: cfg.Schedule.WeeklyAt,
		ACOnly: cfg.Schedule.ACOnly,
	})
	if err != nil {
		return err
	}

	ctx := context.Background()
	var missing []schedule.Task
	for _, t := range tasks {
		r, err := schedule.Query(ctx, t.Name)
		if err != nil {
			return err
		}
		if !r.Exists {
			missing = append(missing, t)
			fmt.Printf("tasks:     %-16s missing\n", t.Name)
			continue
		}
		fmt.Printf("tasks:     %-16s installed · next=%s · ac_only=%v\n",
			t.Name, t.NextRun.Format("2006-01-02 15:04"), r.DisallowStartIfOnBatteries)
		if r.Command != t.Command || r.Arguments != t.Args {
			fmt.Println("           task points at a different executable or database; replace with -install-tasks")
		}
		if cfg.Schedule.ACOnly && !r.DisallowStartIfOnBatteries {
			fmt.Println("           config wants AC-only but the installed task is not; replace with -install-tasks")
		}
		if !r.StartWhenAvailable {
			fmt.Println("           missed runs are not caught up; replace with -install-tasks")
		}
	}

	if install {
		if err := schedule.Install(ctx, tasks, true, false, os.Stdout); err != nil {
			return err
		}
		return nil
	}
	if len(missing) == 0 {
		fmt.Println("tasks:     installed; no changes")
		return nil
	}
	if !interactive {
		return fmt.Errorf("setup: %d scheduled task(s) missing; re-run with -install-tasks", len(missing))
	}
	ok, err := p.yesNo("Install the missing scheduled tasks now?", true)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Println("tasks:     skipped; run fat schedule install when ready")
		return nil
	}
	if err := schedule.Install(ctx, missing, false, false, os.Stdout); err != nil {
		return err
	}
	return nil
}

func promptRoots(p *prompter) []string {
	volumes := fixedVolumes()
	if len(volumes) > 0 {
		fmt.Printf("detected fixed volumes: %s\n", strings.Join(volumes, ", "))
	}
	for {
		line, err := p.line(`scan roots (comma-separated, e.g. D:\): `)
		if err != nil {
			return nil
		}
		roots := splitRoots(line)
		if len(roots) > 0 {
			return roots
		}
		fmt.Println("at least one absolute root is required")
	}
}

func writeNewConfig(cfg config.Config) error {
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(cfg.Path, []byte(config.Template(cfg.Schedule.Roots)), 0o600)
}

func writeConfig(cfg config.Config) error {
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
		return err
	}
	data, err := cfg.Marshal()
	if err != nil {
		return err
	}
	return os.WriteFile(cfg.Path, data, 0o600)
}

func splitRoots(s string) []string {
	var roots []string
	for _, r := range strings.Split(s, ",") {
		if r = strings.TrimSpace(r); r != "" {
			roots = append(roots, r)
		}
	}
	return roots
}

func sameRoots(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.EqualFold(filepath.Clean(a[i]), filepath.Clean(b[i])) {
			continue
		}
		return false
	}
	return true
}

func isVolumeRoot(root string) bool {
	clean := filepath.Clean(root)
	volume := filepath.VolumeName(clean)
	if volume == "" {
		return false
	}
	return strings.EqualFold(clean, volume) || strings.EqualFold(clean, volume+`\`)
}

func fixedVolumes() []string {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	var roots []string
	for i := 0; i < 26; i++ {
		if mask&(1<<i) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		ptr, err := windows.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		if windows.GetDriveType(ptr) == windows.DRIVE_FIXED {
			roots = append(roots, root)
		}
	}
	return roots
}

func tableRows(tables []store.TableCount, name string) int64 {
	for _, t := range tables {
		if t.Name == name {
			return t.Rows
		}
	}
	return 0
}

func stdinIsTerminal() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
}

type prompter struct {
	r *bufio.Reader
}

func newPrompter() *prompter {
	return &prompter{r: bufio.NewReader(os.Stdin)}
}

func (p *prompter) line(label string) (string, error) {
	fmt.Print(label)
	line, err := p.r.ReadString('\n')
	if err != nil && !(err == io.EOF && line != "") {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (p *prompter) yesNo(label string, def bool) (bool, error) {
	hint := "[Y/n]"
	if !def {
		hint = "[y/N]"
	}
	for {
		line, err := p.line(label + " " + hint + " ")
		if err != nil {
			return false, err
		}
		switch strings.ToLower(line) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
	}
}

func serveAfterSetup(path string) {
	local, err := startLocalServer(path, "127.0.0.1:0", 20)
	if err != nil {
		fail(err)
	}
	defer local.Close()
	fmt.Printf("listening  %s/\n", local.base)
	fmt.Printf("open       %s\n", local.url)
	fmt.Println("read-only · token required · Ctrl+C to stop")
	if err := openBrowser(local.url); err != nil {
		fmt.Fprintf(os.Stderr, "warning: open browser: %v\n", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := local.srv.Serve(ctx, local.ln); err != nil {
		fail(err)
	}
}
