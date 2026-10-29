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

	"fattrace/internal/server"
	"fattrace/internal/store"
)

func serveCmd(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dbPath := fs.String("db", "", "database file")
	addr := fs.String("addr", "127.0.0.1:0", "listen address; loopback only")
	topN := fs.Int("top", 20, "ranking rows per diff API call")
	_ = fs.Parse(args)
	if fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: fat serve [flags]")
		os.Exit(2)
	}

	path, err := resolveDB(*dbPath)
	if err != nil {
		fail(err)
	}
	migrateForServe(path)
	st, err := store.OpenReader(path)
	if err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			fmt.Fprintf(os.Stderr, "no database at %s\nrun: fat db init\n", path)
			os.Exit(1)
		case errors.Is(err, store.ErrNotInitialized):
			fmt.Fprintf(os.Stderr, "%v\nrun: fat db init\n", err)
			os.Exit(1)
		}
		fail(err)
	}
	defer st.Close()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fail(err)
	}
	tcp, ok := ln.Addr().(*net.TCPAddr)
	if !ok || !tcp.IP.IsLoopback() {
		ln.Close()
		fail(fmt.Errorf("refusing to listen on %q: only loopback addresses are allowed", *addr))
	}

	srv, err := server.New(st, server.Options{TopN: *topN})
	if err != nil {
		ln.Close()
		fail(err)
	}

	host := tcp.IP.String()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	base := "http://" + host + ":" + strconv.Itoa(tcp.Port)
	fmt.Printf("listening  %s/\n", base)
	fmt.Printf("open       %s/#t=%s\n", base, srv.Token())
	fmt.Println("read-only · token required · Ctrl+C to stop")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := srv.Serve(ctx, ln); err != nil {
		fail(err)
	}
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
