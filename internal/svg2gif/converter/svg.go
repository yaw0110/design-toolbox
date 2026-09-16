package converter

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

// SVGConverter handles SVG to GIF conversion
type SVGConverter struct{}

// NewSVGConverter creates a new SVG converter
func NewSVGConverter() *SVGConverter {
	return &SVGConverter{}
}

// Format returns the SVG format
func (c *SVGConverter) Format() Format {
	return FormatSVG
}

// Convert converts an SVG file to GIF frames
func (c *SVGConverter) Convert(r io.Reader, opts Options) ([]image.Image, []int, error) {
	svgData, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read SVG: %w", err)
	}

	// Get dimensions
	w, h, err := c.getDimensions(svgData)
	if err != nil {
		w, h = 200, 200
	}

	if opts.Width > 0 && opts.Height > 0 {
		w, h = opts.Width, opts.Height
	}

	// Check for animation
	if !c.hasAnimation(svgData) {
		return c.renderStatic(svgData, w, h)
	}

	return c.renderAnimated(svgData, w, h, opts)
}

// getDimensions extracts dimensions from SVG
func (c *SVGConverter) getDimensions(svgData []byte) (int, int, error) {
	icon, err := oksvg.ReadIconStream(bytes.NewReader(svgData), oksvg.WarnErrorMode)
	if err != nil {
		return 0, 0, err
	}
	w, h := int(icon.ViewBox.W), int(icon.ViewBox.H)
	if w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("invalid dimensions")
	}
	return w, h, nil
}

// hasAnimation checks for SMIL animation elements
func (c *SVGConverter) hasAnimation(svgData []byte) bool {
	return bytes.Contains(svgData, []byte("<animate"))
}

// renderStatic renders static SVG
func (c *SVGConverter) renderStatic(svgData []byte, w, h int) ([]image.Image, []int, error) {
	icon, err := oksvg.ReadIconStream(bytes.NewReader(svgData), oksvg.WarnErrorMode)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse SVG: %w", err)
	}
	icon.SetTarget(0, 0, float64(w), float64(h))

	img := c.newWhiteImage(w, h)
	c.drawSVG(img, icon, w, h)

	return []image.Image{img}, []int{0}, nil
}

// newWhiteImage creates a white image
func (c *SVGConverter) newWhiteImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	return img
}

// drawSVG draws SVG to image
func (c *SVGConverter) drawSVG(img *image.RGBA, icon *oksvg.SvgIcon, w, h int) {
	scanner := rasterx.NewScannerGV(w, h, img, img.Bounds())
	dasher := rasterx.NewDasher(w, h, scanner)
	icon.Draw(dasher, 1.0)
}

// AnimData holds parsed animation data
type AnimData struct {
	ParentTag string
	AttrName  string
	From      float64
	To        float64
	Dur       float64
}

// renderAnimated renders animated SVG with interpolation
func (c *SVGConverter) renderAnimated(svgData []byte, w, h int, opts Options) ([]image.Image, []int, error) {
	fps := opts.FPS
	if fps <= 0 {
		fps = 15
	}

	content := string(svgData)

	// Parse animations
	animations := parseAnimations(content)
	if len(animations) == 0 {
		return c.renderStatic(svgData, w, h)
	}

	// Get max duration
	duration := 0.0
	for _, a := range animations {
		if a.Dur > duration {
			duration = a.Dur
		}
	}
	if duration <= 0 {
		duration = 2.0
	}

	frameCount := int(float64(fps) * duration)
	if frameCount > 100 {
		frameCount = 100
	}
	if frameCount < 2 {
		frameCount = 2
	}

	// Create base SVG without animate elements
	baseSVG := removeAnimateElements(content)

	frames := make([]image.Image, frameCount)
	delays := make([]int, frameCount)

	for i := 0; i < frameCount; i++ {
		t := float64(i) / float64(frameCount) * duration

		// Interpolate and apply animations
		frameSVG := applyAnimations(baseSVG, animations, t)

		// Render frame
		icon, err := oksvg.ReadIconStream(strings.NewReader(frameSVG), oksvg.WarnErrorMode)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse frame %d: %w", i, err)
		}
		icon.SetTarget(0, 0, float64(w), float64(h))

		img := c.newWhiteImage(w, h)
		c.drawSVG(img, icon, w, h)
		frames[i] = img
		delays[i] = 100 / fps
	}

	return frames, delays, nil
}

