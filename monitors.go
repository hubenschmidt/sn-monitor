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
	Index  int
	Output string
	Model  string
	Geom   string
	X, Y   int
	Width  int
	Height int
}

func listMonitors() ([]MonitorInfo, error) {
	out, err := exec.Command("xrandr", "--listmonitors").Output()
	if err != nil {
		return nil, fmt.Errorf("xrandr failed: %w", err)
	}

	edidNames := readEDIDNames()

	var monitors []MonitorInfo
	for _, line := range strings.Split(string(out), "\n") {
		m := parseMonitorLine(line, len(monitors), edidNames)
		if m != nil {
			monitors = append(monitors, *m)
		}
	}

	if len(monitors) == 0 {
		return nil, fmt.Errorf("no monitors detected")
	}
	return monitors, nil
}

func parseMonitorLine(line string, idx int, edidNames map[string]string) *MonitorInfo {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "Monitors:") {
		return nil
	}
	parts := strings.Fields(line)
	if len(parts) < 4 {
		return nil
	}
	output := parts[len(parts)-1]
	geom := parts[2]
	model := edidNames[output]
	if model == "" {
		model = "Unknown"
	}
	w, h, x, y := parseGeom(geom)
	return &MonitorInfo{
		Index: idx, Output: output, Model: model, Geom: geom,
		X: x, Y: y, Width: w, Height: h,
	}
}

// parseGeom parses "2192/700x1233/400+1080+177" → w=2192, h=1233, x=1080, y=177
func parseGeom(g string) (w, h, x, y int) {
	plusParts := strings.Split(g, "+")
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
	if len(plusParts) < 3 {
		return
	}
	x, _ = strconv.Atoi(plusParts[1])
	y, _ = strconv.Atoi(plusParts[2])
	return
}

func readEDIDNames() map[string]string {
	names := make(map[string]string)
	cards, _ := filepath.Glob("/sys/class/drm/card*-*")
	for _, cardPath := range cards {
		output, name := resolveEDID(cardPath)
		if output != "" {
			names[output] = name
		}
	}
	return names
}

func resolveEDID(cardPath string) (string, string) {
	edidPath := filepath.Join(cardPath, "edid")
	data, err := os.ReadFile(edidPath)
	if err != nil || len(data) < 128 {
		return "", ""
	}
	name := parseEDIDName(data)
	if name == "" {
		return "", ""
	}
	base := filepath.Base(cardPath)
	idx := strings.Index(base, "-")
	if idx < 0 {
		return "", ""
	}
	return base[idx+1:], name
}

func parseEDIDName(data []byte) string {
	for i := 0; i < 4; i++ {
		offset := 54 + i*18
		if offset+18 > len(data) {
			return ""
		}
		desc := data[offset : offset+18]
		if desc[0] == 0 && desc[1] == 0 && desc[2] == 0 && desc[3] == 0xFC {
			raw := string(desc[5:18])
			raw = strings.TrimRight(raw, "\n \x00\x0a")
			return strings.TrimSpace(raw)
		}
	}
	return ""
}
