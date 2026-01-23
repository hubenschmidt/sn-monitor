package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

const (
	evKey        = 1
	keyPress     = 1
	keyRelease   = 0
	keyLeftCtrl  = 29
	keyRightCtrl = 97
	keyLeftAlt   = 56
	keyRightAlt  = 100
	keySpace     = 57
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

func findKeyboard() (string, error) {
	matches, _ := filepath.Glob("/dev/input/event*")
	for _, dev := range matches {
		name, err := deviceName(dev)
		if err != nil {
			continue
		}
		lower := strings.ToLower(name)
		if strings.Contains(lower, "keyboard") {
			return dev, nil
		}
	}
	// Fallback: try each device that supports EV_KEY
	for _, dev := range matches {
		if supportsEVKey(dev) {
			return dev, nil
		}
	}
	return "", fmt.Errorf("no keyboard found in /dev/input/")
}

func deviceName(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	buf := make([]byte, 256)
	// EVIOCGNAME ioctl
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

func supportsEVKey(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	// EVIOCGBIT(0, ...) to get event type bits
	var bits [4]byte
	const eviocgbit = 0x80044520
	_, _, errBit := ioctl(f.Fd(), eviocgbit, uintptr(unsafe.Pointer(&bits[0])))
	if errBit != nil {
		return false
	}
	// Check if EV_KEY (bit 1) is set
	return bits[0]&(1<<evKey) != 0
}

// listenHotkey listens on all keyboard devices for Ctrl+Alt+Space.
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
		name, err := deviceName(dev)
		if err != nil {
			continue
		}
		if strings.Contains(strings.ToLower(name), "keyboard") {
			result = append(result, dev)
		}
	}
	return result
}

func listenDevice(path string, ch chan<- struct{}) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	buf := make([]byte, inputEventSize*64)
	ctrlDown := false
	altDown := false

	for {
		n, err := f.Read(buf)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		for i := 0; i+inputEventSize <= n; i += inputEventSize {
			ev := (*inputEvent)(unsafe.Pointer(&buf[i]))
			if ev.Type != evKey {
				continue
			}

			pressed := ev.Value == keyPress
			released := ev.Value == keyRelease

			switch ev.Code {
			case keyLeftCtrl, keyRightCtrl:
				if pressed {
					ctrlDown = true
				}
				if released {
					ctrlDown = false
				}
			case keyLeftAlt, keyRightAlt:
				if pressed {
					altDown = true
				}
				if released {
					altDown = false
				}
			case keySpace:
				if pressed && ctrlDown && altDown {
					select {
					case ch <- struct{}{}:
					default:
					}
				}
			}
		}
	}
}

func ioctl(fd uintptr, req uintptr, arg uintptr) (uintptr, uintptr, error) {
	r1, r2, errno := rawIoctl(fd, req, arg)
	if errno != 0 {
		return r1, r2, errno
	}
	return r1, r2, nil
}
