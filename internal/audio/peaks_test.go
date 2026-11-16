package audio

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestMini(t *testing.T) {
	// A ramp from silence to full scale in the left channel, with a
	// negative spike early in the right channel.
	p := newPCM(48000, 2, 9600)
	for i := range p.Data[0] {
		p.Data[0][i] = float32(i) / 9599
	}
	p.Data[1][1050] = -0.8
	got := Mini(p)
	if len(got) != MiniBuckets {
		t.Fatalf("len = %d", len(got))
	}
	if got[0] != 3 || got[95] != 255 {
		t.Errorf("ends = %d, %d; want 3, 255", got[0], got[95])
	}
	for i := 1; i < len(got); i++ {
		if got[i] < got[i-1] && i != 11 {
			t.Errorf("bucket %d = %d falls below bucket %d = %d", i, got[i], i-1, got[i-1])
		}
	}
	if got[10] != 204 {
		t.Errorf("bucket 10 = %d, want 204 from the right channel's -0.8", got[10])
	}
}

func TestFull(t *testing.T) {
	p := newPCM(48000, 2, 4000)
	p.Data[0][0] = 1
	p.Data[1][1] = -1
	p.Data[0][2] = 0.5
	p.Data[1][3] = -0.25
	p.Data[0][3998] = 4 // float overshoot clamps
	p.Data[1][3999] = -4
	got := Full(p)
	if len(got) != FullSize {
		t.Fatalf("len = %d", len(got))
	}
	pair := func(i int) [2]int8 { return [2]int8{int8(got[2*i]), int8(got[2*i+1])} }
	for i, want := range map[int][2]int8{
		0:    {-127, 127},
		1:    {-32, 64},
		2:    {0, 0},
		1999: {-127, 127},
	} {
		if pair(i) != want {
			t.Errorf("bucket %d = %v, want %v", i, pair(i), want)
		}
	}
}

func TestPeaksShortAndEmpty(t *testing.T) {
	// Fewer frames than buckets: every bucket still gets a frame.
	p := newPCM(48000, 1, 4)
	copy(p.Data[0], []float32{0.25, -0.5, 0.75, -1})
	full := Full(p)
	for i, want := range map[int][2]int8{0: {32, 32}, 499: {32, 32}, 500: {-64, -64}, 1500: {-127, -127}, 1999: {-127, -127}} {
		if got := [2]int8{int8(full[2*i]), int8(full[2*i+1])}; got != want {
			t.Errorf("bucket %d = %v, want %v", i, got, want)
		}
	}
	mini := Mini(p)
	if mini[0] != 64 || mini[95] != 255 {
		t.Errorf("mini ends = %d, %d", mini[0], mini[95])
	}

	for _, empty := range []*PCM{nil, {}, newPCM(48000, 2, 0)} {
		if got := Full(empty); len(got) != FullSize || !bytes.Equal(got, make([]byte, FullSize)) {
			t.Error("Full of empty PCM is not 4000 zero bytes")
		}
		if got := Mini(empty); len(got) != MiniBuckets || !bytes.Equal(got, make([]byte, MiniBuckets)) {
			t.Error("Mini of empty PCM is not 96 zero bytes")
		}
	}
}

func TestPeaksCache(t *testing.T) {
	dir := t.TempDir()
	const id = "0123456789abcdef0123456789abcdef"
	if got, want := PeaksPath(dir, id), filepath.Join(dir, "01", id+".pk"); got != want {
		t.Errorf("PeaksPath = %q, want %q", got, want)
	}
	if _, err := LoadPeaks(dir, id); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing: err = %v", err)
	}
	if err := SavePeaks(dir, id, make([]byte, 10)); err == nil {
		t.Error("SavePeaks accepted a short buffer")
	}

	want := make([]byte, FullSize)
	for i := range want {
		want[i] = byte(i)
	}
	if err := SavePeaks(dir, id, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPeaks(dir, id)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("round trip: %v", err)
	}
	want[0] = 99
	if err := SavePeaks(dir, id, want); err != nil {
		t.Fatal(err)
	}
	if got, _ := LoadPeaks(dir, id); !bytes.Equal(got, want) {
		t.Error("overwrite did not take")
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "01"))
	if len(entries) != 1 {
		t.Errorf("%d files left in the shard, want 1", len(entries))
	}

	// A damaged cache file reads as absent.
	if err := os.WriteFile(PeaksPath(dir, id), []byte("short"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPeaks(dir, id); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("short file: err = %v", err)
	}
}
