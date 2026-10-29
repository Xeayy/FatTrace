// Package schedule installs the daily/weekly FatTrace jobs in Windows Task
// Scheduler. Tasks run as the current user with least privilege; elevation is
// neither required nor attempted.
package schedule

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"

	"fattrace/internal/config"
)

const (
	DailyName  = "FatTrace-Daily"
	WeeklyName = "FatTrace-Weekly"
)

// Options describes the two generated tasks.
type Options struct {
	Exe      string
	DBPath   string
	Roots    []string
	DailyAt  string
	WeeklyAt string
	Now      time.Time
}

// Task is one generated Task Scheduler job.
type Task struct {
	Name    string    `json:"name"`
	Kind    string    `json:"kind"`
	XMLPath string    `json:"xml_path"`
	NextRun time.Time `json:"next_run"`
	Command string    `json:"command"`
	Args    string    `json:"arguments"`
}

// Build validates the schedule and computes both tasks without touching the
// system. Roots must be non-empty: FatTrace never schedules a default volume.
func Build(opts Options) ([]Task, error) {
	if len(opts.Roots) == 0 {
		return nil, fmt.Errorf("schedule: config schedule.roots is empty; run fat config init -roots \"D:\\\"")
	}
	if opts.Exe == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		opts.Exe = exe
	}
	exe, err := filepath.Abs(opts.Exe)
	if err != nil {
		return nil, err
	}
	if opts.DBPath == "" {
		return nil, fmt.Errorf("schedule: database path is empty")
	}
	db, err := filepath.Abs(opts.DBPath)
	if err != nil {
		return nil, err
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	daily, err := config.NextDaily(opts.Now, opts.DailyAt)
	if err != nil {
		return nil, fmt.Errorf("schedule: daily_at: %w", err)
	}
	weekly, err := config.NextWeekly(opts.Now, opts.WeeklyAt)
	if err != nil {
		return nil, fmt.Errorf("schedule: weekly_at: %w", err)
	}
	dir := filepath.Join(filepath.Dir(db), "tasks")
	mk := func(name, kind string, next time.Time) Task {
		args := fmt.Sprintf(`maintain --kind %s --quiet --db "%s"`, kind, db)
		return Task{
			Name: name, Kind: kind, NextRun: next,
			XMLPath: filepath.Join(dir, name+".xml"),
			Command: exe, Args: args,
		}
	}
	return []Task{
		mk(DailyName, "daily", daily),
		mk(WeeklyName, "weekly", weekly),
	}, nil
}

type scheduleKind int

const (
	scheduleDaily scheduleKind = iota
	scheduleWeekly
)

// WriteXML writes the task definition in the UTF-16 encoding schtasks expects.
func (t Task) WriteXML() error {
	if err := os.MkdirAll(filepath.Dir(t.XMLPath), 0o755); err != nil {
		return err
	}
	data, err := t.marshal()
	if err != nil {
		return err
	}
	return os.WriteFile(t.XMLPath, data, 0o600)
}

func (t Task) marshal() ([]byte, error) {
	kind := scheduleDaily
	if t.Kind == "weekly" {
		kind = scheduleWeekly
	}
	doc := buildTaskXML(t, kind)
	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	text := `<?xml version="1.0" encoding="UTF-16"?>` + "\r\n" + string(body)
	return utf16LE(text), nil
}

func utf16LE(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 2+len(units)*2)
	out[0], out[1] = 0xFF, 0xFE
	for i, u := range units {
		out[2+i*2] = byte(u)
		out[3+i*2] = byte(u >> 8)
	}
	return out
}

