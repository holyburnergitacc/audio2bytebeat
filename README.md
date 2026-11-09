# bytebeat-convert

Turn any audio file into **bytebeat** code — the tiny JavaScript expression
format where a function of a sample counter `t` produces one unsigned
8-bit sample per tick.

```
bytebeat song.wav        ->  song.bytebeat.js
```

Paste the last line of the `.js` file into a bytebeat player, set the
player's sample rate to the rate you converted at, and it plays your audio.

## Build & test

Requires Go (stdlib only, no modules to download):

```sh
go build -o bytebeat .
go test ./...
```

## Usage

```sh
bytebeat convert [flags] <input-audio>   # default subcommand
bytebeat preview [flags] <input-audio>   # render a .wav of what it sounds like
bytebeat info <input-audio>              # duration/level + estimated sizes
bytebeat <input-audio>                   # shorthand for convert
```

Flags may come before **or after** the input file:

```sh
bytebeat convert song.wav -rate 8000 -o song.js -preview song-preview.wav
bytebeat convert -rate 44100 -mode synth -html song.html song.wav
```

### Input formats

WAV is decoded natively (PCM 8/16/24/32-bit and IEEE float, mono/stereo).
MP3, FLAC, OGG, etc. work too when `ffmpeg` is in your `PATH` — the tool
hands the file to ffmpeg for decoding, then processes the result itself.

### Modes

| Mode | What it does | Size | Fidelity |
| --- | --- | --- | --- |
| `lookup` (default) | one table entry per sample; the expression indexes the table by `t` | ~2 chars/sample (hex) | near-perfect (quantization + resampling only) |
| `synth` | FFT analysis; each frame becomes a few sine waves (freq/amp/phase tables) | a few KB per minute-ish | lo-fi, vocoder-like approximation |

Lookup encodings:

- `hex` (default): `parseInt("8cabd0...".slice((t%8000)*2,(t%8000)*2+2),16)` —
  compact, needs only `parseInt`.
- `base64`: `atob("...").charCodeAt(t%8000)` — smallest, needs browser `atob`.
- `array`: `[140,171,208,...][t%8000]` — most readable / most compatible,
  largest.

End-of-audio behaviour (`-loop`): `loop` (wrap around, default), `hold`
(stay on the last sample), or `silence` (output the silence byte).

Synth mode is a single expression too, e.g.:

```js
(f=[439.4,...],g=[1.0,...],a=[1.0,...],p=[-89.2,...],
 128+127*g[((t/512)|0)%N]*(a[...]*cos(6.2832*f[...]*...)+...))
```

`t` selects the current frame; each sinusoid restarts at the frame-local
sample with the FFT phase measured at that frame, so phase doesn't drift.

### Key flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `-rate N` | `8000` | bytebeat sample rate. `8000` is the classic bytebeat rate and keeps output small; `44100` is faithful but ~5.5× bigger. **The player must use the same rate.** |
| `-mode lookup\|synth` | `lookup` | |
| `-encoding hex\|base64\|array` | `hex` | lookup table encoding |
| `-loop loop\|hold\|silence` | `loop` | |
| `-o FILE` | `<input>.bytebeat.js` | output `.js` |
| `-preview FILE` | — | also write a WAV preview (simulates the generated code) |
| `-html FILE` | — | also write a self-contained playable HTML page (Web Audio) |
| `-trim` | off | trim leading/trailing silence |
| `-start S -duration S` | `0 0` | cut a segment (seconds; duration 0 = to end) |
| `-gain DB` | `0` | gain before normalization |
| `-normalize=false` | on | disable peak normalization |
| `-center N` | `128` | byte value for silence |
| `-frame N -partials N` | `512 4` | synth: frame size (power of two) and sinusoids per frame |

Each conversion prints an SNR figure: a simulation of the generated
expression compared against the processed audio. Lookup mode typically
scores ~50 dB (pure 8-bit quantization); synth mode is intentionally lo-fi.

### Example

```sh
# 440 Hz test tone -> bytebeat
ffmpeg -f lavfi -i "sine=frequency=440:duration=1:sample_rate=44100" demo.wav
bytebeat convert demo.wav -rate 8000
# -> demo.bytebeat.js  (16 KB of hex, one line, loops a 1 s tone)
```

## How it works

1. **Decode** WAV (or via ffmpeg).
2. **Crop / resample** to the bytebeat rate (linear interpolation, with a
   box low-pass anti-alias filter when downsampling) and **downmix to mono**
   (bytebeat is mono).
3. **Trim / gain / normalize**, then **quantize** to bytes:
   `byte = clamp(round(sample*127) + 128)`, so silence is `128`.
4. **Generate code**: lookup mode embeds the bytes as hex/base64/array
   and indexes them with `t % N`; synth mode runs a radix-2 FFT per frame
   (Hann window), keeps the strongest peaks with parabolically
   interpolated frequencies and phase-corrected phases, and emits sine
   tables.
5. **Verify**: the exact generated expression is re-simulated in Go and
   written out as a preview WAV so you can listen before pasting it
   anywhere.

## Limitations

- **Mono only** — stereo is averaged down.
- **Loops**: playback past the end wraps to the start, so a song repeats
  (select `-loop hold` or `silence` if you prefer).
- **Sample rate is part of the format**: the same code at the wrong player
  rate plays at the wrong speed/pitch.
- **Lookup size** grows with duration × rate: at 8000 Hz, ~16 KB/minute;
  at 44100 Hz, ~86 KB/minute (hex). Use `-mode synth` or a shorter
  `-duration` for long songs.
- Synth mode is an approximation: it captures pitch, rhythm and loudness
  well, but not noise, reverb, or dense polyphony.

## Files

| File | Contents |
| --- | --- |
| `main.go` | CLI: `convert`, `preview`, `info` subcommands |
| `wav.go` | WAV decode/encode + ffmpeg fallback |
| `audio.go` | resampling, downmix, trim, normalize, quantize, SNR |
| `fft.go` | iterative radix-2 FFT (magnitude + phase) |
| `synth.go` | spectral analysis, synth code generation & simulation |
| `bytebeat.go` | lookup code generation, file header, playable HTML template |
| `main_test.go` | tests (WAV round-trip, FFT, synth, end-to-end convert) |

## License
MIT