// parseAnimations extracts animation data from SVG
func parseAnimations(content string) []AnimData {
	var animations []AnimData

	// Find all animate elements
	animRegex := regexp.MustCompile(`<animate\s+([^>]*?)\s*/>`)

	// Find parent elements followed by animate
	lines := strings.Split(content, "\n")
	var parentTag string
	var parentAttrs string

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Check if this is a parent element opening
		if strings.HasPrefix(line, "<") && !strings.HasPrefix(line, "</") && !strings.HasPrefix(line, "<!") && !strings.HasPrefix(line, "<?") {
			// Check if it has animate child
			if strings.Contains(line, ">") && !strings.HasSuffix(line, "/>") {
				parts := strings.SplitN(line, " ", 2)
				if len(parts) >= 1 {
					tag := parts[0][1:] // Remove <
					if tag != "svg" && tag != "g" && tag != "defs" {
						attrs := ""
						if len(parts) > 1 {
							attrs = parts[1]
						}
						parentTag = tag
						parentAttrs = attrs
					}
				}
			}
		}

		// Check if this is an animate element
		if strings.HasPrefix(line, "<animate") {
			match := animRegex.FindStringSubmatch(line)
			if len(match) > 1 {
				animAttrs := match[1]

				anim := AnimData{ParentTag: parentTag}
				anim.AttrName = extractAttr(animAttrs, "attributeName")
				anim.From = parseFloat(extractAttr(animAttrs, "from"))
				anim.To = parseFloat(extractAttr(animAttrs, "to"))
				anim.Dur = parseDuration(extractAttr(animAttrs, "dur"))

				// Use parent's original value if 'from' not specified
				if anim.From == 0 {
					anim.From = parseFloat(extractAttr(parentAttrs, anim.AttrName))
				}

				if anim.AttrName != "" && anim.Dur > 0 {
					animations = append(animations, anim)
				}
			}
		}
	}

	return animations
}

// extractAttr extracts an attribute value from attribute string
func extractAttr(attrs, name string) string {
	pattern := regexp.MustCompile(name + `="([^"]*)"`)
	match := pattern.FindStringSubmatch(attrs)
	if len(match) > 1 {
		return match[1]
	}
	return ""
}

// parseFloat parses a float from string
func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// parseDuration parses duration string like "2s" or "500ms"
func parseDuration(s string) float64 {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "ms") {
		v, _ := strconv.ParseFloat(strings.TrimSuffix(s, "ms"), 64)
		return v / 1000
	}
	if strings.HasSuffix(s, "s") {
		v, _ := strconv.ParseFloat(strings.TrimSuffix(s, "s"), 64)
		return v
	}
	return 0
}

// removeAnimateElements removes all animate elements from SVG
func removeAnimateElements(content string) string {
	// Remove self-closing animate elements
	re := regexp.MustCompile(`<animate[^>]*?/>[\s]*`)
	content = re.ReplaceAllString(content, "")

	return content
}

// applyAnimations applies interpolated animation values to SVG
func applyAnimations(baseSVG string, animations []AnimData, t float64) string {
	result := baseSVG

	for _, anim := range animations {
		// Calculate current value with linear interpolation
		// Handle repeating animations
		cycleT := t
		if anim.Dur > 0 {
			cycleT = math.Mod(t, anim.Dur) / anim.Dur
		}

		currentVal := anim.From + (anim.To-anim.From)*cycleT

		// Replace the attribute value in parent tag
		result = replaceAttrValue(result, anim.ParentTag, anim.AttrName, currentVal)
	}

	return result
}

// replaceAttrValue replaces an attribute value in SVG
func replaceAttrValue(content, tag, attr string, value float64) string {
	// Pattern to find the tag with the attribute
	// <tag attr="old" ...>
	pattern := regexp.MustCompile(`(<` + tag + `\s+[^>]*` + attr + `=")[^"]*(")`)

	// Replace with new value (rounded for cleaner output)
	replacement := fmt.Sprintf(`${1}%.0f${2}`, value)
	return pattern.ReplaceAllString(content, replacement)
}