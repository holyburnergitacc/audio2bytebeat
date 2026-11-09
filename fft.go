package main

import (
	"math"
	"math/cmplx"
)

// fftMagnitudes returns the magnitude spectrum (bins 0..N/2) of a frame
// after applying a Hann window. len(frame) must be a power of two.
func fftMagnitudes(frame []float64) []float64 {
	mags, _ := fftSpectrum(frame)
	return mags
}

// fftSpectrum returns magnitudes and phases (bins 0..N/2) of a Hann-windowed frame.
// Phases are the angles of the FFT bins and are used by the synth mode to
// reconstruct each frame with the correct starting phase.
func fftSpectrum(frame []float64) (mags, phases []float64) {
	n := len(frame)
	if n == 0 || !isPowerOfTwo(n) {
		return nil, nil
	}
	x := make([]complex128, n)
	for i, s := range frame {
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
		x[i] = complex(s*w, 0)
	}
	fft(x)
	mags = make([]float64, n/2+1)
	phases = make([]float64, n/2+1)
	for i := 0; i <= n/2; i++ {
		mags[i] = cmplx.Abs(x[i])
		phases[i] = cmplx.Phase(x[i])
	}
	return mags, phases
}

// fft is an in-place iterative radix-2 Cooley-Tukey FFT.
func fft(x []complex128) {
	n := len(x)
	// Bit-reversal permutation.
	j := 0
	for i := 1; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j &^= bit
		}
		j |= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		angle := -2 * math.Pi / float64(size)
		wlen := complex(math.Cos(angle), math.Sin(angle))
		for i := 0; i < n; i += size {
			w := complex(1, 0)
			for k := 0; k < size/2; k++ {
				u := x[i+k]
				v := x[i+k+size/2] * w
				x[i+k] = u + v
				x[i+k+size/2] = u - v
				w *= wlen
			}
		}
	}
}
