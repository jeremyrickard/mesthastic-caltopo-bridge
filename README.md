# Meshtastic TAK to CalTopo Bridge

A Go daemon that reads Meshtastic packets over USB serial, stores them in
SQLite, decodes Meshtastic and legacy ATAK position reports, and optionally
publishes positions as CalTopo live tracks. It also serves a Leaflet map of each
node's latest position.

Supported runtime targets:

- macOS Intel (`darwin/amd64`)
- macOS Apple Silicon (`darwin/arm64`)
- Linux x86_64 (`linux/amd64`)
- Raspberry Pi 5 and other Linux ARM64 systems (`linux/arm64`)

## Current protocol scope

The bridge decodes standard Meshtastic positions from port 3 (`POSITION_APP`)
and legacy ATAK position reports from port 72 (`ATAK_PLUGIN`). This supports
ordinary `CLIENT` nodes as well as `TAK` and `TAK_TRACKER` roles. Port 3 reports
retain their location source and configured coordinate precision; reports with
no GPS fix are archived as `position_no_fix` without creating a position. The
bridge associates these reports with persisted Meshtastic node information,
preferring each radio's short name and falling back to its long name.
Every received mesh packet is archived, including raw decoded payloads or
ciphertext. Ports 78 (`ATAK_PLUGIN_V2`) and 257 (`ATAK_FORWARDER`) are identified
and stored but are not decoded because their encodings and ecosystem support are
still evolving. Compressed legacy reports retain their PLI data; when their
unishox2 callsign cannot be decoded, the bridge uses the originating node ID as
the track name and records `tak_callsign_undecodable` as the packet parse status.

The attached radio performs Meshtastic channel/PKI decryption before forwarding
packets to the serial client. Configure the private channel key on the radio, not
in this application. Packets that the radio cannot decrypt are retained and
produce a rate-limited warning.

## Radio setup

1. Configure the Meshtastic radio with the channels and private keys used by the
   TAK devices.
2. Use `TAK` or `TAK_TRACKER` for position-reporting nodes. The radio attached
   to the bridge may use a forwarding role such as `ROUTER_LATE` when it only
   receives and relays their packets.
3. Leave the serial interface enabled.
4. Avoid the public default channel key (`AQ==`) for operational traffic.
5. Connect the radio over USB and find its serial path:

```sh
./meshtastic-caltopo-bridge -list-devices
```

On macOS use the `/dev/cu.usbmodem*` device rather than `/dev/tty.*`. On Linux,
prefer `/dev/serial/by-id/...` so reconnects and reboots do not change the path.

## Build and test

Go 1.24 or newer is required. `gotopo` is currently a private, untagged module,
so the GitHub account used for builds must have repository access and Git must be
configured for authenticated GitHub HTTPS access.

```sh
export GOPRIVATE=github.com/jeremyrickard/gotopo
export GONOSUMDB="$GOPRIVATE"
make test
make build
```

The host binary is written to `bin/meshtastic-caltopo-bridge`.

Build all four release executables and their SHA-256 checksums:

```sh
make release VERSION=0.1.0
ls dist/
```

Release builds disable CGO and include the version and Git commit:

```text
meshtastic-caltopo-bridge_0.1.0_darwin_amd64
meshtastic-caltopo-bridge_0.1.0_darwin_arm64
meshtastic-caltopo-bridge_0.1.0_linux_amd64
meshtastic-caltopo-bridge_0.1.0_linux_arm64
SHA256SUMS
```

## Local development

Copy the example environment file and adjust paths:

```sh
cp config/bridge.env.example .env
set -a
. ./.env
set +a
go run ./cmd/bridge
```

For a minimal run:

```sh
go run ./cmd/bridge \
  -serial-device /dev/cu.usbmodem123456 \
  -database ./bridge.db \
  -http-listen 127.0.0.1:8080
```

