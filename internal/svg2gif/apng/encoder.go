package apng

import (
	"image"
	"image/png"
	"io"

	"github.com/kettek/apng"
)

// Encoder handles APNG encoding
type Encoder struct {
	width, height int
	fps           int
}

// NewEncoder creates a new APNG encoder
func NewEncoder(width, height, fps int) *Encoder {
	return &Encoder{
		width:  width,
		height: height,
		fps:    fps,
	}
}

// Encode encodes frames to APNG format
func (e *Encoder) Encode(w io.Writer, frames []image.Image, delays []int) error {
	if len(frames) == 0 {
		return nil
	}

	// Create APNG
	a := apng.APNG{
		Frames: make([]apng.Frame, len(frames)),
	}

	for i, frame := range frames {
		a.Frames[i] = apng.Frame{
			Image:            frame,
			DelayNumerator:   uint16(delays[i]),
			DelayDenominator: 100,
			DisposeOp:        apng.DISPOSE_OP_BACKGROUND,
			BlendOp:          apng.BLEND_OP_SOURCE,
		}
	}

	return apng.Encode(w, a)
}

// EncodeStatic encodes a single frame as static PNG
func (e *Encoder) EncodeStatic(w io.Writer, frame image.Image) error {
	return png.Encode(w, frame)
}
