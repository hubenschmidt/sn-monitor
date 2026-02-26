# sn-monitor

CLI tool that silently captures a selected monitor via hotkey and pipes to Claude Opus 4.5

## Prerequisites

- Go 1.21+
- Linux / X11 (log in with "Ubuntu on Xorg" — Wayland is not supported)
- `xrandr` available in PATH

## Setup (one-time)

1. Add yourself to the `input` group (required for global hotkey):
   ```bash
   sudo usermod -aG input $USER
   ```
   Log out and back in for this to take effect.

2. If you don't want to log out, activate the group in your current shell:
   ```bash
   newgrp input
   ```

3. Create your `.env` file:
   ```bash
   cp .env.example .env
   # Edit .env and add your Anthropic API key
   ```

## Build & Run

```bash
go build -o sn-monitor .
./sn-monitor
```

## Architecture

```
┌─────────────┐     ┌──────────────┐     ┌──────────────┐
│  main.go    │     │ monitors.go  │     │  hotkey.go   │
│             │────▶│  xrandr +    │     │ /dev/input/* │
│  CLI entry  │     │  EDID parse  │     │  L+R arrow   │
└──────┬──────┘     └──────────────┘     └──────┬───────┘
       │  user selects monitor                  │ hotkey fired
       ▼                                        ▼
┌──────────────┐                        ┌──────────────┐
│ capture.go   │◀───────────────────────│  main loop   │
│  screenshot  │  capture selected      │  (goroutine) │
│  → JPEG 95%  │  monitor bounds        └──────────────┘
└──────┬───────┘
       │ []byte (JPEG)
       ▼
┌──────────────┐     ┌──────────────────┐
│  solve.go    │────▶│  Claude Opus 4.6 │
│  base64 enc  │     │  vision API      │
│  msg history │◀────│  streaming resp  │
└──────────────┘     └──────────────────┘
```

**Flow**: startup → detect monitors → user picks one → listen for hotkey → capture screen → JPEG encode → base64 → Claude vision API → print response. Message history persists across captures for multi-turn context.

## Usage

1. The CLI lists available monitors with model names — select one by number
2. Press **Left + Right arrow keys** simultaneously to capture and get a solution
3. Press **Ctrl+C** to quit
