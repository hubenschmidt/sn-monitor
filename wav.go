package main

import (
	"bytes"
	"encoding/binary"
)

// EncodeWAV produces a minimal RIFF/WAV file from raw PCM samples.
func EncodeWAV(samples []int16, sampleRate int) []byte {
	dataSize := len(samples) * 2
	fileSize := 36 + dataSize

	var buf bytes.Buffer
	buf.Grow(44 + dataSize)

	// RIFF header
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, int32(fileSize))
	buf.WriteString("WAVE")

	// fmt sub-chunk
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, int32(16))          // sub-chunk size
	binary.Write(&buf, binary.LittleEndian, int16(1))           // PCM format
	binary.Write(&buf, binary.LittleEndian, int16(1))           // mono
	binary.Write(&buf, binary.LittleEndian, int32(sampleRate))  // sample rate
	binary.Write(&buf, binary.LittleEndian, int32(sampleRate*2)) // byte rate
	binary.Write(&buf, binary.LittleEndian, int16(2))           // block align
	binary.Write(&buf, binary.LittleEndian, int16(16))          // bits per sample

	// data sub-chunk
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, int32(dataSize))
	binary.Write(&buf, binary.LittleEndian, samples)

	return buf.Bytes()
}
