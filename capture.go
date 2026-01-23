package main

import (
	"bytes"
	"fmt"
	"image/png"

	"github.com/kbinani/screenshot"
)

func captureMonitor(index int) ([]byte, error) {
	n := screenshot.NumActiveDisplays()
	if index >= n {
		return nil, fmt.Errorf("monitor %d not available (only %d displays)", index, n)
	}

	bounds := screenshot.GetDisplayBounds(index)
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		return nil, fmt.Errorf("capture failed: %w", err)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("png encode failed: %w", err)
	}
	return buf.Bytes(), nil
}
