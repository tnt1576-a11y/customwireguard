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
	"path/filepath"
)

// One configuration and key pair serves one WireGuard peer. Use separate
// processes/ports for additional peers to keep association and revocation simple.
type Config struct {
	Mode            string `json:"mode"`
	Listen          string `json:"listen"`
	Endpoint        string `json:"endpoint,omitempty"`
	WireGuard       string `json:"wireguard"`
	Key             string `json:"key"`
	Profile         string `json:"profile"`
	Transport       string `json:"transport,omitempty"`
	TLSCert         string `json:"tls_cert,omitempty"`
	TLSKey          string `json:"tls_key,omitempty"`
	H3Target        string `json:"h3_target,omitempty"`
	PaceKbps        int    `json:"pace_kbps,omitempty"`
	PaceQueueMS     int    `json:"pace_queue_ms,omitempty"`
	ExchangePPS     int    `json:"exchange_pps,omitempty"`
	ExchangeMaxPPS  int    `json:"exchange_max_pps,omitempty"`
	ExchangeQueueMS int    `json:"exchange_queue_ms,omitempty"`
	ExchangeBurstMS int    `json:"exchange_burst_ms,omitempty"`
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
	for _, p := range []*string{&c.TLSCert, &c.TLSKey} {
		if *p != "" && !filepath.IsAbs(*p) {
			*p = filepath.Join(filepath.Dir(path), *p)
		}
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
	if c.Transport != "" && c.Transport != "raw" && c.Transport != "h3" && c.Transport != "exchange" {
		return errors.New("transport must be raw, exchange or h3")
	}
	if c.ExchangePPS != 0 && (c.Transport != "exchange" || c.ExchangePPS < 10 || c.ExchangePPS > 200) {
		return errors.New("exchange_pps requires exchange transport and must be 10..200")
	}
	if c.ExchangeMaxPPS != 0 && (c.Transport != "exchange" || mode != "client" || c.ExchangeMaxPPS < 200 || c.ExchangeMaxPPS > 12000 || c.ExchangeMaxPPS < c.ExchangePPS) {
		return errors.New("exchange_max_pps is client-only, requires exchange, and must be 200..12000")
	}
	if c.ExchangeQueueMS != 0 && (c.Transport != "exchange" || c.ExchangeQueueMS < 20 || c.ExchangeQueueMS > 500) {
		return errors.New("exchange_queue_ms requires exchange and must be 20..500")
	}
	if c.ExchangeBurstMS != 0 && (c.Transport != "exchange" || c.ExchangeQueueMS == 0 || c.ExchangeBurstMS < c.ExchangeQueueMS || c.ExchangeBurstMS > 250) {
		return errors.New("exchange_burst_ms requires exchange_queue_ms and must be between that deadline and 250")
	}
	if c.PaceKbps != 0 && (c.PaceKbps < 128 || c.PaceKbps > 100000) {
		return errors.New("pace_kbps must be zero or 128..100000")
	}
	if c.PaceQueueMS != 0 && (c.PaceQueueMS < 10 || c.PaceQueueMS > 1000) {
		return errors.New("pace_queue_ms must be zero or 10..1000")
	}
	if (c.PaceKbps == 0 && c.PaceQueueMS != 0) || ((c.Transport == "h3" || c.Transport == "exchange") && c.PaceKbps != 0) {
		return errors.New("pacing requires a nonzero raw UDP pace_kbps")
	}
	if c.Transport == "h3" {
		if c.TLSCert == "" || (mode == "server" && c.TLSKey == "") {
			return errors.New("h3 requires tls_cert, and server requires tls_key")
		}
		if mode == "client" {
			target, err := address(c.H3Target, "h3_target")
			if err != nil || !target.Addr().IsLoopback() || c.TLSKey != "" {
				return errors.New("h3 client requires a literal loopback h3_target and must not contain tls_key")
			}
		} else if c.H3Target != "" {
			return errors.New("h3 server uses wireguard as its only permitted target")
		}
	} else if c.TLSCert != "" || c.TLSKey != "" || c.H3Target != "" {
		return errors.New("TLS and h3_target settings require transport h3")
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
