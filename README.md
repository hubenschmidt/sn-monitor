# sn-monitor

CLI tool that silently captures a selected monitor via hotkey and sends the screenshot to Claude's vision API.

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

2. Create your `.env` file:
   ```bash
   cp .env.example .env
   # Edit .env and add your Anthropic API key
   ```

## Build & Run

```bash
go build -o sn-monitor .
./sn-monitor
```

## Usage

1. The CLI lists available monitors with model names — select one by number
2. Press **Ctrl+Alt+Space** to capture the selected monitor and get a solution
3. Press **Ctrl+C** to quit
