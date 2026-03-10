package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

type HotkeyAction int

const (
	HotkeyCapture      HotkeyAction = iota // Left+Right
	HotkeyFollowUp                         // Up+Down (toggle voice recording)
	HotkeyExplain                          // Left+Up
	HotkeyAudioCapture                     // Left+Down (toggle audio capture)
	HotkeyAudioSend                        // Right+Down (send accumulated transcript to LLM)
	HotkeyToggleView                       // Right+Up (toggle transcript/chat view)
	HotkeyClear                            // Right+Left+Up+Down (clear conversation history)
	HotkeySoundCheck                       // overlay-button only
	HotkeyImplement                        // overlay-button only
)

var keyLabels = map[HotkeyAction]string{
	HotkeyCapture:      "←→ screen",
	HotkeyAudioCapture: "←↓ audio",
	HotkeyFollowUp:     "↑↓ mic",
	HotkeyAudioSend:    "→↓ process",
	HotkeyClear:        "←→↑↓ clear",
	HotkeySoundCheck:   "🔊 check",
	HotkeyImplement:    "⚙ impl",
}

var keyOrder = []HotkeyAction{
	HotkeyCapture,
	HotkeyAudioCapture,
	HotkeyFollowUp,
	HotkeyAudioSend,
	HotkeySoundCheck,
	HotkeyImplement,
	HotkeyClear,
}

var actionNames = map[HotkeyAction]string{
	HotkeyCapture:      "capture",
	HotkeyFollowUp:     "voice",
	HotkeyExplain:      "explain",
	HotkeyAudioCapture: "audio",
	HotkeyAudioSend:    "send",
	HotkeySoundCheck:   "soundcheck",
	HotkeyImplement:    "implement",
	HotkeyClear:        "clear",
}

const (
	evKey      = 1
	keyPress   = 1
	keyRelease = 0
	keyUp      = 103
	keyLeft    = 105
	keyRight   = 106
	keyDown    = 108
)

// inputEvent matches the Linux input_event struct on 64-bit.
type inputEvent struct {
	TimeSec  int64
	TimeUsec int64
	Type     uint16
	Code     uint16
	Value    int32
}

var inputEventSize = int(unsafe.Sizeof(inputEvent{}))

type keyState struct {
	left  bool
	right bool
	up    bool
	down  bool
}

func listenHotkey(ch chan<- HotkeyAction) error {
	keyboards := findAllKeyboards()
	if len(keyboards) == 0 {
		return fmt.Errorf("no keyboard found in /dev/input/")
	}

	errCh := make(chan error, len(keyboards))
	for _, dev := range keyboards {
		fmt.Fprintf(os.Stderr, "listening on: %s\n", dev)
		go func(path string) {
			errCh <- listenDevice(path, ch)
		}(dev)
	}
	return <-errCh
}

func findAllKeyboards() []string {
	matches, _ := filepath.Glob("/dev/input/event*")
	var result []string
	for _, dev := range matches {
		if isKeyboard(dev) {
			result = append(result, dev)
		}
	}
	return result
}

func isKeyboard(path string) bool {
	name, err := deviceName(path)
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(name), "keyboard")
}

func deviceName(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	buf := make([]byte, 256)
	const eviocgname = 0x80ff4506
	_, _, err = ioctl(f.Fd(), eviocgname, uintptr(unsafe.Pointer(&buf[0])))
	if err != nil {
		return "", fmt.Errorf("ioctl: %v", err)
	}
	end := 0
	for end < len(buf) && buf[end] != 0 {
		end++
	}
	return string(buf[:end]), nil
}

func listenDevice(path string, ch chan<- HotkeyAction) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	buf := make([]byte, inputEventSize*64)
	var ks keyState

	for {
		n, err := f.Read(buf)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		for i := 0; i+inputEventSize <= n; i += inputEventSize {
			ev := (*inputEvent)(unsafe.Pointer(&buf[i]))
			ks = processEvent(ev, ks, ch)
		}
	}
}

func processEvent(ev *inputEvent, ks keyState, ch chan<- HotkeyAction) keyState {
	if ev.Type != evKey {
		return ks
	}

	pressed := ev.Value == keyPress
	ks = updateKeyState(ev.Code, pressed, ks)

	// All four keys → clear conversation history
	if ks.left && ks.right && ks.up && ks.down {
		send(ch, HotkeyClear)
		ks.left, ks.right, ks.up, ks.down = false, false, false, false
		return ks
	}

	// Left+Right → screen capture
	if ks.left && ks.right {
		send(ch, HotkeyCapture)
		ks.left, ks.right = false, false
		return ks
	}

	// Left+Down → toggle audio capture
	if ks.left && ks.down {
		send(ch, HotkeyAudioCapture)
		ks.left, ks.down = false, false
		return ks
	}

	// Right+Down → send accumulated transcript to LLM
	if ks.right && ks.down {
		send(ch, HotkeyAudioSend)
		ks.right, ks.down = false, false
		return ks
	}

	// Up+Down → toggle voice recording
	if ks.up && ks.down {
		send(ch, HotkeyFollowUp)
		ks.up, ks.down = false, false
		return ks
	}

	return ks
}

func updateKeyState(code uint16, pressed bool, ks keyState) keyState {
	if code == keyLeft {
		ks.left = pressed
	}
	if code == keyRight {
		ks.right = pressed
	}
	if code == keyUp {
		ks.up = pressed
	}
	if code == keyDown {
		ks.down = pressed
	}
	return ks
}

func send(ch chan<- HotkeyAction, action HotkeyAction) {
	select {
	case ch <- action:
	default:
	}
}

func ioctl(fd uintptr, req uintptr, arg uintptr) (uintptr, uintptr, error) {
	r1, r2, errno := rawIoctl(fd, req, arg)
	if errno != 0 {
		return r1, r2, errno
	}
	return r1, r2, nil
}
