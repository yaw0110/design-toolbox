// Package svg2gif converts SVG/SVGA files to GIF and APNG.
// The converters are ported from the svg2gif project; the toolbox adds
// directory batch runs and a pure-Go GIF fallback when ffmpeg is absent.
package svg2gif

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	apngencoder "github.com/jhyan/design-toolbox/internal/svg2gif/apng"
	converter "github.com/jhyan/design-toolbox/internal/svg2gif/converter"
	"github.com/jhyan/design-toolbox/internal/svg2gif/gifenc"
)

const defaultFPS = 20

// Options contains conversion parameters.
type Options struct {
	Width  int
	Height int
	FPS    int
}

// DetectFFmpeg returns the path to a usable ffmpeg: first next to the
// executable, then the system PATH. It returns "" when ffmpeg is absent,
// in which case the built-in GIF encoder is used instead.
func DetectFFmpeg() string {
	executablePath, err := os.Executable()
	if err == nil {
		executableDir := filepath.Dir(executablePath)
		for _, name := range []string{"ffmpeg", "ffmpeg.exe"} {
			localFFmpeg := filepath.Join(executableDir, name)
			if _, statErr := os.Stat(localFFmpeg); statErr == nil {
				return localFFmpeg
			}
		}
	}

	if path, lookErr := exec.LookPath("ffmpeg"); lookErr == nil {
		return path
	}
	return ""
}

// ConvertFile converts one SVG/SVGA file into an APNG and a GIF. When
// ffmpegPath is empty, the built-in GIF encoder produces the GIF.
func ConvertFile(inputPath, apngOutput, gifOutput string, opts Options, ffmpegPath string) error {
	inputFile, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("打开文件失败: %w", err)
	}
	defer inputFile.Close()

	var conv converter.Converter
	if converter.DetectFormat(inputPath) == converter.FormatSVGA {
		conv = converter.NewSVGAConverter()
	} else {
		conv = converter.NewSVGConverter()
	}

	if opts.FPS <= 0 {
		opts.FPS = defaultFPS
	}

	frames, delays, err := conv.Convert(inputFile, converter.Options{
		Width:  opts.Width,
		Height: opts.Height,
		FPS:    opts.FPS,
	})
	if err != nil {
		return fmt.Errorf("转换失败: %w", err)
	}

	imgWidth, imgHeight := opts.Width, opts.Height
	if imgWidth <= 0 || imgHeight <= 0 {
		if len(frames) > 0 {
			bounds := frames[0].Bounds()
			imgWidth = bounds.Dx()
			imgHeight = bounds.Dy()
		} else {
			imgWidth, imgHeight = 800, 600
		}
	}

	if err := os.MkdirAll(filepath.Dir(apngOutput), 0o755); err != nil {
		return fmt.Errorf("创建 APNG 输出目录: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(gifOutput), 0o755); err != nil {
		return fmt.Errorf("创建 GIF 输出目录: %w", err)
	}

	apngFile, err := os.Create(apngOutput)
	if err != nil {
		return fmt.Errorf("创建 APNG 失败: %w", err)
	}

	encoder := apngencoder.NewEncoder(imgWidth, imgHeight, opts.FPS)
	if len(frames) == 1 {
		err = encoder.EncodeStatic(apngFile, frames[0])
	} else {
		err = encoder.Encode(apngFile, frames, delays)
	}
	closeErr := apngFile.Close()
	if err != nil {
		return fmt.Errorf("APNG 编码失败: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("关闭 APNG 失败: %w", closeErr)
	}

	if apngInfo, statErr := os.Stat(apngOutput); statErr == nil {
		fmt.Printf("  APNG: %s (%.1f KB)\n", filepath.Base(apngOutput), float64(apngInfo.Size())/1024)
	}

	if ffmpegPath != "" {
		ffmpegCmd := exec.Command(
			ffmpegPath,
			"-y",
			"-i", apngOutput,
			"-filter_complex", fmt.Sprintf("[0:v] fps=%d,split [a][b];[a] palettegen [p];[b][p] paletteuse", opts.FPS),
			"-loop", "0",
			gifOutput,
		)
		if output, ffmpegErr := ffmpegCmd.CombinedOutput(); ffmpegErr != nil {
			return fmt.Errorf("ffmpeg 转换 GIF 失败: %w\n%s", ffmpegErr, string(output))
		}
	} else {
		gifFile, gifErr := os.Create(gifOutput)
		if gifErr != nil {
			return fmt.Errorf("创建 GIF 失败: %w", gifErr)
		}
		encodeErr := gifenc.EncodeFrames(gifFile, frames, delays, 100/opts.FPS)
		closeErr := gifFile.Close()
		if encodeErr != nil {
			return fmt.Errorf("内置 GIF 编码失败: %w", encodeErr)
		}
		if closeErr != nil {
			return fmt.Errorf("关闭 GIF 失败: %w", closeErr)
		}
	}

	if gifInfo, statErr := os.Stat(gifOutput); statErr == nil {
		fmt.Printf("  GIF:  %s (%.1f KB)\n", filepath.Base(gifOutput), float64(gifInfo.Size())/1024)
	}

	return nil
}
