package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
)

// One configuration and key pair serves one WireGuard peer. Use separate
// processes/ports for additional peers to keep association and revocation simple.
type Config struct {
	Mode      string `json:"mode"`
	Listen    string `json:"listen"`
	Endpoint  string `json:"endpoint,omitempty"`
	WireGuard string `json:"wireguard"`
	Key       string `json:"key"`
	Profile   string `json:"profile"`
}

func Load(path string, mode string) (Config, error) {
	var c Config
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(data) > 16384 {
		return c, errors.New("configuration unreadable or larger than 16 KiB")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return Config{}, errors.New("invalid JSON configuration or unknown field")
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return Config{}, errors.New("configuration contains trailing data")
	}
	if mode == "" {
		mode = c.Mode
	}
	if err = c.Validate(mode); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c Config) Secret() ([]byte, error) {
	k, err := base64.StdEncoding.Strict().DecodeString(c.Key)
	if err != nil || len(k) != 32 {
		return nil, errors.New("key must encode exactly 32 random bytes in standard base64")
	}
	var nonzero byte
	for _, b := range k {
		nonzero |= b
	}
	if nonzero == 0 {
		return nil, errors.New("all-zero key is forbidden")
	}
	return k, nil
}

func (c Config) Validate(mode string) error {
	if c.Mode != mode || (mode != "client" && mode != "server") {
		return errors.New("configuration mode does not match command")
	}
	if _, err := c.Secret(); err != nil {
		return err
	}
	if c.Profile != "balanced" && c.Profile != "minimal" {
		return errors.New("profile must be balanced or minimal")
	}
	listen, err := address(c.Listen, "listen")
	if err != nil {
		return err
	}
	wg, err := address(c.WireGuard, "wireguard")
	if err != nil {
		return err
	}
	if !wg.Addr().IsLoopback() {
		return errors.New("wireguard target must be a literal loopback address")
	}
	if mode == "server" && listen.Port() == wg.Port() && (listen.Addr().IsUnspecified() || listen.Addr() == wg.Addr()) {
		return errors.New("server outer listener must not overlap the underlying WireGuard port")
	}
	if mode == "client" {
		if !listen.Addr().IsLoopback() {
			return errors.New("client listen address must be loopback")
		}
		if listen.Addr().Is4() != wg.Addr().Is4() || listen == wg {
			return errors.New("local WireGuard addresses must use the same IP family and different ports")
		}
		endpoint, err := address(c.Endpoint, "endpoint")
		if err != nil {
			return err
		}
		if endpoint.Addr().IsUnspecified() || endpoint.Addr().IsMulticast() {
			return errors.New("invalid server endpoint")
		}
	} else if c.Endpoint != "" {
		return errors.New("server does not accept an endpoint setting")
	}
	return nil
}

func address(s, name string) (netip.AddrPort, error) {
	ap, err := netip.ParseAddrPort(s)
	if err != nil || ap.Port() == 0 || ap.Addr().Is4In6() || ap.Addr().Zone() != "" {
		return netip.AddrPort{}, fmt.Errorf("%s must be a literal IP:port with nonzero port (IPv6 in brackets)", name)
	}
	return ap, nil
}
