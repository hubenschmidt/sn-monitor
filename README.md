# sn-monitor

CLI tool that captures a selected monitor via hotkey and pipes to an AI vision model. Supports terminal output or a transparent always-on-top overlay with syntax-highlighted code.

## Prerequisites

- Go 1.23+
- Linux / X11 (log in with "Ubuntu on Xorg" — Wayland is not supported)
- `xrandr` available in PATH
- **Overlay mode only:** `libwebkit2gtk-4.1-dev`
  ```bash
  sudo apt-get install -y libwebkit2gtk-4.1-dev
  ```

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
   # Edit .env and add your API key(s): ANTHROPIC_API_KEY and/or OPENAI_API_KEY
   ```

## Build & Run

```bash
make        # build
make run    # build + run
```

## Usage

1. Select a monitor by number
2. Select an AI model (Claude Opus 4.6 or GPT-5.2 Codex)
3. Select output mode (Terminal or Overlay)
4. Press **Left + Right arrow keys** simultaneously to capture and solve
5. Press **Ctrl+C** to quit

In overlay mode, drag the title bar to reposition the window.
