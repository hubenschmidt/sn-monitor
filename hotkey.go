package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

const (
	evKey      = 1
	keyPress   = 1
	keyRelease = 0
	keyLeft    = 105
	keyRight   = 106
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

func listenHotkey(ch chan<- struct{}) error {
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

func listenDevice(path string, ch chan<- struct{}) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	buf := make([]byte, inputEventSize*64)
	leftDown := false
	rightDown := false

	for {
		n, err := f.Read(buf)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		for i := 0; i+inputEventSize <= n; i += inputEventSize {
			ev := (*inputEvent)(unsafe.Pointer(&buf[i]))
			leftDown, rightDown = processEvent(ev, leftDown, rightDown, ch)
		}
	}
}

func processEvent(ev *inputEvent, leftDown, rightDown bool, ch chan<- struct{}) (bool, bool) {
	if ev.Type != evKey {
		return leftDown, rightDown
	}

	if ev.Code == keyLeft {
		leftDown = ev.Value == keyPress
	}
	if ev.Code == keyRight {
		rightDown = ev.Value == keyPress
	}

	if leftDown && rightDown {
		select {
		case ch <- struct{}{}:
		default:
		}
		return false, false
	}
	return leftDown, rightDown
}

func ioctl(fd uintptr, req uintptr, arg uintptr) (uintptr, uintptr, error) {
	r1, r2, errno := rawIoctl(fd, req, arg)
	if errno != 0 {
		return r1, r2, errno
	}
	return r1, r2, nil
}
