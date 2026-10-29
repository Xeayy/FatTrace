package schedule

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func testOptions(t *testing.T) Options {
	t.Helper()
	dir := t.TempDir()
	return Options{
		Exe:      filepath.Join(dir, "fat.exe"),
		DBPath:   filepath.Join(dir, "fattrace.db"),
		Roots:    []string{`D:\`},
		DailyAt:  "03:00",
		WeeklyAt: "Sun 04:00",
		ACOnly:   true,
		Now:      time.Date(2026, 10, 29, 10, 0, 0, 0, time.Local),
	}
}

func decodeUTF16LE(t *testing.T, b []byte) string {
	t.Helper()
	if len(b) < 2 || b[0] != 0xFF || b[1] != 0xFE {
		t.Fatalf("missing UTF-16LE BOM: % x", b[:min(len(b), 4)])
	}
	units := make([]uint16, 0, (len(b)-2)/2)
	for i := 2; i+1 < len(b); i += 2 {
		units = append(units, uint16(b[i])|uint16(b[i+1])<<8)
	}
	return string(utf16.Decode(units))
}

func TestBuildRequiresRootsAndDB(t *testing.T) {
	opts := testOptions(t)
	opts.Roots = nil
	if _, err := Build(opts); err == nil {
		t.Fatal("Build accepted an empty root list")
	}
	opts = testOptions(t)
	opts.DBPath = ""
	if _, err := Build(opts); err == nil {
		t.Fatal("Build accepted an empty database path")
	}
}

func TestBuildTwoTasks(t *testing.T) {
	opts := testOptions(t)
	tasks, err := Build(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("tasks = %d, want 2", len(tasks))
	}
	daily, weekly := tasks[0], tasks[1]
	if daily.Name != DailyName || weekly.Name != WeeklyName || daily.Kind != "daily" || weekly.Kind != "weekly" {
		t.Fatalf("tasks = %+v", tasks)
	}
	if daily.XMLPath != filepath.Join(filepath.Dir(opts.DBPath), "tasks", DailyName+".xml") {
		t.Fatalf("xml path = %q", daily.XMLPath)
	}
	if !strings.Contains(daily.Args, `maintain --kind daily --quiet --db "`) ||
		!strings.Contains(daily.Args, opts.DBPath) {
		t.Fatalf("daily args = %q", daily.Args)
	}
	if len(daily.Args) > 0 && strings.Contains(daily.Args, "%") {
		t.Fatalf("args still contain a format verb: %q", daily.Args)
	}
	if daily.NextRun.Hour() != 3 || daily.NextRun.Minute() != 0 {
		t.Fatalf("daily next run = %v", daily.NextRun)
	}
	if weekly.NextRun.Weekday() != time.Sunday || weekly.NextRun.Hour() != 4 {
		t.Fatalf("weekly next run = %v", weekly.NextRun)
	}
	if daily.Command != opts.Exe {
		t.Fatalf("command = %q", daily.Command)
	}
}

func TestMarshalDailyXML(t *testing.T) {
	opts := testOptions(t)
	tasks, err := Build(opts)
	if err != nil {
		t.Fatal(err)
	}
	data, err := tasks[0].marshal()
	if err != nil {
		t.Fatal(err)
	}
	text := decodeUTF16LE(t, data)
	if !strings.HasPrefix(text, `<?xml version="1.0" encoding="UTF-16"?>`) {
		t.Fatalf("xml prefix = %q", text[:min(len(text), 60)])
	}
	if !strings.Contains(text, `<Task version="1.2"`) ||
		!strings.Contains(text, "http://schemas.microsoft.com/windows/2004/02/mit/task") {
		t.Fatal("missing task version or namespace")
	}
	if !strings.Contains(text, "<ScheduleByDay>") || !strings.Contains(text, "<DaysInterval>1</DaysInterval>") {
		t.Fatal("missing daily trigger")
	}
	if strings.Contains(text, "ScheduleByWeek") {
		t.Fatal("daily task has a weekly trigger")
	}
	if !strings.Contains(text, "<LogonType>InteractiveToken</LogonType>") ||
		!strings.Contains(text, "<RunLevel>LeastPrivilege</RunLevel>") {
		t.Fatal("missing non-elevated principal")
	}
	if !strings.Contains(text, "<StartWhenAvailable>true</StartWhenAvailable>") ||
		!strings.Contains(text, "<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>") {
		t.Fatal("missing settings")
	}
	if !strings.Contains(text, "<DisallowStartIfOnBatteries>true</DisallowStartIfOnBatteries>") ||
		!strings.Contains(text, "<StopIfGoingOnBatteries>true</StopIfGoingOnBatteries>") {
		t.Fatal("AC-only default did not reach the XML")
	}
	settings := text[strings.Index(text, "<Settings>"):strings.Index(text, "</Settings>")]
	order := []string{
		"<MultipleInstancesPolicy>", "<DisallowStartIfOnBatteries>", "<StopIfGoingOnBatteries>",
		"<AllowHardTerminate>", "<StartWhenAvailable>", "<RunOnlyIfNetworkAvailable>",
		"<IdleSettings>", "<AllowStartOnDemand>", "<Enabled>", "<Hidden>", "<RunOnlyIfIdle>",
		"<WakeToRun>", "<ExecutionTimeLimit>", "<Priority>",
	}
	prev := -1
	for _, tag := range order {
		i := strings.Index(settings, tag)
		if i < 0 {
			t.Fatalf("settings missing %s", tag)
		}
		if i < prev {
			t.Fatalf("settings element %s is out of schema order", tag)
		}
		prev = i
	}
	if !strings.Contains(text, "<ExecutionTimeLimit>PT6H</ExecutionTimeLimit>") {
		t.Fatal("missing execution time limit")
	}
}

func TestMarshalWeeklyXML(t *testing.T) {
	opts := testOptions(t)
	tasks, err := Build(opts)
	if err != nil {
		t.Fatal(err)
	}
	data, err := tasks[1].marshal()
	if err != nil {
		t.Fatal(err)
	}
	text := decodeUTF16LE(t, data)
	if !strings.Contains(text, "<ScheduleByWeek>") || !strings.Contains(text, "<Sunday>") {
		t.Fatal("missing weekly Sunday trigger")
	}
	if !strings.Contains(text, "<WeeksInterval>1</WeeksInterval>") {
		t.Fatal("missing weekly interval")
	}
	if strings.Contains(text, "<ScheduleByDay>") {
		t.Fatal("weekly task has a daily trigger")
	}
	week := text[strings.Index(text, "<ScheduleByWeek>"):strings.Index(text, "</ScheduleByWeek>")]
	if strings.Contains(week, "<Monday>") {
		t.Fatalf("weekly trigger lists an extra weekday: %s", week)
	}
}

func TestACOnlyDisabled(t *testing.T) {
	opts := testOptions(t)
	opts.ACOnly = false
	tasks, err := Build(opts)
	if err != nil {
		t.Fatal(err)
	}
	data, err := tasks[0].marshal()
	if err != nil {
		t.Fatal(err)
	}
	text := decodeUTF16LE(t, data)
	if !strings.Contains(text, "<DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>") ||
		!strings.Contains(text, "<StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>") {
		t.Fatal("ac_only=false did not reach the XML")
	}
}

const registeredSample = `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Triggers><CalendarTrigger><StartBoundary>2026-10-30T03:00:00</StartBoundary></CalendarTrigger></Triggers>
  <Principals><Principal id="Author"><UserId>PC\alice</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>
  <Settings>
    <DisallowStartIfOnBatteries>true</DisallowStartIfOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <Enabled>true</Enabled>
    <Hidden>true</Hidden>
  </Settings>
  <Actions Context="Author"><Exec><Command>C:\Tools\fat.exe</Command><Arguments>maintain --kind daily</Arguments></Exec></Actions>
</Task>`

func TestParseRegisteredASCII(t *testing.T) {
	r, err := parseRegistered([]byte(registeredSample))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Enabled || !r.StartWhenAvailable || !r.DisallowStartIfOnBatteries || !r.Hidden {
		t.Fatalf("settings = %+v", r)
	}
	if r.RunLevel != "LeastPrivilege" || r.LogonType != "InteractiveToken" {
		t.Fatalf("principal = %+v", r)
	}
	if r.StartBoundary != "2026-10-30T03:00:00" {
		t.Fatalf("start boundary = %q", r.StartBoundary)
	}
	if r.Command != `C:\Tools\fat.exe` || r.Arguments != "maintain --kind daily" {
		t.Fatalf("action = %q %q", r.Command, r.Arguments)
	}
}

func TestParseRegisteredUTF16(t *testing.T) {
	r, err := parseRegistered(utf16LE(registeredSample))
	if err != nil {
		t.Fatal(err)
	}
	if r.Command != `C:\Tools\fat.exe` || !r.DisallowStartIfOnBatteries {
		t.Fatalf("utf16 parse = %+v", r)
	}
}

func TestParseRegisteredDefaults(t *testing.T) {
	xml := `<Task><Settings><Enabled>false</Enabled></Settings></Task>`
	r, err := parseRegistered([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	if r.Enabled {
		t.Fatal("explicit Enabled=false was ignored")
	}
	if r.RunLevel != "LeastPrivilege" || r.LogonType != "InteractiveToken" {
		t.Fatalf("defaults = %+v", r)
	}
}

func TestWriteXMLFile(t *testing.T) {
	opts := testOptions(t)
	tasks, err := Build(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks[0].WriteXML(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(tasks[0].XMLPath)
	if err != nil {
		t.Fatal(err)
	}
	text := decodeUTF16LE(t, raw)
	if !strings.Contains(text, "maintain --kind daily --quiet --db") || !strings.Contains(text, opts.DBPath) {
		t.Fatalf("xml does not contain the scheduled arguments: %q", tasks[0].Args)
	}
}

func TestExistsMissingTask(t *testing.T) {
	if os.Getenv("USERNAME") == "" {
		t.Skip("schtasks requires a Windows user session")
	}
	ok, err := Exists(context.Background(), "FatTrace-Test-DefinitelyMissing-Task")
	if err != nil {
		t.Fatalf("query missing task: %v", err)
	}
	if ok {
		t.Fatal("a made-up task exists")
	}
}
