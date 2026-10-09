#!/usr/bin/env bash
# Installs the split-tunnel server. Internet forwarding is a separate, inspected
# deployment step; this script never flushes the host's firewall or enables NAT.
set -euo pipefail
if [[ $# -ne 2 ]]; then echo 'Usage: sudo bash install-server.sh /path/to/cwg-linux-ARCH /path/to/private-server-files' >&2; exit 2; fi
if [[ $(id -u) -ne 0 ]]; then echo 'Run as root.' >&2; exit 1; fi
binary=$(realpath -- "$1")
bundle=$(realpath -- "$2")
here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
for command in wg wg-quick nft ip ss systemctl python3; do
    command -v "$command" >/dev/null || { echo "Missing dependency: $command (Debian/Ubuntu: wireguard-tools nftables iproute2 python3)." >&2; exit 1; }
done
[[ $(command -v nft) == /usr/sbin/nft ]] || { echo 'Adjust cwg-guard.service for this distribution nft path first.' >&2; exit 1; }
[[ -d /run/systemd/system ]] || { echo 'systemd is required.' >&2; exit 1; }
systemd_version=$(systemctl --version | awk 'NR==1 {print $2}')
[[ $systemd_version -ge 247 ]] || { echo 'systemd 247+ is required for LoadCredential.' >&2; exit 1; }
for path in /etc/cwg /etc/wireguard/cwg0.conf /usr/local/lib/cwg /etc/systemd/system/cwg.service /etc/systemd/system/cwg-guard.service /etc/systemd/system/wg-quick@cwg0.service.d; do
    [[ ! -e $path ]] || { echo "Existing installation at $path; refusing to overwrite. Inspect and back it up first." >&2; exit 1; }
done
if nft list table inet cwg_guard >/dev/null 2>&1; then echo 'Reserved cwg_guard table already exists; inspect it first.' >&2; exit 1; fi
if ip link show cwg0 >/dev/null 2>&1; then echo 'cwg0 already exists.' >&2; exit 1; fi
"$binary" check -config "$bundle/server.json"
outer_port=$(python3 -c 'import json,sys; print(int(json.load(open(sys.argv[1]))["listen"].rsplit(":",1)[1]))' "$bundle/server.json")
for port in 51820 "$outer_port"; do
    if [[ -n $(ss -H -lun "sport = :$port") ]]; then echo "UDP port $port is already in use; refusing to disturb it." >&2; exit 1; fi
done
# Match fixed ports/addresses assumed by the guard and generated tunnel config.
python3 - "$bundle" <<'PY'
import json, pathlib, sys
p = pathlib.Path(sys.argv[1])
c = json.loads((p / 'server.json').read_text())
assert c['mode'] == 'server' and c['wireguard'] == '127.0.0.1:51820', 'Unexpected backend'
assert c['listen'].startswith('0.0.0.0:') and int(c['listen'].split(':')[1]) != 51820, 'Unexpected public listener'
text = (p / 'cwg0.conf').read_text()
required = ['ListenPort = 51820', 'MTU = 1280', 'Address = 10.77.0.1/24, fd77:77:77::1/64', 'AllowedIPs = 10.77.0.2/32, fd77:77:77::2/128']
assert all(line in text.splitlines() for line in required), 'Use the generated cwg0.conf'
assert not any(line.strip().split('=')[0].strip() in ('PreUp','PostUp','PreDown','PostDown','SaveConfig') for line in text.splitlines()), 'Unexpected wg-quick hook'
PY
nft --check --file "$here/guard.nft"
install -d -m 0700 /etc/cwg /etc/wireguard
install -d -m 0755 /usr/local/lib/cwg /etc/systemd/system/wg-quick@cwg0.service.d
install -m 0755 "$binary" /usr/local/lib/cwg/cwg
install -m 0600 "$bundle/server.json" /etc/cwg/server.json
install -m 0600 "$bundle/cwg0.conf" /etc/wireguard/cwg0.conf
install -m 0600 "$here/guard.nft" /etc/cwg/guard.nft
install -m 0644 "$here/cwg.service" "$here/cwg-guard.service" /etc/systemd/system/
printf '[Unit]\nRequires=cwg-guard.service\nAfter=cwg-guard.service\n' > /etc/systemd/system/wg-quick@cwg0.service.d/cwg.conf
systemctl daemon-reload
systemctl enable --now cwg-guard.service
systemctl enable --now wg-quick@cwg0.service cwg.service
systemctl is-active --quiet cwg-guard.service wg-quick@cwg0.service cwg.service
echo 'Split-tunnel server installed. Allow the configured OUTER UDP port in host/cloud firewalls.'
echo 'Native UDP port 51820 is blocked outside loopback. Internet forwarding/NAT is not enabled.'
echo 'Run the client probe, then test ping 10.77.0.1. Inspect forwarding separately before Full mode.'
