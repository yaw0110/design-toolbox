package converter

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"sort"

	xdraw "golang.org/x/image/draw"
)

// SVGAConverter handles SVGA to GIF conversion
type SVGAConverter struct{}

// NewSVGAConverter creates a new SVGA converter
func NewSVGAConverter() *SVGAConverter {
	return &SVGAConverter{}
}

// Format returns the SVGA format
func (c *SVGAConverter) Format() Format {
	return FormatSVGA
}

// Convert converts an SVGA file to GIF frames
func (c *SVGAConverter) Convert(r io.Reader, opts Options) ([]image.Image, []int, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read SVGA: %w", err)
	}

	movie, err := c.parseSVGA(data)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse SVGA: %w", err)
	}

	// Calculate actual frame count from sprite frames array length
	// Each sprite has a frames array where array index = frame number
	// Empty frames (nil) = hidden sprite at that frame
	frameCount := int(movie.Frames)
	if frameCount <= 0 {
		frameCount = 1
	}
	// Check if any sprite has more frames than movie declares
	for _, sprite := range movie.Sprites {
		if len(sprite.Frames) > frameCount {
			frameCount = len(sprite.Frames)
		}
	}

	width := int(movie.Width)
	height := int(movie.Height)

	scaleX := 1.0
	scaleY := 1.0
	if opts.Width > 0 && opts.Height > 0 {
		scaleX = float64(opts.Width) / float64(width)
		scaleY = float64(opts.Height) / float64(height)
		width = opts.Width
		height = opts.Height
	}

	fps := int(movie.Fps)
	if fps <= 0 {
		fps = opts.FPS
	}
	if fps <= 0 {
		fps = 20
	}

	// Decode images
	images := make(map[string]image.Image)
	for key, imgData := range movie.Images {
		img, err := c.decodeImage(imgData)
		if err != nil {
			fmt.Printf("Warning: failed to decode image %s: %v\n", key, err)
			continue
		}
		images[key] = img
	}

	// If no sprites but has images, use images directly as frames
	if len(movie.Sprites) == 0 && len(movie.Images) > 0 {
		keys := make([]string, 0, len(movie.Images))
		for k := range movie.Images {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		count := len(keys)
		if count > frameCount {
			count = frameCount
		}

		frames := make([]image.Image, count)
		delays := make([]int, count)

		for i := 0; i < count; i++ {
			frame := image.NewRGBA(image.Rect(0, 0, width, height))
			for y := 0; y < height; y++ {
				for x := 0; x < width; x++ {
					frame.Set(x, y, color.RGBA{0, 0, 0, 0})
				}
			}
			if i < len(keys) && images[keys[i]] != nil {
				c.drawImageCentered(frame, images[keys[i]], width, height)
			}
			frames[i] = frame
			delays[i] = 100 / fps
		}
		return frames, delays, nil
	}

	frames := make([]image.Image, frameCount)
	delays := make([]int, frameCount)

	// Render each frame
	for i := 0; i < frameCount; i++ {
		frame := c.renderFrame(movie, images, i, width, height, scaleX, scaleY)
		frames[i] = frame
		delays[i] = 100 / fps
	}

	return frames, delays, nil
}

// ProtobufReader helps read protobuf wire format
type ProtobufReader struct {
	data []byte
	pos  int
}

func (r *ProtobufReader) readVarint() uint64 {
	var result uint64
	var shift uint
	for r.pos < len(r.data) {
		b := r.data[r.pos]
		r.pos++
		result |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			break
		}
		shift += 7
	}
	return result
}

func (r *ProtobufReader) readFixed32() float32 {
	if r.pos+4 > len(r.data) {
		return 0
	}
	bits := binary.LittleEndian.Uint32(r.data[r.pos : r.pos+4])
	r.pos += 4
	return math.Float32frombits(bits)
}

func (r *ProtobufReader) readBytes() []byte {
	length := r.readVarint()
	if r.pos+int(length) > len(r.data) {
		return nil
	}
	data := r.data[r.pos : r.pos+int(length)]
	r.pos += int(length)
	return data
}

func (r *ProtobufReader) remaining() int {
	return len(r.data) - r.pos
}

