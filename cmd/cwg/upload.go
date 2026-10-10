package main

import (
	"context"
	"customwireguard/internal/diagnostic"
	"encoding/json"
	"flag"
	"os"
	"os/signal"
	"syscall"
)

func smoothUpload(args []string) error {
	f := flag.NewFlagSet("smooth-upload", flag.ContinueOnError)
	endpoint := f.String("endpoint", "10.77.0.1:18081", "tunnel or loopback diagnostic endpoint")
	count := f.Int64("bytes", 31250000, "synthetic payload bytes, 1..67108864")
	rate := f.Int64("bps", 3125000, "payload bytes/second, 1250000/3125000/6250000")
	id := f.String("id", "smooth", "diagnostic identifier, letters/digits/hyphen")
	if err := f.Parse(args); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	result, err := diagnostic.Upload(ctx, *endpoint, *count, *rate, *id)
	if result != nil {
		json.NewEncoder(os.Stdout).Encode(result)
	}
	return err
}
