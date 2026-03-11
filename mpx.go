package main

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

type MouseInfo struct {
	ID   string `json:"ID"`
	Name string `json:"Name"`
}

var mouseIDRe = regexp.MustCompile(`id=(\d+)`)

var excludeDevices = []string{
	"virtual", "touchpad", "trackpoint", "touch", "tablet",
	"stylus", "eraser", "pad", "consumer", "power", "secondary",
}

func listMice() []MouseInfo {
	out, err := exec.Command("xinput", "list", "--short").Output()
	if err != nil {
		return nil
	}
	var mice []MouseInfo
	for _, line := range strings.Split(string(out), "\n") {
		m := parseMouse(line)
		if m != nil {
			mice = append(mice, *m)
		}
	}
	return mice
}

func parseMouse(line string) *MouseInfo {
	if !strings.Contains(line, "slave  pointer") {
		return nil
	}
	if isExcludedDevice(line) {
		return nil
	}
	m := mouseIDRe.FindStringSubmatch(line)
	if len(m) < 2 {
		return nil
	}
	nameEnd := strings.Index(line, "\tid=")
	if nameEnd < 0 {
		return nil
	}
	name := strings.TrimLeft(line[:nameEnd], "⎡⎜⎣↳ \t")
	return &MouseInfo{ID: m[1], Name: strings.TrimSpace(name)}
}

func isExcludedDevice(line string) bool {
	lower := strings.ToLower(line)
	for _, pat := range excludeDevices {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

func isMPXActive() bool {
	out, err := exec.Command("xinput", "list", "--name-only").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "Secondary pointer")
}

func setupMPX(deviceID string) error {
	teardownMPX()
	if err := exec.Command("xinput", "create-master", "Secondary").Run(); err != nil {
		return fmt.Errorf("create-master: %w", err)
	}
	if err := exec.Command("xinput", "reattach", deviceID, "Secondary pointer").Run(); err != nil {
		return fmt.Errorf("reattach %s: %w", deviceID, err)
	}
	return nil
}

func teardownMPX() {
	if !isMPXActive() {
		return
	}
	exec.Command("xinput", "remove-master", "Secondary pointer", "AttachToMaster", "Virtual core pointer").Run()
}
