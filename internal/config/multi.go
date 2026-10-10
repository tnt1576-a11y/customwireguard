package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"os"
	"regexp"
)

// MultiServer keeps an independent transport credential and session for each
// native WireGuard peer. Empty Users is valid after revoking the last account.
type MultiServer struct {
	Mode     string       `json:"mode"`
	Listen   string       `json:"listen"`
	MaxPPS   int          `json:"max_pps"`
	Revision uint64       `json:"revision,omitempty"`
	Users    []ServerUser `json:"users"`
}
type ServerUser struct {
	Name   string `json:"name"`
	Config Config `json:"config"`
}

var userName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func LoadMulti(path string) (MultiServer, error) {
	result, _, err := LoadMultiSnapshot(path)
	return result, err
}

// The digest and configuration always describe the same bytes, including while
// management atomically replaces the file during a live reload.
func LoadMultiSnapshot(path string) (MultiServer, [32]byte, error) {
	var result MultiServer
	f, err := os.Open(path)
	if err != nil {
		return result, [32]byte{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(data) > 65536 {
		return result, [32]byte{}, errors.New("multi-user configuration unreadable or larger than 64 KiB")
	}
	digest := sha256.Sum256(data)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF {
		return MultiServer{}, digest, errors.New("invalid multi-user JSON configuration")
	}
	if err = result.Validate(); err != nil {
		return MultiServer{}, digest, err
	}
	return result, digest, nil
}
func (c MultiServer) Validate() error {
	if c.Mode != "server-multi" {
		return errors.New("multi-user mode must be server-multi")
	}
	if _, err := address(c.Listen, "listen"); err != nil {
		return err
	}
	if c.MaxPPS < 200 || c.MaxPPS > 12000 {
		return errors.New("aggregate max_pps must be 200..12000")
	}
	if len(c.Users) > 3 {
		return errors.New("this server supports at most three independent users")
	}
	names, keys := map[string]bool{}, map[string]bool{}
	for _, u := range c.Users {
		if !userName.MatchString(u.Name) || names[u.Name] {
			return errors.New("user names must be unique lowercase identifiers, at most 32 characters")
		}
		if err := u.Config.Validate("server"); err != nil {
			return err
		}
		if u.Config.Listen != c.Listen || u.Config.Transport != "exchange" {
			return errors.New("each user must use exchange transport and the shared listener")
		}
		if keys[u.Config.Key] {
			return errors.New("users must have independent outer credentials")
		}
		names[u.Name], keys[u.Config.Key] = true, true
	}
	ap, _ := netip.ParseAddrPort(c.Listen)
	if ap.Addr().IsMulticast() {
		return errors.New("listener cannot be multicast")
	}
	return nil
}
