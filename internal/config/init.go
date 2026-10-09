package config

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
)

// Initialize writes secrets only to a newly created directory. Existing output
// is never overwritten. The caller must restrict directory ACLs on Windows.
func Initialize(dir, endpoint, profile string) error {
	ep, err := address(endpoint, "endpoint")
	if err != nil {
		return err
	}
	if !ep.Addr().Is4() || ep.Addr().IsUnspecified() || ep.Addr().IsMulticast() {
		return errors.New("bundle setup currently requires a literal server IPv4 address")
	}
	if ep.Port() == 51820 {
		return errors.New("outer port 51820 is reserved for the private WireGuard backend; choose another port")
	}
	if profile != "balanced" && profile != "minimal" {
		return errors.New("profile must be balanced or minimal")
	}
	if err = os.Mkdir(dir, 0700); err != nil {
		return fmt.Errorf("create new private directory (must not exist): %w", err)
	}
	encode := func(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
	secret := make([]byte, 32)
	rand.Read(secret)
	wgpsk := make([]byte, 32)
	rand.Read(wgpsk)
	serverKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	clientKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	client := Config{Mode: "client", Listen: "127.0.0.1:51821", Endpoint: endpoint, WireGuard: "127.0.0.1:51822", Key: encode(secret), Profile: profile}
	server := Config{Mode: "server", Listen: netip.AddrPortFrom(netip.IPv4Unspecified(), ep.Port()).String(), WireGuard: "127.0.0.1:51820", Key: encode(secret), Profile: profile}
	write := func(name string, b []byte) error { return os.WriteFile(filepath.Join(dir, name), b, 0600) }
	for name, c := range map[string]Config{"client.json": client, "server.json": server} {
		b, e := json.MarshalIndent(c, "", "  ")
		if e != nil {
			return e
		}
		b = append(b, '\n')
		if err = write(name, b); err != nil {
			return err
		}
	}
	serverWG := fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = 10.77.0.1/24, fd77:77:77::1/64\nListenPort = 51820\nMTU = 1280\n\n[Peer]\nPublicKey = %s\nPresharedKey = %s\nAllowedIPs = 10.77.0.2/32, fd77:77:77::2/128\n", encode(serverKey.Bytes()), encode(clientKey.PublicKey().Bytes()), encode(wgpsk))
	if err = write("cwg0.conf", []byte(serverWG)); err != nil {
		return err
	}
	clientWG := func(full bool) string {
		allowed := "10.77.0.0/24, fd77:77:77::/64"
		dns := ""
		if full {
			allowed = "0.0.0.0/1, 128.0.0.0/1, ::/1, 8000::/1"
			dns = "DNS = 1.1.1.1, 1.0.0.1\n"
		}
		return fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = 10.77.0.2/32, fd77:77:77::2/128\nListenPort = 51822\nMTU = 1280\n%s\n[Peer]\nPublicKey = %s\nPresharedKey = %s\nAllowedIPs = %s\nEndpoint = 127.0.0.1:51821\n", encode(clientKey.Bytes()), dns, encode(serverKey.PublicKey().Bytes()), encode(wgpsk), allowed)
	}
	if err = write("cwg-test.conf", []byte(clientWG(false))); err != nil {
		return err
	}
	if err = write("cwg-full.conf", []byte(clientWG(true))); err != nil {
		return err
	}
	return write("PRIVATE-README.txt", []byte("These files contain private keys. Do not publish or commit this directory.\nSend only server.json and cwg0.conf to your Linux server.\nKeep client.json and cwg-*.conf on the client.\nStart with cwg-test.conf (split tunnel). Full mode requires an endpoint bypass route and server forwarding; use the provided Windows helper.\nFull mode includes IPv6 routes; without IPv6 server egress, IPv6 fails rather than using your ISP.\nFull mode is not a system-wide kill switch after the interface is removed. See README.\nOne active client per key/port. Use fresh bundles for additional clients.\n"))
}
