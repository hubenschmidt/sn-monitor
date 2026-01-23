package main

import (
	"bytes"
	"fmt"
	"image/jpeg"

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
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		return nil, fmt.Errorf("jpeg encode failed: %w", err)
	}
	return buf.Bytes(), nil
}
