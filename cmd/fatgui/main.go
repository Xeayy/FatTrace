//go:build windows

// Command fatgui is a GUI-subsystem launcher. Double-clicking it starts the
// FatTrace tray and dashboard without a console window. It delegates to the
// fat.exe sitting next to it; all tray/single-instance/server behavior stays
// in fat.exe.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"

	"fattrace/internal/tray"
)

func main() {
	exe, err := os.Executable()
	if err != nil {
		tray.ErrorBox("FatTrace", "无法定位启动器：\n"+err.Error())
		os.Exit(1)
	}
	fatPath := filepath.Join(filepath.Dir(exe), "fat.exe")
	if _, err := os.Stat(fatPath); err != nil {
		tray.ErrorBox("FatTrace", "找不到 fat.exe：\n"+fatPath+"\n\n请把 fatgui.exe 与 fat.exe 放在同一目录。")
		os.Exit(1)
	}

	cmd := exec.Command(fatPath, buildTrayArgs(os.Args[1:])...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
	if err := cmd.Start(); err != nil {
		tray.ErrorBox("FatTrace 启动失败", err.Error())
		os.Exit(1)
	}
	_ = cmd.Process.Release()
}

// buildTrayArgs always targets the tray subcommand and opens the dashboard
// unless the caller already passed an --open flag.
func buildTrayArgs(extra []string) []string {
	args := append([]string{"tray"}, extra...)
	if !hasOpenFlag(args) {
		args = append(args, "--open")
	}
	return args
}

func hasOpenFlag(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "--open") || strings.HasPrefix(a, "-open") {
			return true
		}
	}
	return false
}
