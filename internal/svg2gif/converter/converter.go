// Package converter provides format conversion interfaces
package converter

import (
	"image"
	"io"
)

// Format represents the input file format
type Format string

const (
	FormatSVG  Format = "svg"
	FormatSVGA Format = "svga"
)

// Options contains conversion parameters
type Options struct {
	Width  int
	Height int
	FPS    int
}

// Converter defines the interface for format converters
type Converter interface {
	// Convert converts the input file to GIF frames
	Convert(r io.Reader, opts Options) ([]image.Image, []int, error)

	// Format returns the supported input format
	Format() Format
}

// DetectFormat detects the input format from the file extension or content
func DetectFormat(filename string) Format {
	if len(filename) >= 5 && filename[len(filename)-5:] == ".svga" {
		return FormatSVGA
	}
	return FormatSVG
}