// parseSVGA parses SVGA protobuf format
func (c *SVGAConverter) parseSVGA(data []byte) (*Movie, error) {
	// Decompress zlib
	if len(data) >= 2 && data[0] == 0x78 {
		r, err := zlib.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("failed to create zlib reader: %w", err)
		}
		defer r.Close()
		data, err = io.ReadAll(r)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress: %w", err)
		}
	}

	movie := &Movie{
		Images:  make(map[string][]byte),
		Sprites: make([]*Sprite, 0),
	}

	reader := &ProtobufReader{data: data}

	for reader.remaining() > 0 {
		tag := reader.readVarint()
		field := tag >> 3
		wire := tag & 0x7

		switch field {
		case 1: // version
			if wire == 2 {
				movie.Version = string(reader.readBytes())
			}
		case 2: // params
			if wire == 2 {
				paramsData := reader.readBytes()
				c.parseParams(paramsData, movie)
			}
		case 3: // images map entry
			if wire == 2 {
				entryData := reader.readBytes()
				key, value := c.parseMapEntry(entryData)
				if key != "" && len(value) > 0 {
					movie.Images[key] = value
				}
			}
		case 4: // sprites
			if wire == 2 {
				spriteData := reader.readBytes()
				sprite := c.parseSprite(spriteData)
				if sprite != nil {
					movie.Sprites = append(movie.Sprites, sprite)
				}
			}
		default:
			if wire == 0 {
				reader.readVarint()
			} else if wire == 2 {
				reader.readBytes()
			} else if wire == 5 {
				reader.pos += 4
			}
		}
	}

	return movie, nil
}

func (c *SVGAConverter) parseParams(data []byte, movie *Movie) {
	reader := &ProtobufReader{data: data}

	for reader.remaining() > 0 {
		tag := reader.readVarint()
		field := tag >> 3
		wire := tag & 0x7

		switch field {
		case 1: // width
			if wire == 5 {
				movie.Width = int32(reader.readFixed32())
			}
		case 2: // height
			if wire == 5 {
				movie.Height = int32(reader.readFixed32())
			}
		case 3: // frames
			if wire == 0 {
				movie.Frames = int32(reader.readVarint())
			}
		case 4: // fps
			if wire == 0 {
				movie.Fps = int32(reader.readVarint())
			}
		default:
			if wire == 0 {
				reader.readVarint()
			} else if wire == 2 {
				reader.readBytes()
			} else if wire == 5 {
				reader.pos += 4
			}
		}
	}
}

func (c *SVGAConverter) parseMapEntry(data []byte) (string, []byte) {
	reader := &ProtobufReader{data: data}
	var key string
	var value []byte

	for reader.remaining() > 0 {
		tag := reader.readVarint()
		field := tag >> 3
		wire := tag & 0x7

		if field == 1 && wire == 2 {
			key = string(reader.readBytes())
		} else if field == 2 && wire == 2 {
			value = reader.readBytes()
		} else {
			if wire == 0 {
				reader.readVarint()
			} else if wire == 2 {
				reader.readBytes()
			}
		}
	}

	return key, value
}

func (c *SVGAConverter) parseSprite(data []byte) *Sprite {
	sprite := &Sprite{Frames: make([]*Frame, 0)}
	reader := &ProtobufReader{data: data}

	for reader.remaining() > 0 {
		tag := reader.readVarint()
		field := tag >> 3
		wire := tag & 0x7

		switch field {
		case 1: // imageKey
			if wire == 2 {
				sprite.ImageKey = string(reader.readBytes())
			}
		case 2: // frames array (repeated field)
			if wire == 2 {
				frameData := reader.readBytes()
				// Parse frame data (empty frames = hidden, still need to track index)
				if len(frameData) > 0 {
					frame := c.parseFrame(frameData)
					sprite.Frames = append(sprite.Frames, frame)
				} else {
					// Empty frame = hidden sprite at this index
					sprite.Frames = append(sprite.Frames, nil)
				}
			}
		default:
			if wire == 0 {
				reader.readVarint()
			} else if wire == 2 {
				reader.readBytes()
			}
		}
	}

	return sprite
}

