package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const version = "1.0.0"

type convertOptions struct {
	rate       int
	mode       string
	encoding   string
	loop       string
	center     int
	gainDB     float64
	normalize  bool
	peak       float64
	trim       bool
	trimThresh float64
	start      float64
	duration   float64
	frameSize  int
	partials   int
	output     string
	preview    string
	html       string
	verify     bool
}

func defaultOptions() convertOptions {
	return convertOptions{
		rate:       8000,
		mode:       "lookup",
		encoding:   "hex",
		loop:       "loop",
		center:     128,
		peak:       0.99,
		normalize:  true,
		trimThresh: 0.002,
		frameSize:  512,
		partials:   4,
		verify:     true,
	}
}

func (o convertOptions) validate() error {
	if o.rate < 1000 || o.rate > 192000 {
		return fmt.Errorf("-rate must be between 1000 and 192000, got %d", o.rate)
	}
	if o.center < 0 || o.center > 255 {
		return fmt.Errorf("-center must be between 0 and 255, got %d", o.center)
	}
	if o.mode != "lookup" && o.mode != "synth" {
		return fmt.Errorf("-mode must be lookup or synth, got %q", o.mode)
	}
	if o.mode == "lookup" {
		if err := validateLookupOptions(LookupOptions{Encoding: o.encoding, Loop: o.loop, Center: o.center}); err != nil {
			return err
		}
	}
	if o.mode == "synth" {
		if !isPowerOfTwo(o.frameSize) || o.frameSize < 64 || o.frameSize > 8192 {
			return fmt.Errorf("-frame must be a power of two between 64 and 8192, got %d", o.frameSize)
		}
		if o.partials < 1 || o.partials > 16 {
			return fmt.Errorf("-partials must be between 1 and 16, got %d", o.partials)
		}
	}
	if o.peak <= 0 || o.peak > 1 {
		return fmt.Errorf("-peak must be in (0, 1], got %v", o.peak)
	}
	return nil
}

// processAudio decodes and processes an input file into mono samples at opts.rate.
func processAudio(path string, opts convertOptions) ([]float64, *decodedAudio, error) {
	src, err := decodeAudio(path)
	if err != nil {
		return nil, nil, err
	}
	a := cropAudio(src, opts.start, opts.duration)
	a = resampleAudio(a, opts.rate)
	mono := downmixToMono(a)
	if opts.trim && len(mono) > 0 {
		mono = trimSilence(mono, opts.trimThresh)
	}
	mono = applyGain(mono, opts.gainDB)
	if opts.normalize {
		mono = normalizePeak(mono, opts.peak)
	}
	if len(mono) == 0 {
		return nil, src, fmt.Errorf("%s: no samples after processing (check -start/-duration)", path)
	}
	return mono, src, nil
}

func runConvert(args []string, stdout, stderr io.Writer) error {
	opts := defaultOptions()
	fs := flag.NewFlagSet("convert", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.IntVar(&opts.rate, "rate", opts.rate, "bytebeat sample rate in Hz (8000 classic, 44100 faithful)")
	fs.StringVar(&opts.mode, "mode", opts.mode, "lookup (faithful byte table) or synth (sine-wave approximation)")
	fs.StringVar(&opts.encoding, "encoding", opts.encoding, "lookup encoding: hex, base64, or array")
	fs.StringVar(&opts.loop, "loop", opts.loop, "end-of-data behaviour: loop, hold, or silence")
	fs.IntVar(&opts.center, "center", opts.center, "byte value for silence (128)")
	fs.Float64Var(&opts.gainDB, "gain", opts.gainDB, "gain in decibels, applied before normalize")
	fs.BoolVar(&opts.normalize, "normalize", opts.normalize, "normalize peak to -peak")
	fs.Float64Var(&opts.peak, "peak", opts.peak, "normalization peak in (0, 1]")
	fs.BoolVar(&opts.trim, "trim", opts.trim, "trim leading/trailing silence")
	fs.Float64Var(&opts.trimThresh, "trim-threshold", opts.trimThresh, "silence threshold for -trim")
	fs.Float64Var(&opts.start, "start", opts.start, "start offset in seconds")
	fs.Float64Var(&opts.duration, "duration", opts.duration, "duration in seconds (0 = to end)")
	fs.IntVar(&opts.frameSize, "frame", opts.frameSize, "synth frame size (power of two)")
	fs.IntVar(&opts.partials, "partials", opts.partials, "synth sinusoids per frame")
	fs.StringVar(&opts.output, "o", "", "output .js file (default: <input>.bytebeat.js)")
	fs.StringVar(&opts.output, "output", "", "output .js file (default: <input>.bytebeat.js)")
	fs.StringVar(&opts.preview, "preview", "", "also write a rendered preview WAV of what the bytebeat will sound like")
	fs.StringVar(&opts.html, "html", "", "also write a playable HTML page")
	fs.BoolVar(&opts.verify, "verify", opts.verify, "simulate the generated code and report SNR vs the processed audio")
	flags, pos := reorderArgs(args, boolFlags)
	if err := fs.Parse(flags); err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: bytebeat convert [flags] <input-audio> (run with -h for flags)")
	}
	input := pos[0]
	if opts.output == "" {
		opts.output = defaultOutputName(input, ".bytebeat.js")
	}
	return doConvert(input, opts, stdout)
}

