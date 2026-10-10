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
	"strconv"
	"strings"
	"syscall"
	"time"

	"customwireguard/internal/config"
	"customwireguard/internal/transport"
)

var version = "0.5.3-dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "cwg:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	command := args[0]
	if command == "smooth-upload" {
		return smoothUpload(args[1:])
	}
	if command == "version" {
		fmt.Println("cwg", version)
		return nil
	}
	if command == "help" || command == "--help" || command == "-h" {
		usage()
		return nil
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	path := f.String("config", "", "private JSON configuration path")
	out := f.String("out", "", "new private output directory (init only)")
	endpoint := f.String("endpoint", "", "literal server IPv4:port (init only)")
	profile := f.String("profile", "balanced", "balanced or minimal (init only)")
	count := f.Int("count", 5, "authenticated UDP probes (probe only, 1..100)")
	probeSizes := f.String("sizes", "", "comma-separated UDP payload sizes (probe only, 256..1440, at most 32)")
	tracePath := f.String("trace", "", "new private metadata trace (client/server, or Windows H3 probe; no packet bytes)")
	traceSample := f.Int("trace-sample", 1, "deterministic metadata hash sample, 1..1024 (client/server with -trace)")
	patternPath := f.String("pattern", "", "client metadata trace to reproduce (replay-probe only)")
	replayStyle := f.String("style", "recorded", "recorded or paced (replay-probe only)")
	probeRate := f.Int("probe-rate-limit", 20, "authenticated diagnostic reply limit, 20..200/s (server only)")
	interfaceIndex := f.Int("interface-index", 0, "Windows outer IPv4 interface index; 0 uses routing table (client or H3 probe)")
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected positional argument")
	}
	if *traceSample < 1 || *traceSample > 1024 || (*traceSample != 1 && (*tracePath == "" || (command != "client" && command != "server"))) {
		return errors.New("-trace-sample must be 1..1024 with a client/server -trace")
	}
	if *interfaceIndex < 0 || *interfaceIndex > 0xffffff || (*interfaceIndex != 0 && command != "client" && command != "probe") {
		return errors.New("-interface-index must be 0..16777215 and is for client or H3 probe")
	}
	if *probeSizes != "" && command != "probe" {
		return errors.New("-sizes is only valid with probe")
	}
	if *tracePath != "" && command != "client" && command != "server" && command != "probe" {
		return errors.New("-trace is only valid with client, server or Windows H3 probe")
	}
	if *probeRate < 20 || *probeRate > 200 || (*probeRate != 20 && command != "server") {
		return errors.New("-probe-rate-limit must be 20..200 and is server-only")
	}
	if (*patternPath != "" || *replayStyle != "recorded") && command != "replay-probe" {
		return errors.New("-pattern and -style are only valid with replay-probe")
	}
	if command == "init" {
		if *out == "" || *endpoint == "" {
			return errors.New("init requires -out and -endpoint")
		}
		if err := config.Initialize(*out, *endpoint, *profile); err != nil {
			return err
		}
		fmt.Println("Private client/server bundle created. No keys printed. Read PRIVATE-README.txt.")
		return nil
	}
	if command == "tls-init" {
		if *out == "" || *endpoint == "" {
			return errors.New("tls-init requires -out and -endpoint")
		}
		if err := config.InitializeTLS(*out, *endpoint); err != nil {
			return err
		}
		fmt.Println("Private TLS identity created. Copy only server-cert.pem to the client; keep server-key.pem on the server.")
		return nil
	}
	if command != "client" && command != "server" && command != "probe" && command != "session-probe" && command != "replay-probe" && command != "check" {
		return errors.New("unknown command; use cwg help")
	}
	if *path == "" {
		return errors.New("-config is required")
	}
	mode := command
	if command == "probe" || command == "session-probe" || command == "replay-probe" {
		mode = "client"
	}
	if command == "check" {
		cfg, err := config.Load(*path, "")
		if err != nil {
			return err
		}
		fmt.Println("Valid", cfg.Mode, "configuration.")
		return nil
	}
	cfg, err := config.Load(*path, mode)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if cfg.Transport == "exchange" && (command == "session-probe" || command == "replay-probe" || *probeRate != 20) {
		return errors.New("raw session/replay diagnostics are not supported by exchange transport")
	}
	if cfg.Transport == "h3" {
		if command == "session-probe" || command == "replay-probe" || *probeSizes != "" || *probeRate != 20 {
			return errors.New("raw UDP diagnostic options are not supported by HTTP/3")
		}
		if command == "probe" {
			var trace *transport.PacketTrace
			if *tracePath != "" {
				trace, err = transport.OpenPacketTrace(*tracePath)
				if err != nil {
					return err
				}
				defer func() {
					if err := trace.Close(); err != nil {
						fmt.Fprintln(os.Stderr, "packet trace error:", err)
					}
				}()
			}
			result, err := transport.ProbeH3(ctx, cfg, *count, transport.H3ProbeOptions{InterfaceIndex: *interfaceIndex, Trace: trace})
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(result)
		}
	}
	if command == "replay-probe" {
		if *patternPath == "" {
			return errors.New("replay-probe requires -pattern")
		}
		points, err := transport.LoadReplayPattern(*patternPath, cfg.Endpoint)
		if err != nil {
			return err
		}
		result, err := transport.ReplayProbeEndpoint(ctx, cfg, points, *replayStyle)
		json.NewEncoder(os.Stdout).Encode(result)
		return err
	}
	if command == "session-probe" {
		result, err := transport.SessionProbeEndpoint(ctx, cfg)
		json.NewEncoder(os.Stdout).Encode(result)
		return err
	}
	if command == "probe" {
		if *tracePath != "" || *interfaceIndex != 0 {
			return errors.New("raw probe does not support -trace or -interface-index")
		}
		sizes, e := parseProbeSizes(*probeSizes)
		if e != nil {
			return e
		}
		result, e := transport.ProbeEndpoint(ctx, cfg, *count, sizes...)
		if e != nil {
			return e
		}
		json.NewEncoder(os.Stdout).Encode(result)
		if result.Received == 0 {
			return errors.New("no authenticated replies; inspect server reachability, key, clock, and filtering")
		}
		return nil
	}
	listen, err := net.ResolveUDPAddr("udp", cfg.Listen)
	if err != nil {
		return err
	}
	local, err := net.ListenUDP("udp", listen)
	if err != nil {
		return err
	}
	defer local.Close()
	local.SetReadBuffer(2 << 20)
	local.SetWriteBuffer(2 << 20)
	stats := new(transport.Stats)
	if *tracePath != "" {
		stats.Trace, err = transport.OpenPacketTraceSampled(*tracePath, *traceSample)
		if err != nil {
			return err
		}
		defer func() {
			if err := stats.Trace.Close(); err != nil {
				fmt.Fprintln(os.Stderr, "packet trace error:", err)
			}
		}()
		fmt.Fprintf(os.Stderr, "Packet metadata trace enabled; maximum 100000 records; deterministic sample 1/%d; no packet bytes or keys.\n", *traceSample)
	}
	reportInterval := 30 * time.Second
	if cfg.Transport == "exchange" && (cfg.ExchangeMaxPPS > 0 || cfg.ExchangeQueueMS > 0) {
		reportInterval = 5 * time.Second
	}
	if cfg.ExchangeBurstMS > 0 {
		reportInterval = time.Second
	}
	go report(ctx, stats, reportInterval)
	fmt.Fprintln(os.Stderr, "cwg", version, command, "started; experimental transport; one WireGuard peer per instance")
	if cfg.Transport == "h3" {
		if command == "server" {
			return transport.RunH3Server(ctx, local, cfg, stats)
		}
		return transport.RunH3Client(ctx, local, cfg, stats, *interfaceIndex)
	}
	if command == "server" {
		return transport.RunServer(ctx, local, cfg, stats, *probeRate)
	}
	remote, err := transport.DialEndpoint(ctx, cfg.Endpoint, *interfaceIndex)
	if err != nil {
		return err
	}
	if *interfaceIndex != 0 {
		fmt.Fprintln(os.Stderr, "Outer UDP socket bound to Windows interface", *interfaceIndex, "before connect; local", remote.LocalAddr())
	}
	remote.SetReadBuffer(2 << 20)
	remote.SetWriteBuffer(2 << 20)
	return transport.RunClient(ctx, local, remote, cfg, stats)
}

