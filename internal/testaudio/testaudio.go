// Package testaudio synthesises small WAV files for tests, so no test needs
// audio fixtures on disk or from the network.
package testaudio

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
)

// Rate is the sample rate of everything this package generates.
const Rate = 44100

// Tone is a sine at freq Hz lasting seconds, with an exponential decay when
// decay (per second) is above zero: a pad when zero, a kick or pluck
// otherwise.
func Tone(freq, seconds, decay float64) []float32 {
	out := make([]float32, int(seconds*Rate))
	for i := range out {
		t := float64(i) / Rate
		out[i] = float32(0.8 * math.Exp(-decay*t) * math.Sin(2*math.Pi*freq*t))
	}
	return out
}

// Loop is a bar-cut loop of the given tempo: a short decaying tone on every
// beat, pitched at freq.
func Loop(bpm float64, beats int, freq float64) []float32 {
	beat := int(60 / bpm * Rate)
	out := make([]float32, beat*beats)
	hit := Tone(freq, 0.12, 30)
	for b := range beats {
		copy(out[b*beat:], hit)
	}
	return out
}

// Encode returns mono 16-bit PCM WAV bytes for samples.
func Encode(samples []float32) []byte {
	var buf bytes.Buffer
	data := len(samples) * 2
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(36+data))
	buf.WriteString("WAVEfmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // PCM
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // mono
	binary.Write(&buf, binary.LittleEndian, uint32(Rate))
	binary.Write(&buf, binary.LittleEndian, uint32(Rate*2))
	binary.Write(&buf, binary.LittleEndian, uint16(2))
	binary.Write(&buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(data))
	for _, v := range samples {
		binary.Write(&buf, binary.LittleEndian, int16(math.Round(float64(max(-1, min(1, v)))*32767)))
	}
	return buf.Bytes()
}

// Write stores samples as a WAV file at path, creating parent folders.
func Write(path string, samples []float32) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, Encode(samples), 0o644)
}