func (c *SVGAConverter) parseFrame(data []byte) *Frame {
	frame := &Frame{Alpha: 1, TransformA: 1, TransformD: 1}
	reader := &ProtobufReader{data: data}

	for reader.remaining() > 0 {
		tag := reader.readVarint()
		field := tag >> 3
		wire := tag & 0x7

		switch field {
		case 1: // alpha (field 1)
			if wire == 5 {
				frame.Alpha = reader.readFixed32()
			}
		case 2: // Layout (embedded message, field 2)
			if wire == 2 {
				layoutData := reader.readBytes()
				c.parseLayout(layoutData, frame)
			}
		case 3: // Transform (embedded message, field 3)
			if wire == 2 {
				transformData := reader.readBytes()
				c.parseTransform(transformData, frame)
			}
		default:
			if wire == 0 {
				reader.readVarint()
			} else if wire == 2 {
				reader.readBytes()
			} else if wire == 5 {
				reader.pos += 4
			}
		}
	}

	return frame
}

func (c *SVGAConverter) parseLayout(data []byte, frame *Frame) {
	reader := &ProtobufReader{data: data}

	for reader.remaining() > 0 {
		tag := reader.readVarint()
		field := tag >> 3
		wire := tag & 0x7

		switch field {
		case 1: // x
			if wire == 5 {
				frame.LayoutX = reader.readFixed32()
			}
		case 2: // y
			if wire == 5 {
				frame.LayoutY = reader.readFixed32()
			}
		case 3: // width
			if wire == 5 {
				frame.LayoutWidth = reader.readFixed32()
			}
		case 4: // height
			if wire == 5 {
				frame.LayoutHeight = reader.readFixed32()
			}
		default:
			if wire == 0 {
				reader.readVarint()
			} else if wire == 2 {
				reader.readBytes()
			} else if wire == 5 {
				reader.pos += 4
			}
		}
	}
}

func (c *SVGAConverter) parseTransform(data []byte, frame *Frame) {
	reader := &ProtobufReader{data: data}

	for reader.remaining() > 0 {
		tag := reader.readVarint()
		field := tag >> 3
		wire := tag & 0x7

		switch field {
		case 1: // a (scale X)
			if wire == 5 {
				frame.TransformA = reader.readFixed32()
			}
		case 2: // b (skew Y)
			if wire == 5 {
				frame.TransformB = reader.readFixed32()
			}
		case 3: // c (skew X)
			if wire == 5 {
				frame.TransformC = reader.readFixed32()
			}
		case 4: // d (scale Y)
			if wire == 5 {
				frame.TransformD = reader.readFixed32()
			}
		case 5: // tx (translate X)
			if wire == 5 {
				frame.TransformTX = reader.readFixed32()
			}
		case 6: // ty (translate Y)
			if wire == 5 {
				frame.TransformTY = reader.readFixed32()
			}
		default:
			if wire == 0 {
				reader.readVarint()
			} else if wire == 2 {
				reader.readBytes()
			} else if wire == 5 {
				reader.pos += 4
			}
		}
	}
}

func (c *SVGAConverter) decodeImage(data []byte) (image.Image, error) {
	if len(data) > 8 && bytes.HasPrefix(data, []byte("\x89PNG")) {
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		// Convert paletted images to RGBA and fix alpha=0 pixels
		// Palette images may have non-zero RGB for transparent pixels which causes issues during interpolation
		rgba := image.NewRGBA(img.Bounds())
		for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
			for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
				r, g, b, a := img.At(x, y).RGBA()
				// If alpha is 0, set RGB to 0 as well (fix palette transparency issue)
				if a == 0 {
					rgba.SetRGBA(x, y, color.RGBA{0, 0, 0, 0})
				} else {
					rgba.SetRGBA(x, y, color.RGBA{
						R: uint8(r >> 8),
						G: uint8(g >> 8),
						B: uint8(b >> 8),
						A: uint8(a >> 8),
					})
				}
			}
		}
		return rgba, nil
	}
	return nil, fmt.Errorf("not a PNG image")
}