// Exists reports whether a task with this name is registered.
func Exists(ctx context.Context, name string) (bool, error) {
	cmd := exec.CommandContext(ctx, "schtasks", "/Query", "/TN", name)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// Install registers the tasks. It refuses to replace existing tasks unless
// force is set; dryRun only writes the XML files and shows the commands.
func Install(ctx context.Context, tasks []Task, force, dryRun bool, out io.Writer) error {
	for _, t := range tasks {
		if err := t.WriteXML(); err != nil {
			return err
		}
	}
	for _, t := range tasks {
		exists, err := Exists(ctx, t.Name)
		if err != nil {
			return fmt.Errorf("query task %s: %w", t.Name, err)
		}
		if exists && !force {
			return fmt.Errorf("task %s already exists; use --force to replace it", t.Name)
		}
		args := []string{"/Create", "/TN", t.Name, "/XML", t.XMLPath}
		if force {
			args = append(args, "/F")
		}
		if dryRun {
			fmt.Fprintf(out, "[dry-run] schtasks %s\n", strings.Join(args, " "))
			continue
		}
		cmd := exec.CommandContext(ctx, "schtasks", args...)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("create task %s: %w (%s)", t.Name, err, strings.TrimSpace(string(output)))
		}
		fmt.Fprintf(out, "installed  %s  next=%s\n", t.Name, t.NextRun.Format("2006-01-02 15:04"))
	}
	if dryRun {
		for _, t := range tasks {
			fmt.Fprintf(out, "xml        %s\n", t.XMLPath)
		}
	}
	return nil
}

// Uninstall deletes both tasks; missing tasks are ignored.
func Uninstall(ctx context.Context, names []string, dryRun bool, out io.Writer) error {
	for _, name := range names {
		exists, err := Exists(ctx, name)
		if err != nil {
			return err
		}
		if !exists {
			fmt.Fprintf(out, "missing    %s\n", name)
			continue
		}
		args := []string{"/Delete", "/TN", name, "/F"}
		if dryRun {
			fmt.Fprintf(out, "[dry-run] schtasks %s\n", strings.Join(args, " "))
			continue
		}
		cmd := exec.CommandContext(ctx, "schtasks", args...)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("delete task %s: %w (%s)", name, err, strings.TrimSpace(string(output)))
		}
		fmt.Fprintf(out, "removed    %s\n", name)
	}
	return nil
}

// ---------- Task Scheduler XML ----------

type taskXML struct {
	XMLName          xml.Name   `xml:"Task"`
	Version          string     `xml:"version,attr"`
	XMLNS            string     `xml:"xmlns,attr"`
	RegistrationInfo regInfo    `xml:"RegistrationInfo"`
	Triggers         triggers   `xml:"Triggers"`
	Principals       principals `xml:"Principals"`
	Settings         settings   `xml:"Settings"`
	Actions          actions    `xml:"Actions"`
}

type regInfo struct {
	Description string `xml:"Description"`
	URI         string `xml:"URI"`
}

type triggers struct {
	Calendar calendarTrigger `xml:"CalendarTrigger"`
}

type calendarTrigger struct {
	StartBoundary  string          `xml:"StartBoundary"`
	Enabled        bool            `xml:"Enabled"`
	ScheduleByDay  *scheduleByDay  `xml:"ScheduleByDay,omitempty"`
	ScheduleByWeek *scheduleByWeek `xml:"ScheduleByWeek,omitempty"`
}

type scheduleByDay struct {
	DaysInterval int `xml:"DaysInterval"`
}

type scheduleByWeek struct {
	DaysOfWeek    daysOfWeek `xml:"DaysOfWeek"`
	WeeksInterval int        `xml:"WeeksInterval"`
}

type daysOfWeek struct {
	Sunday    *empty `xml:"Sunday,omitempty"`
	Monday    *empty `xml:"Monday,omitempty"`
	Tuesday   *empty `xml:"Tuesday,omitempty"`
	Wednesday *empty `xml:"Wednesday,omitempty"`
	Thursday  *empty `xml:"Thursday,omitempty"`
	Friday    *empty `xml:"Friday,omitempty"`
	Saturday  *empty `xml:"Saturday,omitempty"`
}

type empty struct{}

type principals struct {
	Principal principal `xml:"Principal"`
}

type principal struct {
	ID        string `xml:"id,attr"`
	UserID    string `xml:"UserId"`
	LogonType string `xml:"LogonType"`
	RunLevel  string `xml:"RunLevel"`
}

