---
id: 499f2d2b-2ead-4726-95df-f867c52f8fd2
slug: eg4-flexboss21-cli-tool
entity: holdingco
---

# eg4ctl

CLI for working with the **EG4 FlexBoss21** hybrid inverter and **GridBoss**
over the LAN — no cloud dependency. Speaks the LuxPower/EG4 WiFi-dongle TCP
protocol (port 8000). Read-only by design in v0: every write path is
fail-closed and unimplemented until explicitly built and reviewed.

Linear project: [EG4 FlexBoss21 CLI (eg4ctl)](https://linear.app/holdingco11/project/eg4-flexboss21-cli-eg4ctl-4a0c0ff4414f)

## Install

```sh
go build -o eg4ctl ./cmd/eg4ctl
```

## Commands

| Command | What it does |
|---|---|
| `eg4ctl discover --cidr 192.168.42.0/24` | Find hosts with tcp/8000 open (dongle candidates) |
| `eg4ctl listen --host <ip> --duration 150s` | Print every frame the dongle pushes (identifies dongle + inverter serials) |
| `eg4ctl read --host <ip> --type input --reg 0 --count 40` | Read a register bank |
| `eg4ctl status --host <ip> [--format json]` | Decoded snapshot (SoC, PV, battery) — **experimental offsets** + raw hex |

Reads require `--dongle-serial` and `--inverter-serial` (10 chars each — on
the dongle sticker, or shown by `listen` once frames arrive). Defaults come
from `EG4_HOST`, `EG4_DONGLE_SERIAL`, `EG4_INVERTER_SERIAL`.

## Protocol notes

- Frame layout follows community reverse engineering:
  [celsworth/lxp-bridge](https://github.com/celsworth/lxp-bridge),
  [jefflaplante/lux PROTOCOL.md](https://github.com/jefflaplante/lux/blob/main/PROTOCOL.md),
  register maps in [joyfulhouse/eg4_web_monitor](https://github.com/joyfulhouse/eg4_web_monitor).
- The dongle pushes input-register banks roughly every 2 minutes to connected
  TCP clients; `listen` answers heartbeats to keep the session alive.
- **Gotcha:** newer EG4 dongle firmware (encrypted protocol) disables the
  plain port-8000 interface. If `listen` hears nothing on a confirmed dongle
  IP, fall back to RS485 (INV485 port, shared with the dongle) or the cloud
  API. Registers decode in `status` is marked experimental until verified
  against live FlexBoss21 hardware
  ([CB-2](https://linear.app/holdingco11/issue/CB-2/dongle-tcp8000-client-luxpower-framing-register-reads)).

## Roadmap

- Cloud client for monitor.eg4electronics.com (blocked on vault grant,
  [CB-6](https://linear.app/holdingco11/issue/CB-6/cloud-api-client-blocked-grant-read-on-secretcbeg4))
- `--format prometheus`, Nagios/Gatus freshness checks
  ([CB-7](https://linear.app/holdingco11/issue/CB-7/observability-format-jsonprom-nagiosgatus-checks-loki-logging))
- Verified FlexBoss21/GridBoss register maps; write mode behind
  `--enable-writes` with confirmation, only after read path is proven.

## Safety

This tool touches a live residential power system. v0 cannot write any
register. When write support lands it will be fail-closed: explicit flag,
per-register allowlist, confirmation prompt, and an audit log line per write.
