//go:build windows

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"github.com/Xeayy/FatTrace/internal/server"
	"github.com/Xeayy/FatTrace/internal/store"
)

func serveCmd(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dbPath := fs.String("db", "", "database file")
	addr := fs.String("addr", "127.0.0.1:0", "listen address; loopback only")
	topN := fs.Int("top", 20, "ranking rows per diff API call")
	open := fs.Bool("open", false, "open the dashboard in the default browser")
	_ = fs.Parse(args)
	if fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: fat serve [flags]")
		os.Exit(2)
	}

	path, err := resolveDB(*dbPath)
	if err != nil {
		fail(err)
	}
	local, err := startLocalServer(path, *addr, *topN)
	if err != nil {
		fail(err)
	}
	defer local.Close()

	fmt.Printf("listening  %s/\n", local.base)
	fmt.Printf("open       %s\n", local.url)
	fmt.Println("read-only · token required · Ctrl+C to stop")
	if *open {
		if err := openBrowser(local.url); err != nil {
			fmt.Fprintf(os.Stderr, "warning: open browser: %v\n", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := local.srv.Serve(ctx, local.ln); err != nil {
		fail(err)
	}
}

// localServer owns the read-only store, listener and HTTP server used by both
// "fat serve" and the tray launcher.
type localServer struct {
	st   *store.Store
	ln   net.Listener
	srv  *server.Server
	base string
	url  string
}

func startLocalServer(dbPath, addr string, topN int) (*localServer, error) {
	migrateForServe(dbPath)
	st, err := store.OpenReader(dbPath)
	if err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			return nil, fmt.Errorf("no database at %s\nrun: fat db init", dbPath)
		case errors.Is(err, store.ErrNotInitialized):
			return nil, fmt.Errorf("%v\nrun: fat db init", err)
		}
		return nil, err
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		st.Close()
		return nil, err
	}
	tcp, ok := ln.Addr().(*net.TCPAddr)
	if !ok || !tcp.IP.IsLoopback() {
		ln.Close()
		st.Close()
		return nil, fmt.Errorf("refusing to listen on %q: only loopback addresses are allowed", addr)
	}

	srv, err := server.New(st, server.Options{TopN: topN})
	if err != nil {
		ln.Close()
		st.Close()
		return nil, err
	}

	host := tcp.IP.String()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	base := "http://" + host + ":" + strconv.Itoa(tcp.Port)
	return &localServer{
		st: st, ln: ln, srv: srv, base: base,
		url: base + "/#t=" + srv.Token(),
	}, nil
}

func (l *localServer) Close() {
	_ = l.ln.Close()
	_ = l.st.Close()
}

// migrateForServe upgrades an existing database before the read-only server
// opens it: readers never migrate, and /api/daily needs the current schema.
func migrateForServe(path string) {
	if _, err := os.Stat(path); err != nil {
		return
	}
	ctx := context.Background()
	r, err := store.OpenReader(path)
	if err != nil {
		return
	}
	v, err := r.SchemaVersion(ctx)
	r.Close()
	if err != nil || v == 0 || v >= store.CurrentSchemaVersion {
		return
	}
	w, err := store.Open(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not migrate schema to v%d: %v\n",
			store.CurrentSchemaVersion, err)
		return
	}
	w.Close()
}
