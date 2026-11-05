//go:build windows

package main

import (
	"fmt"
	"net/url"
	"os/exec"
)

// openBrowser hands a validated loopback URL to the default browser. It uses
// rundll32 instead of "cmd /c start" so URL characters are never re-parsed by
// a shell.
func openBrowser(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("invalid URL scheme %q", u.Scheme)
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1":
	default:
		return fmt.Errorf("refusing to open non-loopback URL %q", u.Hostname())
	}
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL).Start()
}