// Field order follows the Task Scheduler XML schema; schtasks rejects
// out-of-sequence elements even when the values are valid.
type settings struct {
	MultipleInstancesPolicy    string       `xml:"MultipleInstancesPolicy"`
	DisallowStartIfOnBatteries bool         `xml:"DisallowStartIfOnBatteries"`
	StopIfGoingOnBatteries     bool         `xml:"StopIfGoingOnBatteries"`
	AllowHardTerminate         bool         `xml:"AllowHardTerminate"`
	StartWhenAvailable         bool         `xml:"StartWhenAvailable"`
	RunOnlyIfNetworkAvailable  bool         `xml:"RunOnlyIfNetworkAvailable"`
	IdleSettings               idleSettings `xml:"IdleSettings"`
	AllowStartOnDemand         bool         `xml:"AllowStartOnDemand"`
	Enabled                    bool         `xml:"Enabled"`
	Hidden                     bool         `xml:"Hidden"`
	RunOnlyIfIdle              bool         `xml:"RunOnlyIfIdle"`
	WakeToRun                  bool         `xml:"WakeToRun"`
	ExecutionTimeLimit         string       `xml:"ExecutionTimeLimit"`
	Priority                   int          `xml:"Priority"`
}

type idleSettings struct {
	StopOnIdleEnd bool `xml:"StopOnIdleEnd"`
	RestartOnIdle bool `xml:"RestartOnIdle"`
}

type actions struct {
	Context string     `xml:"Context,attr"`
	Exec    execAction `xml:"Exec"`
}

type execAction struct {
	Command          string `xml:"Command"`
	Arguments        string `xml:"Arguments"`
	WorkingDirectory string `xml:"WorkingDirectory"`
}

func buildTaskXML(t Task, kind scheduleKind) taskXML {
	user := os.Getenv("USERDOMAIN") + `\` + os.Getenv("USERNAME")
	if os.Getenv("USERDOMAIN") == "" {
		user = os.Getenv("USERNAME")
	}
	trigger := calendarTrigger{
		StartBoundary: t.NextRun.Format("2006-01-02T15:04:05"),
		Enabled:       true,
	}
	if kind == scheduleWeekly {
		trigger.ScheduleByWeek = &scheduleByWeek{
			DaysOfWeek:    dayOnly(t.NextRun.Weekday()),
			WeeksInterval: 1,
		}
	} else {
		trigger.ScheduleByDay = &scheduleByDay{DaysInterval: 1}
	}
	return taskXML{
		Version: "1.2",
		XMLNS:   "http://schemas.microsoft.com/windows/2004/02/mit/task",
		RegistrationInfo: regInfo{
			Description: fmt.Sprintf("FatTrace %s snapshot and maintenance", t.Kind),
			URI:         `\` + t.Name,
		},
		Triggers: triggers{Calendar: trigger},
		Principals: principals{Principal: principal{
			ID: "Author", UserID: user, LogonType: "InteractiveToken", RunLevel: "LeastPrivilege",
		}},
		Settings: settings{
			MultipleInstancesPolicy:    "IgnoreNew",
			DisallowStartIfOnBatteries: false,
			StopIfGoingOnBatteries:     true,
			AllowHardTerminate:         true,
			StartWhenAvailable:         true,
			RunOnlyIfNetworkAvailable:  false,
			IdleSettings:               idleSettings{StopOnIdleEnd: false, RestartOnIdle: false},
			AllowStartOnDemand:         true,
			Enabled:                    true,
			Hidden:                     true,
			RunOnlyIfIdle:              false,
			WakeToRun:                  false,
			ExecutionTimeLimit:         "PT6H",
			Priority:                   7,
		},
		Actions: actions{Context: "Author", Exec: execAction{
			Command:          t.Command,
			Arguments:        t.Args,
			WorkingDirectory: filepath.Dir(t.Command),
		}},
	}
}

func dayOnly(wd time.Weekday) daysOfWeek {
	var d daysOfWeek
	e := &empty{}
	switch wd {
	case time.Sunday:
		d.Sunday = e
	case time.Monday:
		d.Monday = e
	case time.Tuesday:
		d.Tuesday = e
	case time.Wednesday:
		d.Wednesday = e
	case time.Thursday:
		d.Thursday = e
	case time.Friday:
		d.Friday = e
	case time.Saturday:
		d.Saturday = e
	}
	return d
}
