package svg2gif

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	converter "github.com/jhyan/design-toolbox/internal/svg2gif/converter"
)

const testSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="40" height="30">` +
	`<rect width="40" height="30" fill="#ff0000"/></svg>`

func TestSVGConvertsToSingleFrame(t *testing.T) {
	frames, delays, err := converter.NewSVGConverter().Convert(
		strings.NewReader(testSVG),
		converter.Options{FPS: 20},
	)
	if err != nil {
		t.Fatalf("SVG conversion failed: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1 (static SVG)", len(frames))
	}
	if bounds := frames[0].Bounds(); bounds.Dx() != 40 || bounds.Dy() != 30 {
		t.Fatalf("frame bounds = %v, want 40x30", bounds)
	}
	if len(delays) != 1 {
		t.Fatalf("delay count = %d, want 1", len(delays))
	}
}

func TestSVGAConvertsToFrames(t *testing.T) {
	data := craftSVGA(t, 8, 8, 2, 10, map[string][]byte{
		"a": makeTestPNG(t, 2, color.RGBA{R: 0xff, A: 0xff}),
		"b": makeTestPNG(t, 2, color.RGBA{B: 0xff, A: 0xff}),
	})

	frames, delays, err := converter.NewSVGAConverter().Convert(
		bytes.NewReader(data),
		converter.Options{FPS: 10},
	)
	if err != nil {
		t.Fatalf("SVGA conversion failed: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("frame count = %d, want 2", len(frames))
	}
	for index, frame := range frames {
		if bounds := frame.Bounds(); bounds.Dx() != 8 || bounds.Dy() != 8 {
			t.Fatalf("frame %d bounds = %v, want 8x8", index, bounds)
		}
	}
	if len(delays) != 2 || delays[0] != 10 || delays[1] != 10 {
		t.Fatalf("delays = %v, want [10 10] (fps 10)", delays)
	}
}

func TestConvertFileWithoutFFmpegProducesAPNGAndGIF(t *testing.T) {
	testDirectory := t.TempDir()
	outputDirectory := filepath.Join(testDirectory, "output")

	svgPath := filepath.Join(testDirectory, "static.svg")
	if err := os.WriteFile(svgPath, []byte(testSVG), 0o644); err != nil {
		t.Fatalf("write SVG fixture: %v", err)
	}
	svgaPath := filepath.Join(testDirectory, "animated.svga")
	if err := os.WriteFile(svgaPath, craftSVGA(t, 8, 8, 2, 10, map[string][]byte{
		"a": makeTestPNG(t, 2, color.RGBA{R: 0xff, A: 0xff}),
		"b": makeTestPNG(t, 2, color.RGBA{B: 0xff, A: 0xff}),
	}), 0o644); err != nil {
		t.Fatalf("write SVGA fixture: %v", err)
	}

	cases := []struct {
		name        string
		inputPath   string
		gifFrames   int
		gifMinDelay int
	}{
		{name: "svg", inputPath: svgPath, gifFrames: 1},
		{name: "svga", inputPath: svgaPath, gifFrames: 2, gifMinDelay: 5},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			baseName := strings.TrimSuffix(filepath.Base(tt.inputPath), filepath.Ext(tt.inputPath))
			apngOutput := filepath.Join(outputDirectory, "apng", baseName+".png")
			gifOutput := filepath.Join(outputDirectory, "gif", baseName+".gif")

			err := ConvertFile(tt.inputPath, apngOutput, gifOutput, Options{FPS: 10}, "")
			if err != nil {
				t.Fatalf("ConvertFile failed: %v", err)
			}

			apngFile, err := os.Open(apngOutput)
			if err != nil {
				t.Fatalf("open APNG output: %v", err)
			}
			_, decodeErr := png.Decode(apngFile)
			closeErr := apngFile.Close()
			if decodeErr != nil {
				t.Fatalf("APNG output is not a valid PNG: %v", decodeErr)
			}
			if closeErr != nil {
				t.Fatalf("close APNG output: %v", closeErr)
			}

			gifFrames := readGIFFrames(t, gifOutput)
			if len(gifFrames.Image) != tt.gifFrames {
				t.Fatalf("GIF frame count = %d, want %d", len(gifFrames.Image), tt.gifFrames)
			}
			for _, delay := range gifFrames.Delay {
				if delay < tt.gifMinDelay {
					t.Fatalf("GIF delay = %d, want >= %d", delay, tt.gifMinDelay)
				}
			}
		})
	}
}

func TestConvertFileWithFFmpeg(t *testing.T) {
	ffmpegPath := DetectFFmpeg()
	if ffmpegPath == "" {
		t.Skip("ffmpeg is unavailable")
	}

	testDirectory := t.TempDir()
	inputPath := filepath.Join(testDirectory, "animated.svga")
	if err := os.WriteFile(inputPath, craftSVGA(t, 8, 8, 2, 10, map[string][]byte{
		"a": makeTestPNG(t, 2, color.RGBA{R: 0xff, A: 0xff}),
		"b": makeTestPNG(t, 2, color.RGBA{B: 0xff, A: 0xff}),
	}), 0o644); err != nil {
		t.Fatalf("write SVGA fixture: %v", err)
	}

	apngOutput := filepath.Join(testDirectory, "apng", "animated.png")
	gifOutput := filepath.Join(testDirectory, "gif", "animated.gif")
	if err := ConvertFile(inputPath, apngOutput, gifOutput, Options{FPS: 10}, ffmpegPath); err != nil {
		t.Fatalf("ConvertFile failed: %v", err)
	}

	gifFrames := readGIFFrames(t, gifOutput)
	if len(gifFrames.Image) < 1 {
		t.Fatalf("GIF frame count = %d, want >= 1", len(gifFrames.Image))
	}
}

func TestRunBatchConvertsAllFiles(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()

	fixtures := map[string]string{
		"one.svg":  testSVG,
		"two.svg":  testSVG,
		"note.txt": "not an SVG",
	}
	for name, content := range fixtures {
		if err := os.WriteFile(filepath.Join(source, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}

	if err := RunBatch(source, target, strings.NewReader("\n"), io.Discard); err != nil {
		t.Fatalf("RunBatch failed: %v", err)
	}

	for _, baseName := range []string{"one", "two"} {
		requireFileExists(t, filepath.Join(target, "gif", baseName+".gif"))
		requireFileExists(t, filepath.Join(target, "apng", baseName+".png"))
	}
	for _, name := range []string{"note.gif", "note.png"} {
		if _, err := os.Stat(filepath.Join(target, "gif", name)); !os.IsNotExist(err) {
			t.Fatalf("non-SVG input produced %s: %v", name, err)
		}
	}
}

func TestPromptForFPS(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{name: "default on empty", input: "\n", expected: 20},
		{name: "custom value", input: "24\n", expected: 24},
		{name: "retries invalid then accepts", input: "0\nabc\n30\n", expected: 30},
		{name: "EOF falls back to default", input: "", expected: 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output strings.Builder
			fps, err := promptForFPS(strings.NewReader(tt.input), &output)
			if err != nil {
				t.Fatalf("prompt failed: %v\n%s", err, output.String())
			}
			if fps != tt.expected {
				t.Fatalf("fps = %d, want %d", fps, tt.expected)
			}
		})
	}
}

func TestValidateOptions(t *testing.T) {
	valid := Options{Width: 100, Height: 100, FPS: 20}
	if err := validateOptions(&valid); err != nil {
		t.Fatalf("valid options rejected: %v", err)
	}

	invalid := Options{FPS: 0}
	if err := validateOptions(&invalid); err == nil {
		t.Fatal("fps 0 was accepted")
	}

	negative := Options{Width: -1, Height: 100, FPS: 20}
	if err := validateOptions(&negative); err == nil {
		t.Fatal("negative width was accepted")
	}
}

// craftSVGA builds a minimal SVGA file: a zlib-compressed protobuf Movie
// message carrying params and an images map, without sprites. The converter
// then treats each image as one frame.
func craftSVGA(
	t *testing.T,
	width, height, frameCount, fps int,
	images map[string][]byte,
) []byte {
	t.Helper()

	params := protoFixed32(1, float32(width))
	params = append(params, protoFixed32(2, float32(height))...)
	params = append(params, protoVarint(3, uint64(frameCount))...)
	params = append(params, protoVarint(4, uint64(fps))...)

	movie := protoString(1, "2.0")
	movie = append(movie, protoBytes(2, params)...)

	keys := make([]string, 0, len(images))
	for key := range images {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		entry := protoString(1, key)
		entry = append(entry, protoBytes(2, images[key])...)
		movie = append(movie, protoBytes(3, entry)...)
	}

	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write(movie); err != nil {
		t.Fatalf("compress SVGA payload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zlib writer: %v", err)
	}
	return compressed.Bytes()
}

func protoBytes(field int, data []byte) []byte {
	out := appendUvarint(nil, uint64(field)<<3|2)
	out = appendUvarint(out, uint64(len(data)))
	return append(out, data...)
}

func protoString(field int, value string) []byte {
	return protoBytes(field, []byte(value))
}

func protoFixed32(field int, value float32) []byte {
	out := appendUvarint(nil, uint64(field)<<3|5)
	var raw [4]byte
	binary.LittleEndian.PutUint32(raw[:], math.Float32bits(value))
	return append(out, raw[:]...)
}

func protoVarint(field int, value uint64) []byte {
	out := appendUvarint(nil, uint64(field)<<3|0)
	return append(out, appendUvarint(nil, value)...)
}

func appendUvarint(dst []byte, value uint64) []byte {
	for value >= 0x80 {
		dst = append(dst, byte(value)|0x80)
		value >>= 7
	}
	return append(dst, byte(value))
}

func makeTestPNG(t *testing.T, size int, pixel color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.SetRGBA(x, y, pixel)
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	return buffer.Bytes()
}

func readGIFFrames(t *testing.T, path string) *gif.GIF {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open GIF output: %v", err)
	}
	defer file.Close()
	decoded, decodeErr := gif.DecodeAll(file)
	if decodeErr != nil {
		t.Fatalf("decode GIF output %s: %v", path, decodeErr)
	}
	return decoded
}

func requireFileExists(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("%s is not a regular file", path)
	}
}
