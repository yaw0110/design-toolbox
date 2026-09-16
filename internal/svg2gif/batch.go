package svg2gif

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jhyan/design-toolbox/internal/app"
)

// Main implements the "toolbox svg2gif" subcommand with the original
// svg2gif CLI syntax: [options] <source> <target>.
func Main(args []string) error {
	var source, target string
	opts := Options{FPS: defaultFPS}

	i := 0
	for i < len(args) {
		switch args[i] {
		case "--width", "-w":
			if i+1 >= len(args) {
				return fmt.Errorf("--width 需要一个值")
			}
			fmt.Sscanf(args[i+1], "%d", &opts.Width)
			i += 2
		case "--height":
			if i+1 >= len(args) {
				return fmt.Errorf("--height 需要一个值")
			}
			fmt.Sscanf(args[i+1], "%d", &opts.Height)
			i += 2
		case "--fps", "-f":
			if i+1 >= len(args) {
				return fmt.Errorf("--fps 需要一个值")
			}
			fmt.Sscanf(args[i+1], "%d", &opts.FPS)
			i += 2
		case "--help", "-h":
			printUsage()
			return nil
		default:
			if source == "" {
				source = args[i]
			} else if target == "" {
				target = args[i]
			}
			i++
		}
	}

	if source == "" || target == "" {
		printUsage()
		return fmt.Errorf("必须提供 source 和 target 目录")
	}
	if err := validateOptions(&opts); err != nil {
		return err
	}

	ffmpegPath := DetectFFmpeg()
	logConversionMode(ffmpegPath)

	return ConvertDirectory(source, target, opts, ffmpegPath)
}

// RunDefaultBatch runs interactive batch mode using input/svg and output/svg
// next to the executable. This is the flow used by the main menu.
func RunDefaultBatch(stdin io.Reader, stdout io.Writer) error {
	source, err := app.ToolInputDir("svg")
	if err != nil {
		return err
	}
	target, err := app.ToolOutputDir("svg")
	if err != nil {
		return err
	}
	return RunBatch(source, target, stdin, stdout)
}

// RunBatch converts every SVG/SVGA in source into target/gif and target/apng,
// asking the user for the frame rate first.
func RunBatch(source, target string, stdin io.Reader, stdout io.Writer) error {
	opts := Options{FPS: defaultFPS}

	if info, err := os.Stat(source); err != nil || !info.IsDir() {
		if err := os.MkdirAll(source, 0o755); err != nil {
			return fmt.Errorf("创建输入目录: %w", err)
		}
	}

	inputFiles, err := scanInputFiles(source)
	if err != nil {
		return err
	}

	log.Printf("源目录: %s", source)
	log.Printf("GIF 输出: %s", filepath.Join(target, "gif"))
	log.Printf("APNG 输出: %s", filepath.Join(target, "apng"))
	if len(inputFiles) == 0 {
		log.Printf("输入目录中没有 SVG/SVGA 文件，无需处理")
		return nil
	}

	opts.FPS, err = promptForFPS(
		stdin,
		stdout,
		rememberedFPS(app.LoadSettings().SVGFps),
	)
	if err != nil {
		return err
	}

	settings := app.LoadSettings()
	if settings.SVGFps != opts.FPS {
		settings.SVGFps = opts.FPS
		if saveErr := app.SaveSettings(settings); saveErr != nil {
			log.Printf("记住帧率失败: %v", saveErr)
		} else {
			log.Printf("已记住帧率，下次直接回车可复用")
		}
	}

	ffmpegPath := DetectFFmpeg()
	logConversionMode(ffmpegPath)

	return convertFiles(inputFiles, target, opts, ffmpegPath)
}

// ConvertDirectory converts every SVG/SVGA file directly inside source.
func ConvertDirectory(source, target string, opts Options, ffmpegPath string) error {
	inputFiles, err := scanInputFiles(source)
	if err != nil {
		return err
	}
	if len(inputFiles) == 0 {
		return fmt.Errorf("输入目录中没有 SVG/SVGA 文件: %s", source)
	}
	if err := validateOptions(&opts); err != nil {
		return err
	}

	return convertFiles(inputFiles, target, opts, ffmpegPath)
}

