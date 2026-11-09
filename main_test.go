package main

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sine returns a mono sine wave in [-1, 1] with the given parameters.
func sine(rate int, freq, seconds float64) []float64 {
	n := int(seconds * float64(rate))
	out := make([]float64, n)
	for i := range out {
		out[i] = math.Sin(2 * math.Pi * freq * float64(i) / float64(rate))
	}
	return out
}

func TestWAVRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tone.wav")
	orig := sine(8000, 440, 0.5)
	if err := writeWAV(path, orig, 8000); err != nil {
		t.Fatal(err)
	}
	got, err := decodeWAV(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.SampleRate != 8000 || got.Channels != 1 || got.frames() != len(orig) {
		t.Fatalf("unexpected wav header: %+v", got)
	}
	if s := snrDB(orig, got.Samples); s < 80 {
		t.Fatalf("round-trip SNR %.1f dB, want >= 80", s)
	}
}

func TestQuantizeSilenceAndExtremes(t *testing.T) {
	b := quantize([]float64{0, 1, -1, 0.5}, 128)
	want := []byte{128, 255, 1, 192}
	for i := range want {
		if b[i] != want[i] {
			t.Errorf("quantize[%d] = %d, want %d", i, b[i], want[i])
		}
	}
}

func TestResampleDoublesLength(t *testing.T) {
	a := &decodedAudio{Samples: sine(8000, 440, 1), SampleRate: 8000, Channels: 1}
	out := resampleAudio(a, 16000)
	if len(out.Samples) != 16000 {
		t.Fatalf("resampled length %d, want 16000", len(out.Samples))
	}
	if out.SampleRate != 16000 {
		t.Fatalf("sample rate %d", out.SampleRate)
	}
}

func TestLookupRenderIsFaithful(t *testing.T) {
	mono := sine(8000, 440, 0.25)
	data := quantize(mono, 128)
	rendered := dequantize(renderLookup(data, "loop", 128, len(mono)), 128)
	if s := snrDB(mono, rendered); s < 50 {
		t.Fatalf("lookup SNR %.1f dB, want >= 50 (quantization only)", s)
	}
	// Loop wraps around.
	wrapped := renderLookup(data, "loop", 128, len(data)+3)
	if !bytes.Equal(wrapped[len(data):], data[:3]) {
		t.Fatal("loop wrap-around mismatch")
	}
}

func TestGenerateLookupCode(t *testing.T) {
	code := generateLookupCode([]byte{0x80, 0xff, 0x00}, LookupOptions{Encoding: "hex", Loop: "loop", Center: 128})
	if !strings.Contains(code, "80ff00") || !strings.Contains(code, "parseInt") {
		t.Fatalf("unexpected lookup code: %s", code)
	}
	code = generateLookupCode([]byte{1, 2, 3}, LookupOptions{Encoding: "array", Loop: "silence", Center: 128})
	if code != "(t<3?[1,2,3][t]:128)" {
		t.Fatalf("unexpected array code: %s", code)
	}
}

func TestFFTFindsTone(t *testing.T) {
	const rate, frame = 8000, 512
	mags := fftMagnitudes(sine(rate, 440, float64(frame)/rate))
	best := 0
	for i := 1; i < len(mags); i++ {
		if mags[i] > mags[best] {
			best = i
		}
	}
	wantBin := int(math.Round(440 * frame / rate)) // 28
	if math.Abs(float64(best-wantBin)) > 1 {
		t.Fatalf("FFT peak bin %d, want ~%d (440 Hz)", best, wantBin)
	}
}

func TestSynthDetectsTone(t *testing.T) {
	const rate = 8000
	sd := analyzeSynth(sine(rate, 440, 0.25), rate, 512, 4)
	// Mid frame should be dominated by ~440 Hz.
	f := len(sd.Freqs) / 2
	best, bestAmp := 0.0, -1.0
	for p, amp := range sd.Amps[f] {
		if amp > bestAmp {
			bestAmp, best = amp, sd.Freqs[f][p]
		}
	}
	if math.Abs(best-440) > 20 {
		t.Fatalf("synth dominant freq %.1f Hz, want ~440", best)
	}
	rendered := renderSynth(sd, 2000)
	if len(rendered) != 2000 {
		t.Fatalf("rendered length %d", len(rendered))
	}
	if s := snrDB(sine(rate, 440, 0.25), rendered); s < 12 {
		t.Fatalf("synth SNR %.1f dB, want >= 12 (phase-tracked resynthesis)", s)
	}
	for _, s := range rendered {
		if math.Abs(s) > 1.001 {
			t.Fatalf("synth output out of range: %v", s)
		}
	}
	code := generateSynthCode(sd, 128)
	if !strings.HasPrefix(code, "(f=[") || !strings.Contains(code, "cos(6.2832") {
		t.Fatalf("unexpected synth code (truncated): %s", code[:minInt(120, len(code))])
	}
}

func TestTrimAndNormalize(t *testing.T) {
	samples := []float64{0, 0, 0.1, -0.5, 0.2, 0, 0}
	trimmed := trimSilence(samples, 0.01)
	if len(trimmed) != 3 {
		t.Fatalf("trimmed length %d, want 3", len(trimmed))
	}
	n := normalizePeak([]float64{0.25, -0.5}, 1)
	if n[1] != -1 || n[0] != 0.5 {
		t.Fatalf("normalize = %v", n)
	}
}

func TestConvertEndToEnd(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "tone.wav")
	if err := writeWAV(in, sine(44100, 440, 0.25), 44100); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.js")
	preview := filepath.Join(dir, "preview.wav")
	var buf bytes.Buffer
	opts := defaultOptions()
	opts.output = out
	opts.preview = preview
	if err := doConvert(in, opts, &buf); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "parseInt") {
		t.Fatalf("output missing bytebeat code:\n%s", content)
	}
	if _, err := decodeWAV(preview); err != nil {
		t.Fatalf("preview wav invalid: %v", err)
	}
	// Flags may come after the input file.
	flags, pos := reorderArgs([]string{in, "-rate", "16000", "-trim"}, boolFlags)
	if len(pos) != 1 || pos[0] != in || len(flags) != 3 {
		t.Fatalf("reorderArgs = flags %v pos %v", flags, pos)
	}
}

func TestBytebeatFileCodeIsNotCommented(t *testing.T) {
	code := generateLookupCode([]byte{128, 255}, LookupOptions{Encoding: "hex", Loop: "loop", Center: 128})
	content := bytebeatFile("song.wav", code, 8000, 2, "lookup (hex, loop)")
	if !strings.Contains(content, "\n"+code+"\n") {
		t.Fatalf("code must appear on its own line (not after //):\n%s", content)
	}
}

func TestValidateOptions(t *testing.T) {
	opts := defaultOptions()
	if err := opts.validate(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
	opts.rate = 100
	if err := opts.validate(); err == nil {
		t.Fatal("expected error for tiny rate")
	}
	opts = defaultOptions()
	opts.mode = "synth"
	opts.frameSize = 1000 // not a power of two
	if err := opts.validate(); err == nil {
		t.Fatal("expected error for non-power-of-two frame")
	}
}
