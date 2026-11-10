//go:build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"fattrace/internal/config"
	"fattrace/internal/health"
	"fattrace/internal/schedule"
	"fattrace/internal/tray"
)

const (
	trayOpen = 1 + iota
	trayScan
	trayDataDir
	trayQuit
)

type trayApp struct {
	dbPath string
	local  *localServer
	tr     *tray.Tray
	cfg    config.Config

	quit     chan struct{}
	quitOnce sync.Once

	mu              sync.Mutex
	scanning        bool
	latest          string
	next            string
	healthLine      string
	healthLevel     health.Level
	healthReason    string
	tasksKnown      bool
	dailyInstalled  bool
	weeklyInstalled bool
	refreshMu       sync.Mutex
}

func trayCmd(args []string) {
	fs := flag.NewFlagSet("tray", flag.ExitOnError)
	dbPath := fs.String("db", "", "database file")
	addr := fs.String("addr", "127.0.0.1:0", "listen address; loopback only")
	topN := fs.Int("top", 20, "ranking rows per diff API call")
	open := fs.Bool("open", false, "open the dashboard on startup")
	_ = fs.Parse(args)
	if fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: fat tray [flags]")
		os.Exit(2)
	}

	path, err := resolveDB(*dbPath)
	if err != nil {
		fail(err)
	}
	if _, err := os.Stat(path); err != nil {
		msg := fmt.Sprintf("找不到数据库：\n%s\n\n请先在命令行运行：\nfat setup -roots \"D:\\\"", path)
		fmt.Fprintln(os.Stderr, msg)
		tray.ErrorBox("FatTrace", msg)
		os.Exit(1)
	}
	release, already, err := tray.AcquireSingleInstance(`Local\FatTraceTray`)
	if err != nil {
		fail(err)
	}
	defer release()
	if already {
		tray.ErrorBox("FatTrace", "FatTrace 已在运行，请查看通知区域。")
		return
	}

	local, err := startLocalServer(path, *addr, *topN)
	if err != nil {
		tray.ErrorBox("FatTrace 启动失败", err.Error())
		os.Exit(1)
	}
	cfg, err := config.Load(path)
	if err != nil {
		local.Close()
		tray.ErrorBox("FatTrace 启动失败", err.Error())
		os.Exit(1)
	}

	app := &trayApp{
		dbPath: path, local: local, cfg: cfg,
		quit:       make(chan struct{}),
		latest:     "最近快照: 暂无",
		next:       "下次: 读取中",
		healthLine: "健康: 读取中",
	}
	app.tr = tray.New(tray.Options{
		Title:     "FatTrace",
		Tooltip:   "FatTrace\n正在启动…",
		Menu:      app.menu,
		OnOpen:    app.openDashboard,
		OnCommand: app.command,
	})

	done := make(chan error, 1)
	go func() { done <- app.tr.Run() }()
	readyCtx, cancelReady := context.WithTimeout(context.Background(), 8*time.Second)
	err = app.tr.WaitReady(readyCtx)
	cancelReady()
	if err != nil {
		local.Close()
		tray.ErrorBox("FatTrace 启动失败", "无法创建托盘图标：\n"+err.Error())
		os.Exit(1)
	}
	tray.HideOwnConsole()

	app.checkTasks()
	app.refresh()
	app.mu.Lock()
	healthLevel, healthReason := app.healthLevel, app.healthReason
	app.mu.Unlock()
	if healthLevel == health.LevelDanger {
		app.tr.Notify("数据采集异常", truncateText(healthReason, 180))
	}
	if *open {
		app.openDashboard()
	} else if healthLevel != health.LevelDanger {
		app.tr.Notify("FatTrace 已启动", "双击托盘图标打开面板，右键查看更多操作。")
	}

	ctx, cancelServe := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- local.srv.Serve(ctx, local.ln) }()

	trayExited := false
	select {
	case <-app.quit:
	case err := <-done:
		trayExited = true
		if err != nil {
			tray.ErrorBox("FatTrace 托盘退出", err.Error())
		}
	case err := <-serveDone:
		if err != nil {
			tray.ErrorBox("FatTrace 服务退出", err.Error())
		}
	}

	app.shutdown()
	cancelServe()
	if !trayExited {
		app.tr.Stop()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
	local.Close()
}

func (a *trayApp) shutdown() {
	a.quitOnce.Do(func() { close(a.quit) })
}

func (a *trayApp) command(id int) {
	switch id {
	case trayOpen:
		a.openDashboard()
	case trayScan:
		a.scan()
	case trayDataDir:
		_ = exec.Command("explorer.exe", filepath.Dir(a.dbPath)).Start()
	case trayQuit:
		a.shutdown()
	}
}

func (a *trayApp) openDashboard() {
	if err := openBrowser(a.local.url); err != nil {
		a.tr.Notify("无法打开浏览器", truncateText(err.Error(), 180))
	}
}

