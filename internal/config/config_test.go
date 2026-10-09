package config

import (
	"encoding/base64"
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
