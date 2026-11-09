package main

import (
	"math"
)

// cropAudio keeps [startSec, startSec+durationSec) of the audio.
// Negative start means 0; a negative or zero duration means "to the end".
func cropAudio(a *decodedAudio, startSec, durationSec float64) *decodedAudio {
	if startSec <= 0 && durationSec <= 0 {
		return a
	}
	start := int(math.Round(startSec * float64(a.SampleRate)))
	if start < 0 {
		start = 0
	}
	end := a.frames()
	if durationSec > 0 {
		end = start + int(math.Round(durationSec*float64(a.SampleRate)))
	}
	if start > end {
		start = end
	}
	if start > a.frames() {
		start = a.frames()
	}
	if end > a.frames() {
		end = a.frames()
	}
	out := &decodedAudio{
		Samples:    make([]float64, (end-start)*a.Channels),
		SampleRate: a.SampleRate,
		Channels:   a.Channels,
	}
	copy(out.Samples, a.Samples[start*a.Channels:end*a.Channels])
	return out
}

// resampleAudio resamples to targetRate with linear interpolation.
// When downsampling it first applies a simple box low-pass to reduce aliasing.
func resampleAudio(a *decodedAudio, targetRate int) *decodedAudio {
	if a.SampleRate == targetRate || a.SampleRate == 0 {
		return a
	}
	ratio := float64(a.SampleRate) / float64(targetRate)
	src := a
	if ratio > 1.5 {
		src = boxLowPass(a, int(math.Ceil(ratio)))
	}
	framesOut := int(math.Round(float64(src.frames()) / ratio))
	if framesOut < 1 {
		framesOut = 1
	}
	out := &decodedAudio{
		Samples:    make([]float64, framesOut*src.Channels),
		SampleRate: targetRate,
		Channels:   src.Channels,
	}
	for i := 0; i < framesOut; i++ {
		pos := float64(i) * ratio
		i0 := int(math.Floor(pos))
		frac := pos - float64(i0)
		i1 := i0 + 1
		if i1 >= src.frames() {
			i1 = src.frames() - 1
		}
		if i0 >= src.frames() {
			i0 = src.frames() - 1
		}
		for c := 0; c < src.Channels; c++ {
			s0 := src.Samples[i0*src.Channels+c]
			s1 := src.Samples[i1*src.Channels+c]
			out.Samples[i*src.Channels+c] = s0*(1-frac) + s1*frac
		}
	}
	return out
}

// boxLowPass is a moving-average filter used as a cheap anti-alias filter.
func boxLowPass(a *decodedAudio, width int) *decodedAudio {
	if width <= 1 || a.frames() == 0 {
		return a
	}
	half := width / 2
	out := &decodedAudio{
		Samples:    make([]float64, len(a.Samples)),
		SampleRate: a.SampleRate,
		Channels:   a.Channels,
	}
	for i := 0; i < a.frames(); i++ {
		for c := 0; c < a.Channels; c++ {
			sum, n := 0.0, 0
			for k := -half; k <= half; k++ {
				j := i + k
				if j >= 0 && j < a.frames() {
					sum += a.Samples[j*a.Channels+c]
					n++
				}
			}
			out.Samples[i*a.Channels+c] = sum / float64(n)
		}
	}
	return out
}

// downmixToMono averages all channels.
func downmixToMono(a *decodedAudio) []float64 {
	frames := a.frames()
	mono := make([]float64, frames)
	for i := 0; i < frames; i++ {
		sum := 0.0
		for c := 0; c < a.Channels; c++ {
			sum += a.Samples[i*a.Channels+c]
		}
		mono[i] = sum / float64(a.Channels)
	}
	return mono
}

// trimSilence removes leading/trailing samples below threshold.
func trimSilence(samples []float64, threshold float64) []float64 {
	start, end := 0, len(samples)
	for start < end && math.Abs(samples[start]) < threshold {
		start++
	}
	for end > start && math.Abs(samples[end-1]) < threshold {
		end--
	}
	return samples[start:end]
}

// applyGain applies a gain in decibels.
func applyGain(samples []float64, dB float64) []float64 {
	if dB == 0 {
		return samples
	}
	g := math.Pow(10, dB/20)
	out := make([]float64, len(samples))
	for i, s := range samples {
		out[i] = s * g
	}
	return out
}

// normalizePeak scales samples so the largest magnitude equals peak.
func normalizePeak(samples []float64, peak float64) []float64 {
	max := 0.0
	for _, s := range samples {
		if math.Abs(s) > max {
			max = math.Abs(s)
		}
	}
	if max == 0 {
		return samples
	}
	k := peak / max
	out := make([]float64, len(samples))
	for i, s := range samples {
		out[i] = s * k
	}
	return out
}

// quantize converts [-1, 1] samples to bytebeat bytes: silence is `center`
// (128 by convention), +1 is 255, -1 is 1.
func quantize(samples []float64, center int) []byte {
	out := make([]byte, len(samples))
	for i, s := range samples {
		v := int(math.Round(s*127)) + center
		if v < 0 {
			v = 0
		}
		if v > 255 {
			v = 255
		}
		out[i] = byte(v)
	}
	return out
}

// dequantize is the inverse of quantize.
func dequantize(data []byte, center int) []float64 {
	out := make([]float64, len(data))
	for i, b := range data {
		out[i] = (float64(b) - float64(center)) / 127
	}
	return out
}

// peakAndRMS reports basic statistics of a sample slice.
func peakAndRMS(samples []float64) (peak, rms float64) {
	if len(samples) == 0 {
		return 0, 0
	}
	var sum float64
	for _, s := range samples {
		if math.Abs(s) > peak {
			peak = math.Abs(s)
		}
		sum += s * s
	}
	return peak, math.Sqrt(sum / float64(len(samples)))
}

// snrDB computes signal-to-noise ratio of test relative to reference.
func snrDB(ref, test []float64) float64 {
	n := len(ref)
	if len(test) < n {
		n = len(test)
	}
	if n == 0 {
		return 0
	}
	var sig, err float64
	for i := 0; i < n; i++ {
		sig += ref[i] * ref[i]
		d := ref[i] - test[i]
		err += d * d
	}
	if err == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(sig/err)
}

// isPowerOfTwo reports whether n is a power of two >= 2.
func isPowerOfTwo(n int) bool {
	return n >= 2 && n&(n-1) == 0
}
