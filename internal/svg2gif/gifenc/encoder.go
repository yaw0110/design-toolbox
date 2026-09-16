// Package gifenc provides a dependency-free animated GIF encoder. The toolbox
// uses it as a fallback when ffmpeg is unavailable, so a single binary stays
// fully self-contained. Frames are mapped onto the Plan9 palette with
// Floyd-Steinberg error diffusion for noticeably better quality than plain
// nearest-color matching.
package gifenc

import (
	"image"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"io"
)

// EncodeFrames writes frames as a looping animated GIF. delays are expressed
// in 100ths of a second (the same unit the converters and APNG encoder use);
// entries that are missing fall back to defaultDelay.
func EncodeFrames(w io.Writer, frames []image.Image, delays []int, defaultDelay int) error {
	if len(frames) == 0 {
		return nil
	}
	if defaultDelay <= 0 {
		defaultDelay = 100 / 20
	}

	out := &gif.GIF{LoopCount: 0}
	for index, frame := range frames {
		bounds := frame.Bounds()
		paletted := image.NewPaletted(bounds, palette.Plan9)
		draw.FloydSteinberg.Draw(paletted, bounds, frame, image.Point{})
		out.Image = append(out.Image, paletted)

		delay := defaultDelay
		if index < len(delays) && delays[index] > 0 {
			delay = delays[index]
		}
		out.Delay = append(out.Delay, delay)
	}

	return gif.EncodeAll(w, out)
}

// EncodeStatic writes a single image as a static GIF.
func EncodeStatic(w io.Writer, img image.Image) error {
	options := &gif.Options{NumColors: 256}
	return gif.Encode(w, img, options)
}