Open [http://localhost:8080](http://localhost:8080) to view the position map.
The initial view automatically fits the latest stored point for each node. Map
tiles and Leaflet are loaded from public CDNs, so the browser needs internet
access.

The serial transport and CalTopo publisher are isolated behind interfaces, so
the automated tests do not require a radio or CalTopo account.

## Configuration

Flags override the most commonly changed values. Environment variables are the
normal service configuration mechanism.

| Variable | Default | Purpose |
|---|---:|---|
| `MESHTASTIC_SERIAL_DEVICE` | required | USB serial device |
| `MESHTASTIC_SERIAL_BAUD` | `115200` | Serial baud rate |
| `BRIDGE_DATABASE_PATH` | `bridge.db` | SQLite database |
| `HTTP_LISTEN_ADDRESS` | `127.0.0.1:8080` | Position map listen address |
| `BRIDGE_DEBUG` | `false` | Log every raw serial read and decoded radio message |
| `DECODE_POSITION_APP` | `true` | Decode standard position packets on port 3 |
| `CALTOPO_ENABLED` | `false` | Enable live-track delivery |
| `CALTOPO_ENDPOINT` | `caltopo.com` | CalTopo/SARTopo or local endpoint |
| `CALTOPO_MAP_ID` | required when enabled | Destination map ID |
| `CALTOPO_CREDENTIAL_ID` | hosted only | CalTopo credential ID |
| `CALTOPO_KEY` | hosted only | Base64 CalTopo credential key |
| `CALTOPO_ACCOUNT_ID` | empty | Account ID used by `gotopo` |
| `CALTOPO_GROUP` | `mesh` | Fleet group; cannot contain `-` |
| `CALTOPO_TIMEOUT` | `10s` | Per-request deadline |
| `CALTOPO_MOVEMENT_METERS` | `25` | Minimum median-filtered movement before an update |
| `CALTOPO_HEARTBEAT` | `5m` | Maximum interval between updates for a stationary node |

Keep the environment file root-readable because it contains CalTopo credentials:

```sh
chmod 600 /etc/meshtastic-caltopo-bridge.env
```

For radio troubleshooting, set `BRIDGE_DEBUG=true` or pass `-debug`. Debug
output includes raw serial data and complete decoded messages, which may expose
private message content and radio configuration. Disable it after troubleshooting.

The map has no authentication. To access it from another computer, explicitly
bind it to a network interface and restrict port `8080` to trusted clients.

The supplied systemd unit uses a private `0700` state directory and `0077`
process umask. Correct permissions manually when upgrading an existing install.

## SQLite and delivery behavior

SQLite runs in WAL mode and stores:

- every received mesh packet and raw payload/ciphertext;
- normalized Meshtastic and legacy TAK position reports, including their source port;
- stable Meshtastic node-to-CalTopo live-track mappings;
- durable CalTopo delivery attempts and error details.

Packet archival occurs in the same transaction that creates a normalized
position and its outbox entry. CalTopo outages do not stop radio ingestion.
Deliveries retry indefinitely with capped exponential backoff and preserve
per-node ordering.
Repeated firmware copies of the same position are archived as packets but only
produce one normalized position.

No retention limit is applied yet. Back up all three SQLite files together while
the service is stopped, or use SQLite's online backup tooling.

## Raspberry Pi/systemd deployment

Install the ARM64 executable, environment file, and unit:

```sh
sudo install -m 0755 dist/meshtastic-caltopo-bridge_0.1.0_linux_arm64 \
  /usr/local/bin/meshtastic-caltopo-bridge
sudo install -m 0600 config/bridge.env.example \
  /etc/meshtastic-caltopo-bridge.env
sudo install -m 0644 deploy/systemd/meshtastic-caltopo-bridge.service \
  /etc/systemd/system/
sudo useradd --system --home /var/lib/meshtastic-caltopo-bridge \
  --shell /usr/sbin/nologin meshtastic-bridge
sudo systemctl daemon-reload
sudo systemctl enable --now meshtastic-caltopo-bridge
```

Edit the environment file before starting. The service grants access through the
`dialout` group; distributions using another serial-device group must adjust
`SupplementaryGroups`.

Inspect JSON logs with:

```sh
journalctl -u meshtastic-caltopo-bridge -f
```

The same binary and unit work on systemd-based x86_64 Linux after choosing the
`linux_amd64` artifact. macOS runs in the foreground; launchd packaging is not
included.

## CalTopo caveats

CalTopo support uses the unofficial API exposed by
[`github.com/jeremyrickard/gotopo`](https://github.com/jeremyrickard/gotopo).
The dependency is pinned to an exact commit until it receives a stable release.
Each Meshtastic source node maps to one `FLEET:<group>-<node>` live track.

The API does not accept the original TAK timestamp, so this daemon suppresses
duplicates and preserves ordering before publishing. Delivery errors and attempt counts remain in SQLite until a later retry succeeds.

## License

GPL-3.0. See [LICENSE](LICENSE).
