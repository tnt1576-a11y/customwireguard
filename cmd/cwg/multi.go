package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"customwireguard/internal/config"
	"customwireguard/internal/transport"
)

func runMulti(args []string, check bool) error {
	flags := flag.NewFlagSet("server-multi", flag.ContinueOnError)
	path := flags.String("config", "", "private multi-user configuration path")
	status := flags.String("status-file", "", "private runtime status JSON file (optional)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || *path == "" {
		return errors.New("multi-user command requires -config and no positional arguments")
	}
	cfg, initialDigest, err := config.LoadMultiSnapshot(*path)
	if err != nil {
		return err
	}
	if check {
		fmt.Printf("Valid multi-user server configuration; %d users.\n", len(cfg.Users))
		return nil
	}
	if *status != "" && !filepath.IsAbs(*status) {
		return errors.New("status-file must be an absolute runtime path")
	}
	address, err := net.ResolveUDPAddr("udp", cfg.Listen)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", address)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err = conn.SetReadBuffer(2 << 20); err != nil {
		return err
	}
	if err = conn.SetWriteBuffer(2 << 20); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	updates := make(chan config.MultiServer, 1)
	stats := new(transport.MultiStats)
	workers.Add(2)
	go func() { defer workers.Done(); watchMulti(ctx, *path, cfg.Listen, initialDigest, updates) }()
	go func() { defer workers.Done(); reportMulti(ctx, stats, *status) }()
	fmt.Fprintf(os.Stderr, "cwg %s multi-user server started; at most three independent users, aggregate ceiling %d requests/s\n", version, cfg.MaxPPS)
	return transport.RunMultiServer(ctx, conn, cfg, updates, stats)
}

func watchMulti(ctx context.Context, path, listener string, last [32]byte, updates chan<- config.MultiServer) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			next, digest, err := config.LoadMultiSnapshot(path)
			if digest == last {
				continue
			}
			if err != nil || next.Listen != listener {
				fmt.Fprintln(os.Stderr, "multi-user reload refused; current sessions retained")
				last = digest
				continue
			}
			select {
			case updates <- next:
				last = digest
			case <-ctx.Done():
				return
			}
		}
	}
}
func writeMultiStatus(path string, snapshot map[string]any) error {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".cwg-status-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
func reportMulti(ctx context.Context, stats *transport.MultiStats, path string) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var fileError bool
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snapshot := stats.Snapshot()
			snapshot["utc_unix_ms"] = time.Now().UnixMilli()
			if path != "" {
				if err := writeMultiStatus(path, snapshot); err != nil {
					if !fileError {
						fmt.Fprintln(os.Stderr, "could not update multi-user runtime status")
					}
					fileError = true
				} else {
					fileError = false
				}
			}
			json.NewEncoder(os.Stderr).Encode(snapshot)
		}
	}
}