func parseProbeSizes(value string) ([]int, error) {
	if value == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	if len(parts) > 32 {
		return nil, errors.New("at most 32 probe sizes are supported")
	}
	sizes := make([]int, len(parts))
	for i, part := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 256 || n > 1440 {
			return nil, errors.New("probe sizes must be comma-separated integers in 256..1440")
		}
		sizes[i] = n
	}
	return sizes, nil
}

func report(ctx context.Context, stats *transport.Stats, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snapshot := stats.Snapshot()
			snapshot["utc_unix_ms"] = uint64(time.Now().UnixMilli())
			json.NewEncoder(os.Stderr).Encode(snapshot)
		}
	}
}

func usage() {
	fmt.Println(`CWG - experimental authenticated UDP transport for WireGuard

  cwg init -endpoint SERVER_IP:PORT -out NEW_PRIVATE_DIRECTORY
  cwg tls-init -endpoint SERVER_IP:PORT -out NEW_PRIVATE_TLS_DIRECTORY
  cwg check -config FILE.json
  cwg server -config server.json
  cwg client -config client.json
  cwg smooth-upload -endpoint TUNNEL_IP:PORT -bytes 31250000 -bps 3125000
  cwg client -config client.json -trace NEW_PRIVATE_FILE.jsonl
  cwg client -config client.json -interface-index WINDOWS_PHYSICAL_IFINDEX
  cwg probe -config client.json -count 5
  cwg probe -config H3_CLIENT.json -count 2 -trace NEW_FILE.jsonl -interface-index WINDOWS_PHYSICAL_IFINDEX
  cwg probe -config client.json -count 16 -sizes 256,257,258,259
  cwg session-probe -config client.json
  cwg replay-probe -config client.json -pattern packets-full.jsonl -style recorded
  cwg server -config server.json -probe-rate-limit 200
  cwg version

session-probe sends 1/5/10/20 authenticated pings per second without WireGuard.
Stop the normal client first: this takes ownership of the one server session.
replay-probe requires a server explicitly allowing 200 diagnostic replies/s.
It reproduces lengths/timing using fresh encrypted probes, never captured bytes.

Keep standard WireGuard installed at both ends. Start with the split-tunnel
test configuration. No routing or firewall changes are made by this executable.
Changing packet signatures does not guarantee DPI evasion or prevent IP blocking.`)
}