func (a *trayApp) menu() []tray.Item {
	a.mu.Lock()
	scanning := a.scanning
	latest := a.latest
	next := a.next
	healthLine := a.healthLine
	a.mu.Unlock()

	scanLabel := "立即扫描"
	if scanning {
		scanLabel = "正在扫描…"
	}
	return []tray.Item{
		{ID: trayOpen, Label: "打开面板"},
		{ID: trayScan, Label: scanLabel, Disabled: scanning},
		{Separator: true},
		{Label: latest, Disabled: true},
		{Label: healthLine, Disabled: true},
		{Label: next, Disabled: true},
		{Separator: true},
		{ID: trayDataDir, Label: "打开数据目录"},
		{ID: trayQuit, Label: "退出"},
	}
}

func (a *trayApp) scan() {
	if !a.beginScan() {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		a.endScan()
		a.tr.Notify("扫描失败", truncateText(err.Error(), 180))
		return
	}
	cmd := exec.Command(exe, "maintain", "--kind", "daily", "--quiet", "--db", a.dbPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, runErr := cmd.CombinedOutput()
	a.endScan()
	if runErr != nil {
		a.tr.Notify("扫描失败", scanFailure(out, runErr))
		return
	}
	a.mu.Lock()
	latest := a.latest
	a.mu.Unlock()
	a.tr.Notify("扫描完成", strings.TrimPrefix(latest, "最近快照: "))
}

func (a *trayApp) beginScan() bool {
	a.mu.Lock()
	if a.scanning {
		a.mu.Unlock()
		return false
	}
	a.scanning = true
	a.mu.Unlock()
	a.tr.SetTooltip("FatTrace\n正在扫描…")
	return true
}

func (a *trayApp) endScan() {
	a.mu.Lock()
	a.scanning = false
	a.mu.Unlock()
	a.refresh()
}

func (a *trayApp) refresh() {
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()

	latest := "最近快照: 暂无"
	healthLine := "健康: 读取失败"
	level := health.LevelDanger
	reason := "无法读取数据库"
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	snaps, err := a.local.st.ListSnapshots(ctx, 1000)
	cancel()
	if err == nil {
		for _, sn := range snaps {
			if sn.Status != "complete" {
				continue
			}
			latest = fmt.Sprintf("最近快照: #%d · %s · %s",
				sn.ID, time.UnixMilli(sn.StartedAt).Format("01-02 15:04"),
				formatBytes(sn.UniqueAllocBytes))
			break
		}
		hr := health.Evaluate(snaps, a.cfg.Schedule.Roots,
			time.Duration(a.cfg.Alerts.StaleHours*float64(time.Hour)), time.Now())
		level = hr.Level
		reason = ""
		for _, hs := range hr.Roots {
			if hs.Level == level {
				reason = hs.Reason
				if reason == "" {
					reason = hs.Summary
				}
				break
			}
		}
		switch level {
		case health.LevelDanger:
			healthLine = "健康: 异常"
		case health.LevelWarn:
			healthLine = "健康: 注意"
		default:
			healthLine = "健康: 正常"
		}
		if level != health.LevelOK && reason != "" {
			healthLine += " · " + reason
		}
	}
	next := a.nextRun()

	a.mu.Lock()
	a.latest, a.next, a.healthLine = latest, next, healthLine
	a.healthLevel, a.healthReason = level, reason
	scanning := a.scanning
	a.mu.Unlock()
	if !scanning {
		a.tr.SetTooltip("FatTrace\n" +
			strings.TrimPrefix(latest, "最近快照: ") + "\n" +
			truncateText(healthLine, 90) + "\n" + next)
	}
}

func (a *trayApp) checkTasks() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	daily, errDaily := schedule.Query(ctx, schedule.DailyName)
	weekly, errWeekly := schedule.Query(ctx, schedule.WeeklyName)

	a.mu.Lock()
	a.tasksKnown = errDaily == nil && errWeekly == nil
	a.dailyInstalled = errDaily == nil && daily.Exists
	a.weeklyInstalled = errWeekly == nil && weekly.Exists
	a.mu.Unlock()
}

func (a *trayApp) nextRun() string {
	a.mu.Lock()
	known := a.tasksKnown
	daily := a.dailyInstalled
	weekly := a.weeklyInstalled
	a.mu.Unlock()

	if !known {
		return "下次: 任务状态未知"
	}
	if !daily && !weekly {
		return "下次: 计划任务未安装"
	}
	now := time.Now()
	var next time.Time
	var label string
	if daily {
		if t, err := config.NextDaily(now, a.cfg.Schedule.DailyAt); err == nil {
			next, label = t, "每日"
		}
	}
	if weekly {
		if t, err := config.NextWeekly(now, a.cfg.Schedule.WeeklyAt); err == nil && (next.IsZero() || t.Before(next)) {
			next, label = t, "每周"
		}
	}
	if next.IsZero() {
		return "下次: 时间配置无效"
	}
	return "下次: " + next.Format("01-02 15:04") + " (" + label + ")"
}

func scanFailure(out []byte, err error) string {
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "error: ") {
			return truncateText(strings.TrimPrefix(line, "error: "), 180)
		}
	}
	return truncateText(err.Error(), 180)
}

func truncateText(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