func doConvert(input string, opts convertOptions, stdout io.Writer) error {
	if err := opts.validate(); err != nil {
		return err
	}
	mono, src, err := processAudio(input, opts)
	if err != nil {
		return err
	}
	peak, rms := peakAndRMS(mono)

	var code, modeDesc string
	var rendered []float64
	switch opts.mode {
	case "lookup":
		data := quantize(mono, opts.center)
		code = generateLookupCode(data, LookupOptions{Encoding: opts.encoding, Loop: opts.loop, Center: opts.center})
		modeDesc = fmt.Sprintf("lookup (%s, %s)", opts.encoding, opts.loop)
		rendered = dequantize(renderLookup(data, opts.loop, opts.center, len(mono)), opts.center)
	case "synth":
		sd := analyzeSynth(mono, opts.rate, opts.frameSize, opts.partials)
		code = generateSynthCode(sd, opts.center)
		modeDesc = fmt.Sprintf("synth (%d partials/frame, frame=%d)", opts.partials, opts.frameSize)
		rendered = renderSynth(sd, len(mono))
	}

	content := bytebeatFile(input, code, opts.rate, len(mono), modeDesc)
	if err := os.WriteFile(opts.output, []byte(content), 0o644); err != nil {
		return err
	}

	if opts.preview != "" {
		if err := writeWAV(opts.preview, rendered, opts.rate); err != nil {
			return fmt.Errorf("write preview: %w", err)
		}
	}
	if opts.html != "" {
		title := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
		if err := os.WriteFile(opts.html, []byte(generateHTML(title, code, opts.rate, len(mono))), 0o644); err != nil {
			return fmt.Errorf("write html: %w", err)
		}
	}

	fmt.Fprintf(stdout, "input:    %s (%d Hz, %d ch, %.2f s)\n", input, src.SampleRate, src.Channels, float64(src.frames())/float64(src.SampleRate))
	fmt.Fprintf(stdout, "output:   %s (%d bytes, %s)\n", opts.output, len(content), modeDesc)
	fmt.Fprintf(stdout, "rate:     %d Hz, %d samples, %.2f s, peak %.3f, RMS %.3f\n", opts.rate, len(mono), float64(len(mono))/float64(opts.rate), peak, rms)
	if opts.verify {
		fmt.Fprintf(stdout, "SNR:      %.1f dB (rendered vs processed audio)\n", snrDB(mono, rendered))
	}
	if opts.preview != "" {
		fmt.Fprintf(stdout, "preview:  %s\n", opts.preview)
	}
	if opts.html != "" {
		fmt.Fprintf(stdout, "html:     %s\n", opts.html)
	}
	if opts.mode == "lookup" && len(mono) > 80000 {
		fmt.Fprintln(stdout, "note: large output - consider -rate 8000 or -mode synth for long songs")
	}
	return nil
}

func runPreview(args []string, stdout, stderr io.Writer) error {
	opts := defaultOptions()
	fs := flag.NewFlagSet("preview", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.IntVar(&opts.rate, "rate", opts.rate, "bytebeat sample rate in Hz")
	fs.StringVar(&opts.mode, "mode", opts.mode, "lookup or synth")
	fs.StringVar(&opts.encoding, "encoding", opts.encoding, "lookup encoding: hex, base64, or array")
	fs.StringVar(&opts.loop, "loop", opts.loop, "end-of-data behaviour: loop, hold, or silence")
	fs.IntVar(&opts.center, "center", opts.center, "byte value for silence (128)")
	fs.Float64Var(&opts.gainDB, "gain", opts.gainDB, "gain in decibels")
	fs.BoolVar(&opts.normalize, "normalize", opts.normalize, "normalize peak to -peak")
	fs.Float64Var(&opts.peak, "peak", opts.peak, "normalization peak in (0, 1]")
	fs.BoolVar(&opts.trim, "trim", opts.trim, "trim leading/trailing silence")
	fs.Float64Var(&opts.trimThresh, "trim-threshold", opts.trimThresh, "silence threshold for -trim")
	fs.Float64Var(&opts.start, "start", opts.start, "start offset in seconds")
	fs.Float64Var(&opts.duration, "duration", opts.duration, "duration in seconds (0 = to end)")
	fs.IntVar(&opts.frameSize, "frame", opts.frameSize, "synth frame size (power of two)")
	fs.IntVar(&opts.partials, "partials", opts.partials, "synth sinusoids per frame")
	fs.StringVar(&opts.output, "o", "", "output preview WAV file")
	fs.StringVar(&opts.output, "output", "", "output preview WAV file")
	flags, pos := reorderArgs(args, boolFlags)
	if err := fs.Parse(flags); err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: bytebeat preview [flags] <input-audio> (run with -h for flags)")
	}
	input := pos[0]
	if err := opts.validate(); err != nil {
		return err
	}
	mono, _, err := processAudio(input, opts)
	if err != nil {
		return err
	}
	var rendered []float64
	switch opts.mode {
	case "lookup":
		data := quantize(mono, opts.center)
		rendered = dequantize(renderLookup(data, opts.loop, opts.center, len(mono)), opts.center)
	case "synth":
		sd := analyzeSynth(mono, opts.rate, opts.frameSize, opts.partials)
		rendered = renderSynth(sd, len(mono))
	}
	if opts.output == "" {
		opts.output = defaultOutputName(input, ".preview.wav")
	}
	if err := writeWAV(opts.output, rendered, opts.rate); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "preview: %s (%d Hz, %.2f s, SNR %.1f dB)\n",
		opts.output, opts.rate, float64(len(rendered))/float64(opts.rate), snrDB(mono, rendered))
	return nil
}