func convertFiles(inputFiles []string, target string, opts Options, ffmpegPath string) error {
	gifTarget := filepath.Join(target, "gif")
	apngTarget := filepath.Join(target, "apng")
	for _, directory := range []string{gifTarget, apngTarget} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("创建输出目录: %w", err)
		}
	}

	fmt.Printf("共发现 %d 个待转换文件\n", len(inputFiles))

	success, failed := 0, 0
	startTime := time.Now()

	for index, inputFile := range inputFiles {
		filename := filepath.Base(inputFile)
		baseName := strings.TrimSuffix(filename, filepath.Ext(filename))

		fmt.Printf("[%d/%d] 正在转换: %s\n", index+1, len(inputFiles), filename)

		apngOutput := filepath.Join(apngTarget, baseName+".png")
		gifOutput := filepath.Join(gifTarget, baseName+".gif")

		err := ConvertFile(inputFile, apngOutput, gifOutput, opts, ffmpegPath)
		if err != nil {
			fmt.Printf("  转换失败: %v\n", err)
			failed++
			continue
		}
		success++
	}

	elapsed := time.Since(startTime)
	fmt.Println()
	fmt.Println("========== 转换汇总 ==========")
	fmt.Printf("总计:   %d 个文件\n", len(inputFiles))
	fmt.Printf("成功:   %d\n", success)
	fmt.Printf("失败:   %d\n", failed)
	fmt.Printf("用时:   %.2f 秒\n", elapsed.Seconds())

	if failed > 0 {
		return fmt.Errorf("%d 个文件转换失败", failed)
	}
	return nil
}

func scanInputFiles(source string) ([]string, error) {
	entries, err := os.ReadDir(source)
	if err != nil {
		return nil, fmt.Errorf("读取输入目录: %w", err)
	}

	var inputFiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".svg" && ext != ".svga" {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil, fmt.Errorf("读取 %s: %w", entry.Name(), infoErr)
		}
		if info.Mode().IsRegular() {
			inputFiles = append(inputFiles, filepath.Join(source, entry.Name()))
		}
	}
	sort.Slice(inputFiles, func(left, right int) bool {
		return strings.ToLower(filepath.Base(inputFiles[left])) <
			strings.ToLower(filepath.Base(inputFiles[right]))
	})
	return inputFiles, nil
}

func promptForFPS(stdin io.Reader, stdout io.Writer, fallbackFPS int) (int, error) {
	hint := ""
	if fallbackFPS != defaultFPS {
		hint = "，上次使用"
	}
	reader := bufio.NewReader(stdin)
	for {
		fmt.Fprintln(stdout)
		fmt.Fprintf(stdout, "请输入帧率 FPS（直接回车 = %d%s，范围 1-50）：", fallbackFPS, hint)

		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return 0, fmt.Errorf("读取帧率: %w", err)
		}

		choice := strings.TrimSpace(line)
		if choice == "" {
			fmt.Fprintf(stdout, "已选择：FPS=%d\n", fallbackFPS)
			return fallbackFPS, nil
		}

		fps, parseErr := strconv.Atoi(choice)
		if parseErr != nil || fps < 1 || fps > 50 {
			fmt.Fprintf(stdout, "输入无效：%q 不是 1-50 之间的数字，请重新输入。\n", choice)
			if err == io.EOF {
				fmt.Fprintf(stdout, "输入结束，使用默认：FPS=%d\n", fallbackFPS)
				return fallbackFPS, nil
			}
			continue
		}

		fmt.Fprintf(stdout, "已选择：FPS=%d\n", fps)
		return fps, nil
	}
}

// rememberedFPS validates the stored FPS; anything outside 1-50 falls back
// to the built-in default.
func rememberedFPS(value int) int {
	if value < 1 || value > 50 {
		return defaultFPS
	}
	return value
}

func validateOptions(opts *Options) error {
	if opts.FPS < 1 || opts.FPS > 50 {
		return fmt.Errorf("fps 必须在 1-50 之间，当前值为 %d", opts.FPS)
	}
	if opts.Width < 0 || opts.Height < 0 {
		return fmt.Errorf("width/height 不能为负数")
	}
	return nil
}

func logConversionMode(ffmpegPath string) {
	if ffmpegPath == "" {
		log.Printf("未检测到 ffmpeg，使用内置 GIF 编码器")
		return
	}
	log.Printf("使用 ffmpeg: %s", ffmpegPath)
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `toolbox svg2gif - 批量将 SVG/SVGA 转换为 GIF（经 APNG）

用法: toolbox svg2gif [选项] <source> <target>

选项:
  -w, --width <pixels>      输出宽度（默认: 自动）
  --height <pixels>         输出高度（默认: 自动）
  -f, --fps <number>        帧率 FPS（默认: 20）
  --help                    显示帮助

输出:
  target/apng/  - APNG 文件
  target/gif/   - GIF 文件

示例:
  toolbox svg2gif ./source ./target
  toolbox svg2gif -w 800 --height 800 ./source ./target
  toolbox svg2gif --fps 24 ./source ./target

`)
}
