//go:build windows

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Xeayy/FatTrace/internal/alerts"
	"github.com/Xeayy/FatTrace/internal/config"
	"github.com/Xeayy/FatTrace/internal/store"
)

func alertsCmd(args []string) {
	fs := flag.NewFlagSet("alerts", flag.ExitOnError)
	dbPath := fs.String("db", "", "database file")
	mute := fs.Duration("mute", 0, "mute all growth alerts for this long, e.g. 24h")
	unmute := fs.Bool("unmute", false, "clear active mutes")
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
	st, err := store.Open(path)
	if err != nil {
		fail(err)
	}
	defer st.Close()
	ctx := context.Background()

	if *mute > 0 && *unmute {
		fail(fmt.Errorf("-mute and -unmute are mutually exclusive"))
	}
	if *mute > 0 || *unmute {
		preview, err := alerts.Evaluate(ctx, st, cfg.Alerts, alerts.Options{Persist: false})
		if err != nil {
			fail(err)
		}
		keys := make([]string, 0, len(preview.Alerts))
		for _, a := range preview.Alerts {
			keys = append(keys, a.Key)
		}
		if len(keys) == 0 {
			fmt.Println("no growth alerts configured for any volume yet")
			return
		}
		if *unmute {
			err = alerts.ClearMutes(ctx, st, keys)
		} else {
			err = alerts.SetMute(ctx, st, keys, time.Now().Add(*mute))
		}
		if err != nil {
			fail(err)
		}
	}

	rep, err := alerts.Evaluate(ctx, st, cfg.Alerts, alerts.Options{Persist: true})
	if err != nil {
		fail(err)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fail(err)
		}
		return
	}
	printAlerts(rep)
}

func printAlerts(rep alerts.Report) {
	if len(rep.Alerts) == 0 {
		fmt.Println("alerts: disabled or no daily summaries yet (run fat daily)")
		return
	}
	fmt.Printf("%-14s  %-10s  %-8s  %-14s  %-7s  %-8s  %s\n",
		"root", "day", "volume", "net delta", "status", "hits", "threshold")
	for _, a := range rep.Alerts {
		status := "ok"
		switch {
		case a.Fired:
			status = "FIRED"
		case a.Muted:
			status = "muted"
		case a.Triggered:
			status = "active"
		}
		var parts []string
		if a.ThresholdBytes > 0 {
			parts = append(parts, formatBytes(a.ThresholdBytes))
		}
		if a.ThresholdPct > 0 {
			parts = append(parts, fmt.Sprintf("%.1f%%", a.ThresholdPct))
		}
		if a.ThresholdMultiple > 0 {
			parts = append(parts, fmt.Sprintf("%.1f×", a.ThresholdMultiple))
		}
		threshold := strings.Join(parts, " / ")
		if threshold == "" {
			threshold = "—"
		}
		fmt.Printf("%-14s  %-10s  %-8d  %-14s  %-7s  %-8d  %s\n",
			a.Root, a.Day, a.VolumeID, formatBytes(a.ValueBytes), status, a.Hits, threshold)
		if a.Triggered {
			fmt.Printf("  %s\n", a.Message)
			if a.MutedUntil > 0 {
				fmt.Printf("  muted until %s\n", formatMillis(a.MutedUntil))
			}
		}
	}
}