func runInfo(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("info", flag.ContinueOnError)
	fs.SetOutput(stderr)
	flags, pos := reorderArgs(args, boolFlags)
	if err := fs.Parse(flags); err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: bytebeat info <input-audio>")
	}
	src, err := decodeAudio(pos[0])
	if err != nil {
		return err
	}
	dur := float64(src.frames()) / float64(src.SampleRate)
	peak, rms := peakAndRMS(downmixToMono(src))
	fmt.Fprintf(stdout, "file:     %s\n", pos[0])
	fmt.Fprintf(stdout, "rate:     %d Hz, channels: %d, samples: %d, duration: %.2f s\n", src.SampleRate, src.Channels, src.frames(), dur)
	fmt.Fprintf(stdout, "level:    peak %.3f, RMS %.3f\n", peak, rms)
	for _, r := range []int{8000, 16000, 44100, 48000} {
		n := int(dur * float64(r))
		fmt.Fprintf(stdout, "at %5d Hz: %7d samples  (~%d KB lookup output)\n", r, n, (n*2+512)/1024)
	}
	return nil
}

var boolFlags = map[string]bool{
	"normalize": true, "trim": true, "verify": true,
	"normalize=true": true, "trim=true": true, "verify=true": true,
	"normalize=false": true, "trim=false": true, "verify=false": true,
	"h": true, "help": true,
}

// reorderArgs moves flags (and their values) before positional arguments so
// users may write `bytebeat convert song.wav -rate 8000` as well as
// `bytebeat convert -rate 8000 song.wav`.
func reorderArgs(args []string, bools map[string]bool) (flags, positionals []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			positionals = append(positionals, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue
		}
		if bools[name] {
			continue
		}
		if i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return flags, positionals
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `bytebeat-convert - turn audio into bytebeat code

Usage:
  bytebeat convert [flags] <input-audio>   generate bytebeat .js (default)
  bytebeat preview [flags] <input-audio>   render what the bytebeat sounds like (.wav)
  bytebeat info <input-audio>              print audio properties
  bytebeat <input-audio>                   shorthand for convert
  bytebeat -h                              this help

Input: WAV natively; mp3/flac/ogg via ffmpeg (if in PATH).
Output: a .js file whose last line is a bytebeat expression. Paste it
into a bytebeat player and set the player's sample rate to -rate.

Common flags:
  -rate N        sample rate (default 8000; 44100 for fidelity, huge output)
  -mode MODE     lookup (default, faithful) or synth (compact sine waves)
  -encoding ENC  hex (default), base64, or array
  -loop MODE     loop (default), hold, or silence after the audio ends
  -o FILE        output file (default: <input>.bytebeat.js)
  -preview FILE  also write a WAV preview of the generated bytebeat
  -html FILE     also write a playable HTML page
  -trim          trim leading/trailing silence
  -start S -duration S   cut a segment (seconds)
  -gain DB       gain before normalization
  -normalize=false       disable peak normalization

Run without arguments for full flag help: bytebeat convert -h
`)
}

func runCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stdout)
		return 0
	}
	var err error
	switch args[0] {
	case "convert":
		err = runConvert(args[1:], stdout, stderr)
	case "preview", "render":
		err = runPreview(args[1:], stdout, stderr)
	case "info":
		err = runInfo(args[1:], stdout, stderr)
	case "-h", "help", "--help":
		printUsage(stdout)
	case "-version", "--version":
		fmt.Fprintf(stdout, "bytebeat-convert %s\n", version)
	default:
		// No subcommand: treat the whole invocation as `convert`.
		err = runConvert(args, stdout, stderr)
	}
	if err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}
