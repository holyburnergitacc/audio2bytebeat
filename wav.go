package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
)

// decodedAudio holds interleaved floating-point samples in [-1, 1].
type decodedAudio struct {
	Samples    []float64 // interleaved by channel
	SampleRate int
	Channels   int
}

func (a *decodedAudio) frames() int {
	if a.Channels == 0 {
		return 0
	}
	return len(a.Samples) / a.Channels
}

// decodeAudio decodes a WAV file, and falls back to ffmpeg for other
// formats (mp3, flac, ogg, ...) when ffmpeg is available in PATH.
func decodeAudio(path string) (*decodedAudio, error) {
	audio, err := decodeWAV(path)
	if err == nil {
		return audio, nil
	}
	ffmpeg, lookErr := exec.LookPath("ffmpeg")
	if lookErr != nil {
		return nil, fmt.Errorf("%s: %w (install ffmpeg to decode non-WAV files)", path, err)
	}
	tmp, tmpErr := os.CreateTemp("", "bytebeat-*.wav")
	if tmpErr != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	tmpName := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpName)
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-i", path, "-f", "wav", "-acodec", "pcm_s16le", tmpName)
	if out, cmdErr := cmd.CombinedOutput(); cmdErr != nil {
		return nil, fmt.Errorf("decode %s: ffmpeg: %w: %s", path, cmdErr, string(out))
	}
	audio, err = decodeWAV(tmpName)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return audio, nil
}

// decodeWAV parses a RIFF/WAVE file (PCM and IEEE-float, 8-64 bit).
func decodeWAV(path string) (*decodedAudio, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("not a RIFF/WAVE file")
	}

	var (
		format     uint16
		channels   uint16
		sampleRate uint32
		bits       uint16
		raw        []byte
		haveFmt    bool
		haveData   bool
	)

	// Iterate RIFF chunks, honouring the pad byte for odd-sized chunks.
	for pos := 12; pos+8 <= len(data); {
		id := string(data[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		body := pos + 8
		end := body + size
		if end > len(data) {
			end = len(data)
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, fmt.Errorf("malformed fmt chunk")
			}
			format = binary.LittleEndian.Uint16(data[body : body+2])
			channels = binary.LittleEndian.Uint16(data[body+2 : body+4])
			sampleRate = binary.LittleEndian.Uint32(data[body+4 : body+8])
			bits = binary.LittleEndian.Uint16(data[body+14 : body+16])
			if format == 0xFFFE && size >= 40 { // WAVE_FORMAT_EXTENSIBLE
				// SubFormat GUID: first two bytes hold the real format tag.
				format = binary.LittleEndian.Uint16(data[body+24 : body+26])
			}
			haveFmt = true
		case "data":
			raw = data[body:end]
			haveData = true
		}
		pos = body + size
		if size%2 == 1 {
			pos++
		}
	}
	if !haveFmt {
		return nil, fmt.Errorf("missing fmt chunk")
	}
	if !haveData {
		return nil, fmt.Errorf("missing data chunk")
	}
	if channels == 0 || sampleRate == 0 {
		return nil, fmt.Errorf("invalid fmt chunk (channels=%d rate=%d)", channels, sampleRate)
	}

	bytesPerSample := int(bits) / 8
	if bytesPerSample == 0 {
		return nil, fmt.Errorf("invalid bits per sample %d", bits)
	}
	nSamples := len(raw) / bytesPerSample
	samples := make([]float64, nSamples)
	for i := 0; i < nSamples; i++ {
		b := raw[i*bytesPerSample : (i+1)*bytesPerSample]
		switch {
		case format == 3 && bits == 32: // IEEE float
			samples[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
		case format == 3 && bits == 64: // IEEE double
			samples[i] = math.Float64frombits(binary.LittleEndian.Uint64(b))
		case bits == 8: // unsigned 8-bit PCM
			samples[i] = (float64(b[0]) - 128) / 128
		case bits == 16:
			samples[i] = float64(int16(binary.LittleEndian.Uint16(b))) / 32768
		case bits == 24:
			v := int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16
			if v&0x800000 != 0 {
				v |= ^0xFFFFFF
			}
			samples[i] = float64(v) / 8388608
		case bits == 32:
			samples[i] = float64(int32(binary.LittleEndian.Uint32(b))) / 2147483648
		default:
			return nil, fmt.Errorf("unsupported WAV format (format=%d bits=%d)", format, bits)
		}
	}
	return &decodedAudio{Samples: samples, SampleRate: int(sampleRate), Channels: int(channels)}, nil
}

// writeWAV writes mono float samples in [-1, 1] as a 16-bit PCM WAV file.
func writeWAV(path string, samples []float64, sampleRate int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dataSize := len(samples) * 2
	_ = f.Chmod(0o644)

	hdr := make([]byte, 44)
	copy(hdr[0:4], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(36+dataSize))
	copy(hdr[8:12], "WAVE")
	copy(hdr[12:16], "fmt ")
	binary.LittleEndian.PutUint32(hdr[16:20], 16)
	binary.LittleEndian.PutUint16(hdr[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(hdr[22:24], 1) // mono
	binary.LittleEndian.PutUint32(hdr[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(hdr[28:32], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(hdr[32:34], 2)
	binary.LittleEndian.PutUint16(hdr[34:36], 16)
	copy(hdr[36:40], "data")
	binary.LittleEndian.PutUint32(hdr[40:44], uint32(dataSize))
	if _, err := f.Write(hdr); err != nil {
		return err
	}
	buf := make([]byte, 2)
	for _, s := range samples {
		v := math.Max(-1, math.Min(1, s))
		binary.LittleEndian.PutUint16(buf, uint16(int16(math.Round(v*32767))))
		if _, err := f.Write(buf); err != nil {
			return err
		}
	}
	return nil
}

func defaultOutputName(input, ext string) string {
	base := filepath.Base(input)
	base = base[:len(base)-len(filepath.Ext(base))]
	return base + ext
}
