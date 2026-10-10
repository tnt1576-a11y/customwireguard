package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateBundleAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private")
	if err := Initialize(path, "192.0.2.10:443", "balanced"); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"client", "server"} {
		if _, e := Load(filepath.Join(path, mode+".json"), mode); e != nil {
			t.Fatal(e)
		}
	}
	if e := Initialize(path, "192.0.2.10:443", "balanced"); e == nil {
		t.Fatal("overwrote keys")
	}
	c, _ := Load(filepath.Join(path, "client.json"), "client")
	c.Listen = "0.0.0.0:51821"
	if e := c.Validate("client"); e == nil {
		t.Fatal("public client listener allowed")
	}
	c.Listen = "127.0.0.1:51821"
	c.WireGuard = "192.0.2.2:51822"
	if e := c.Validate("client"); e == nil {
		t.Fatal("non-loopback backend allowed")
	}
	c.WireGuard = "127.0.0.1:51822"
	c.Key = base64.StdEncoding.EncodeToString(make([]byte, 32))
	if e := c.Validate("client"); e == nil {
		t.Fatal("zero secret allowed")
	}
	data, e := os.ReadFile(filepath.Join(path, "cwg-full.conf"))
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(data), "::/1, 8000::/1") || !strings.Contains(string(data), "MTU = 1280") {
		t.Fatal("missing full-mode IPv6 routes or MTU")
	}
}

func TestRejectUnknownAndTrailingJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	for _, body := range []string{`{"private_secret":"DO_NOT_ECHO","extra":1}`, `{} {}`, strings.Repeat(" ", 16385)} {
		os.WriteFile(path, []byte(body), 0600)
		_, err := Load(path, "client")
		if err == nil {
			t.Fatal("bad config accepted")
		}
		if strings.Contains(err.Error(), "DO_NOT_ECHO") {
			t.Fatal("error leaked value")
		}
	}
}

func TestExchangeBurstConfig(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bundle")
	if err := Initialize(dir, "192.0.2.10:443", "balanced"); err != nil {
		t.Fatal(err)
	}
	c, err := Load(filepath.Join(dir, "client.json"), "client")
	if err != nil {
		t.Fatal(err)
	}
	c.Transport = "exchange"
	c.ExchangeQueueMS = 60
	c.ExchangeBurstMS = 200
	if err := c.Validate("client"); err != nil {
		t.Fatal(err)
	}
	for _, values := range [][2]int{{0, 200}, {60, 59}, {60, 251}, {200, 60}, {60, -1}} {
		c.ExchangeQueueMS, c.ExchangeBurstMS = values[0], values[1]
		if c.Validate("client") == nil {
			t.Fatal("invalid burst accepted", values)
		}
	}
}

func TestRawPacingValidation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bundle")
	if err := Initialize(dir, "192.0.2.10:443", "balanced"); err != nil {
		t.Fatal(err)
	}
	c, err := Load(filepath.Join(dir, "client.json"), "client")
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]int{{0, 0}, {128, 0}, {1000, 150}, {100000, 1000}} {
		c.PaceKbps, c.PaceQueueMS = pair[0], pair[1]
		if err := c.Validate("client"); err != nil {
			t.Fatal(pair, err)
		}
	}
	for _, pair := range [][2]int{{-1, 0}, {1, 0}, {100001, 0}, {0, 150}, {1000, -1}, {1000, 9}, {1000, 1001}} {
		c.PaceKbps, c.PaceQueueMS = pair[0], pair[1]
		if c.Validate("client") == nil {
			t.Fatal("accepted invalid pacing", pair)
		}
	}
	c.PaceKbps, c.PaceQueueMS = 1000, 150
	c.Transport, c.TLSCert, c.H3Target = "h3", "server-cert.pem", "127.0.0.1:51820"
	if c.Validate("client") == nil {
		t.Fatal("accepted raw pacing on H3")
	}
}

func TestExchangeConfigValidation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bundle")
	if e := Initialize(dir, "192.0.2.10:443", "balanced"); e != nil {
		t.Fatal(e)
	}
	c, e := Load(filepath.Join(dir, "client.json"), "client")
	if e != nil {
		t.Fatal(e)
	}
	c.Transport = "exchange"
	for _, rate := range []int{0, 10, 100, 200} {
		c.ExchangePPS = rate
		if e = c.Validate("client"); e != nil {
			t.Fatal(e)
		}
	}
	for _, rate := range []int{-1, 1, 201} {
		c.ExchangePPS = rate
		if c.Validate("client") == nil {
			t.Fatal("bad exchange rate accepted")
		}
	}
	c.ExchangePPS = 100
	for _, rate := range []int{200, 6000, 12000} {
		c.ExchangeMaxPPS = rate
		c.ExchangeQueueMS = 60
		if e = c.Validate("client"); e != nil {
			t.Fatal(e)
		}
	}
	for _, rate := range []int{-1, 199, 12001} {
		c.ExchangeMaxPPS = rate
		if c.Validate("client") == nil {
			t.Fatal("invalid adaptive ceiling accepted")
		}
	}
	c.ExchangeMaxPPS = 6000
	for _, age := range []int{-1, 19, 501} {
		c.ExchangeQueueMS = age
		if c.Validate("client") == nil {
			t.Fatal("invalid queue age accepted")
		}
	}
	c.ExchangeMaxPPS = 0
	c.ExchangeQueueMS = 0
	c.PaceKbps = 1000
	if c.Validate("client") == nil {
		t.Fatal("mixed pacing accepted")
	}
	c.PaceKbps = 0
	c.Transport = "raw"
	if c.Validate("client") == nil {
		t.Fatal("exchange setting accepted in raw mode")
	}
}

func TestH3ConfigurationAndRelativePin(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bundle")
	if err := Initialize(dir, "192.0.2.10:443", "balanced"); err != nil {
		t.Fatal(err)
	}
	c, err := Load(filepath.Join(dir, "client.json"), "client")
	if err != nil {
		t.Fatal(err)
	}
	c.Transport, c.TLSCert, c.H3Target = "h3", "server-cert.pem", "127.0.0.1:51820"
	if err = c.Validate("client"); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Config){
		func(c *Config) { c.TLSKey = "server-key.pem" },
		func(c *Config) { c.TLSCert = "" },
		func(c *Config) { c.H3Target = "192.0.2.1:22" },
		func(c *Config) { c.H3Target = "localhost:51820" },
		func(c *Config) { c.Transport = "raw" },
	} {
		bad := c
		change(&bad)
		if bad.Validate("client") == nil {
			t.Fatal("invalid H3 config accepted")
		}
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "h3.json")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, "client")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TLSCert != filepath.Join(dir, "server-cert.pem") {
		t.Fatal("pin is not relative to config")
	}
	tlsDir := filepath.Join(dir, "identity")
	if err = InitializeTLS(tlsDir, "192.0.2.10:443"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(tlsDir, "server-key.pem"))
	if err = InitializeTLS(tlsDir, "192.0.2.10:443"); err == nil {
		t.Fatal("overwrote TLS identity")
	}
	after, _ := os.ReadFile(filepath.Join(tlsDir, "server-key.pem"))
	if !bytes.Equal(before, after) {
		t.Fatal("TLS identity changed")
	}
}
