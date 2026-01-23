package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type MonitorInfo struct {
	Index   int
	Output  string
	Model   string
	Geom    string
	X, Y    int
	Width   int
	Height  int
}

func listMonitors() ([]MonitorInfo, error) {
	out, err := exec.Command("xrandr", "--listmonitors").Output()
	if err != nil {
		return nil, fmt.Errorf("xrandr failed: %w", err)
	}

	edidNames := readEDIDNames()

	var monitors []MonitorInfo
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Monitors:") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 4 {
			continue
		}
		idx := len(monitors)
		output := parts[len(parts)-1]
		geom := parts[2]
		model := edidNames[output]
		if model == "" {
			model = "Unknown"
		}
		w, h, x, y := parseGeom(geom)
		monitors = append(monitors, MonitorInfo{
			Index: idx, Output: output, Model: model, Geom: geom,
			X: x, Y: y, Width: w, Height: h,
		})
	}

	if len(monitors) == 0 {
		return nil, fmt.Errorf("no monitors detected")
	}
	return monitors, nil
}

// parseGeom parses "2192/700x1233/400+1080+177" → w=2192, h=1233, x=1080, y=177
func parseGeom(g string) (w, h, x, y int) {
	plusParts := strings.Split(g, "+")
	if len(plusParts) >= 3 {
		x, _ = strconv.Atoi(plusParts[1])
		y, _ = strconv.Atoi(plusParts[2])
	}
	dims := plusParts[0]
	xIdx := strings.Index(dims, "x")
	if xIdx < 0 {
		return
	}
	wPart := dims[:xIdx]
	hPart := dims[xIdx+1:]
	if slashIdx := strings.Index(wPart, "/"); slashIdx > 0 {
		wPart = wPart[:slashIdx]
	}
	if slashIdx := strings.Index(hPart, "/"); slashIdx > 0 {
		hPart = hPart[:slashIdx]
	}
	w, _ = strconv.Atoi(wPart)
	h, _ = strconv.Atoi(hPart)
	return
}

func readEDIDNames() map[string]string {
	names := make(map[string]string)
	cards, _ := filepath.Glob("/sys/class/drm/card*-*")
	for _, cardPath := range cards {
		edidPath := filepath.Join(cardPath, "edid")
		data, err := os.ReadFile(edidPath)
		if err != nil || len(data) < 128 {
			continue
		}
		name := parseEDIDName(data)
		if name == "" {
			continue
		}
		// card path like /sys/class/drm/card1-DP-4 → output "DP-4"
		base := filepath.Base(cardPath)
		idx := strings.Index(base, "-")
		if idx < 0 {
			continue
		}
		output := base[idx+1:]
		names[output] = name
	}
	return names
}

func parseEDIDName(data []byte) string {
	// Descriptors start at byte 54, each 18 bytes, 4 descriptors in base block
	for i := 0; i < 4; i++ {
		offset := 54 + i*18
		if offset+18 > len(data) {
			break
		}
		desc := data[offset : offset+18]
		// Monitor name descriptor: bytes 0-2 = 0x00, byte 3 = 0xFC
		if desc[0] != 0 || desc[1] != 0 || desc[2] != 0 || desc[3] != 0xFC {
			continue
		}
		raw := string(desc[5:18])
		raw = strings.TrimRight(raw, "\n \x00\x0a")
		return strings.TrimSpace(raw)
	}
	return ""
}