// renderFrame renders a single animation frame
func (c *SVGAConverter) renderFrame(movie *Movie, images map[string]image.Image, frameIndex, width, height int, scaleX, scaleY float64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// Transparent background
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{0, 0, 0, 0})
		}
	}

	// Render all sprites at this frame
	for _, sprite := range movie.Sprites {
		// Get frame data by index
		if frameIndex >= len(sprite.Frames) {
			continue
		}
		frame := sprite.Frames[frameIndex]

		// Skip if no frame data (empty frame = hidden)
		if frame == nil {
			continue
		}

		// Get the image
		spriteImg, ok := images[sprite.ImageKey]
		if !ok {
			continue
		}

		// Skip if completely transparent
		if frame.Alpha <= 0 {
			continue
		}

		// Draw sprite
		c.drawSpriteWithTransform(img, spriteImg, frame, width, height, scaleX, scaleY)
	}

	return img
}

// drawSpriteWithTransform draws sprite using transform matrix
// The reference implementation (Canvas 2D) does:
// 1. Apply transform matrix to context
// 2. Draw bitmap at (0, 0) with layout size
func (c *SVGAConverter) drawSpriteWithTransform(dst *image.RGBA, src image.Image, frame *Frame, width, height int, scaleX, scaleY float64) {
	srcBounds := src.Bounds()

	// Layout gives us the size to draw the bitmap
	layoutW := float64(frame.LayoutWidth)
	layoutH := float64(frame.LayoutHeight)

	// Skip sprites with empty layout (hidden)
	if layoutW <= 0 || layoutH <= 0 {
		return
	}

	// Get transform matrix components
	mA := float64(frame.TransformA)
	mB := float64(frame.TransformB)
	mC := float64(frame.TransformC)
	mD := float64(frame.TransformD)
	mTX := float64(frame.TransformTX)
	mTY := float64(frame.TransformTY)

	// Apply canvas scaling
	mA *= scaleX
	mD *= scaleY
	mTX *= scaleX
	mTY *= scaleY

	// Skip if no transform needed (identity with scale)
	if mA == 1 && mD == 1 && mB == 0 && mC == 0 && mTX == 0 && mTY == 0 {
		// Simple case: just draw at origin
		draw.Draw(dst, image.Rect(0, 0, int(layoutW), int(layoutH)), src, srcBounds.Min, draw.Over)
		return
	}

	// Scale the source image to layout size first
	scaledToLayout := image.NewRGBA(image.Rect(0, 0, int(layoutW), int(layoutH)))
	xdraw.CatmullRom.Scale(scaledToLayout, scaledToLayout.Bounds(), src, srcBounds, xdraw.Over, nil)

	// Now apply the transform to create the final image
	// Calculate output bounds
	corners := [4]struct{ x, y float64 }{
		{mTX, mTY},                                                              // (0, 0)
		{mA*layoutW + mTX, mB*layoutW + mTY},                                    // (layoutW, 0)
		{mC*layoutH + mTX, mD*layoutH + mTY},                                    // (0, layoutH)
		{mA*layoutW + mC*layoutH + mTX, mB*layoutW + mD*layoutH + mTY},          // (layoutW, layoutH)
	}

	minX := corners[0].x
	maxX := corners[0].x
	minY := corners[0].y
	maxY := corners[0].y
	for _, c := range corners[1:] {
		if c.x < minX { minX = c.x }
		if c.x > maxX { maxX = c.x }
		if c.y < minY { minY = c.y }
		if c.y > maxY { maxY = c.y }
	}

	// Clamp to canvas
	dstStartX := int(math.Floor(minX))
	dstStartY := int(math.Floor(minY))
	dstEndX := int(math.Ceil(maxX))
	dstEndY := int(math.Ceil(maxY))
	if dstStartX < 0 { dstStartX = 0 }
	if dstStartY < 0 { dstStartY = 0 }
	if dstEndX > width { dstEndX = width }
	if dstEndY > height { dstEndY = height }

	// Apply alpha
	alpha := float64(frame.Alpha)
	if alpha <= 0 { alpha = 1 }

	// Calculate inverse transform
	det := mA*mD - mB*mC
	if det == 0 { return }
	invA := mD / det
	invB := -mB / det
	invC := -mC / det
	invD := mA / det

	// Draw transformed pixels
	for dstY := dstStartY; dstY < dstEndY; dstY++ {
		for dstX := dstStartX; dstX < dstEndX; dstX++ {
			// Inverse transform
			dx := float64(dstX) - mTX
			dy := float64(dstY) - mTY
			layoutXF := invA*dx + invC*dy
			layoutYF := invB*dx + invD*dy

			// Check bounds
			if layoutXF < 0 || layoutXF >= layoutW || layoutYF < 0 || layoutYF >= layoutH {
				continue
			}

			// Sample from scaled image (nearest neighbor for simplicity)
			ix := int(layoutXF)
			iy := int(layoutYF)
			if ix >= int(layoutW) { ix = int(layoutW) - 1 }
			if iy >= int(layoutH) { iy = int(layoutH) - 1 }

			r, g, b, a := scaledToLayout.At(ix, iy).RGBA()
			if a == 0 { continue }

			// Apply frame alpha
			if alpha < 1 {
				a = uint32(float64(a) * alpha)
			}

			// Alpha blend with destination
			dstR, dstG, dstB, dstA := dst.At(dstX, dstY).RGBA()
			srcANorm := float64(a) / 65535.0
			dstANorm := float64(dstA) / 65535.0
			outA := srcANorm + dstANorm*(1-srcANorm)
			if outA > 0 {
				outR := (float64(r)/65535.0*srcANorm + float64(dstR)/65535.0*dstANorm*(1-srcANorm)) / outA
				outG := (float64(g)/65535.0*srcANorm + float64(dstG)/65535.0*dstANorm*(1-srcANorm)) / outA
				outB := (float64(b)/65535.0*srcANorm + float64(dstB)/65535.0*dstANorm*(1-srcANorm)) / outA
				dst.Set(dstX, dstY, color.RGBA{
					R: uint8(outR * 255),
					G: uint8(outG * 255),
					B: uint8(outB * 255),
					A: uint8(outA * 255),
				})
			}
		}
	}
}

// bilinearF performs bilinear interpolation on float64 values
func bilinearF(v00, v10, v01, v11, fx, fy float64) float64 {
	v0 := v00*(1-fx) + v10*fx
	v1 := v01*(1-fx) + v11*fx
	return v0*(1-fy) + v1*fy
}

// bilinear performs bilinear interpolation between four uint32 values
func bilinear(v00, v10, v01, v11 uint32, fx, fy float64) uint32 {
	v0 := uint32(float64(v00)*(1-fx) + float64(v10)*fx)
	v1 := uint32(float64(v01)*(1-fx) + float64(v11)*fx)
	return uint32(float64(v0)*(1-fy) + float64(v1)*fy)
}

// drawImageCentered draws an image centered on the canvas
func (c *SVGAConverter) drawImageCentered(dst *image.RGBA, src image.Image, width, height int) {
	srcBounds := src.Bounds()
	srcW, srcH := srcBounds.Dx(), srcBounds.Dy()

	startX := (width - srcW) / 2
	startY := (height - srcH) / 2

	for y := 0; y < srcH; y++ {
		for x := 0; x < srcW; x++ {
			dstX := startX + x
			dstY := startY + y

			if dstX >= 0 && dstX < width && dstY >= 0 && dstY < height {
				srcColor := src.At(x+srcBounds.Min.X, y+srcBounds.Min.Y)
				dst.Set(dstX, dstY, srcColor)
			}
		}
	}
}

// Movie represents SVGA movie data
type Movie struct {
	Version string
	Fps     int32
	Frames  int32
	Width   int32
	Height  int32
	Images  map[string][]byte
	Sprites []*Sprite
}

// Sprite represents a sprite element
type Sprite struct {
	ImageKey string
	Frames   []*Frame
}

// Frame represents frame transformation data
type Frame struct {
	FrameIndex int32
	Alpha      float32
	// Layout contains position and size
	LayoutX      float32
	LayoutY      float32
	LayoutWidth  float32
	LayoutHeight float32
	// Transform is 2D affine matrix [a, b, c, d, tx, ty]
	TransformA  float32 // scale X
	TransformB  float32 // skew Y
	TransformC  float32 // skew X
	TransformD  float32 // scale Y
	TransformTX float32 // translate X
	TransformTY float32 // translate Y
}
