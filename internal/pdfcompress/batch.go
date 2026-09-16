package pdfcompress

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jhyan/design-toolbox/internal/app"
)

type qualityPreset struct {
	option      string
	name        string
	quality     int
	description string
}

var batchQualityPresets = []qualityPreset{
	{
		option:      "1",
		name:        "推荐均衡",
		quality:     defaultBatchQuality,
		description: "适合提交和分享，通常能明显压小，同时保留可读性",
	},
	{
		option:      "2",
		name:        "清晰优先",
		quality:     50,
		description: "画质更稳，文件会比推荐档稍大",
	},
	{
		option:      "3",
		name:        "更小文件",
		quality:     30,
		description: "适合严格大小限制，细节损失会更明显",
	},
	{
		option:      "4",
		name:        "高质量",
		quality:     defaultQuality,
		description: "接近原默认画质，文件相对更大",
	},
}

// Main implements the "toolbox pdf" subcommand: without arguments it runs
// batch mode on the input/pdf directory, otherwise it parses single-file flags.
func Main(args []string) error {
	if len(args) == 0 {
		err := RunDefaultBatch(os.Stdin, os.Stdout)
		if err != nil {
			log.Printf("批处理失败: %v", err)
		}
		app.PauseIfInteractive("\n按回车键退出...")
		if err != nil {
			os.Exit(1)
		}
		return nil
	}

	opts, err := ParseFlags(args)
	if err != nil {
		return err
	}
	return run(opts)
}

// RunDefaultBatch runs interactive batch mode using input/pdf and output/pdf
// next to the executable. This is the flow used by the main menu.
func RunDefaultBatch(stdin io.Reader, stdout io.Writer) error {
	inputDirectory, err := app.ToolInputDir("pdf")
	if err != nil {
		return err
	}
	outputDirectory, err := app.ToolOutputDir("pdf")
	if err != nil {
		return err
	}
	return RunBatch(inputDirectory, outputDirectory, stdin, stdout)
}

// RunBatch compresses every PDF found in inputDirectory into outputDirectory,
// asking the user to choose quality presets first.
func RunBatch(inputDirectory, outputDirectory string, stdin io.Reader, stdout io.Writer) error {
	if err := os.MkdirAll(inputDirectory, 0o755); err != nil {
		return fmt.Errorf("创建 input 目录: %w", err)
	}
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return fmt.Errorf("创建 output 目录: %w", err)
	}

	entries, err := os.ReadDir(inputDirectory)
	if err != nil {
		return fmt.Errorf("读取 input 目录: %w", err)
	}

	var inputFiles []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".pdf") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return fmt.Errorf("读取 input/%s: %w", entry.Name(), infoErr)
		}
		if info.Mode().IsRegular() {
			inputFiles = append(inputFiles, filepath.Join(inputDirectory, entry.Name()))
		}
	}
	sort.Slice(inputFiles, func(left, right int) bool {
		return strings.ToLower(filepath.Base(inputFiles[left])) <
			strings.ToLower(filepath.Base(inputFiles[right]))
	})

	log.Printf("输入目录: %s", inputDirectory)
	log.Printf("输出目录: %s", outputDirectory)
	if len(inputFiles) == 0 {
		log.Printf("输入目录中没有 PDF 文件，无需处理")
		return nil
	}

	qualities, err := promptForBatchQualities(stdin, stdout)
	if err != nil {
		return err
	}

	successes := 0
	failures := 0
	totalJobs := len(inputFiles) * len(qualities)
	jobIndex := 0
	useQualitySuffix := len(qualities) > 1
	for _, inputPath := range inputFiles {
		inputName := filepath.Base(inputPath)
		baseName := strings.TrimSuffix(inputName, filepath.Ext(inputName))

		for _, quality := range qualities {
			jobIndex++
			outputName := batchOutputName(baseName, quality, useQualitySuffix)
			outputPath := filepath.Join(outputDirectory, outputName)

			log.Printf("")
			log.Printf(
				"[%d/%d] 开始处理: %s，quality=%d",
				jobIndex,
				totalJobs,
				inputName,
				quality,
			)
			err := run(options{
				input:       inputPath,
				output:      outputPath,
				quality:     quality,
				stripHeight: defaultStripSize,
			})
			if err != nil {
				failures++
				log.Printf("[%d/%d] 处理失败: %v", jobIndex, totalJobs, err)
				continue
			}
			successes++
		}
	}

	log.Printf("")
	log.Printf("批处理完成：成功 %d，失败 %d", successes, failures)
	if failures > 0 {
		return fmt.Errorf("%d 个 PDF 处理失败", failures)
	}
	return nil
}

func batchOutputName(baseName string, quality int, useQualitySuffix bool) string {
	if useQualitySuffix {
		return fmt.Sprintf("%s-压缩版-q%d.pdf", baseName, quality)
	}
	return baseName + "-压缩版.pdf"
}

func promptForBatchQualities(input io.Reader, output io.Writer) ([]int, error) {
	reader := bufio.NewReader(input)
	for {
		fmt.Fprintln(output)
		fmt.Fprintln(output, "请选择压缩档位：")
		for _, preset := range batchQualityPresets {
			fmt.Fprintf(
				output,
				"  %s. %s（quality=%d）：%s\n",
				preset.option,
				preset.name,
				preset.quality,
				preset.description,
			)
		}
		fmt.Fprintf(
			output,
			"直接回车 = %s（q%d）。\n",
			batchQualityPresets[0].name,
			batchQualityPresets[0].quality,
		)
		fmt.Fprintln(output, "也可以输入多个 quality，例如：40,50,60")
		fmt.Fprintln(output, "或输入范围，例如：q10-q90（生成 q10/q20/.../q90）")
		fmt.Fprint(output, "请输入选择：")

		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("读取压缩档位: %w", err)
		}

		choice := strings.TrimSpace(line)
		if choice == "" {
			selected := batchQualityPresets[0]
			fmt.Fprintf(output, "已选择：%s（q%d）\n", selected.name, selected.quality)
			return []int{selected.quality}, nil
		}

		for _, preset := range batchQualityPresets {
			if choice == preset.option {
				fmt.Fprintf(output, "已选择：%s（q%d）\n", preset.name, preset.quality)
				return []int{preset.quality}, nil
			}
		}

		qualities, parseErr := parseQualityList(choice)
		if parseErr == nil {
			fmt.Fprintf(output, "已选择：%s\n", formatQualityList(qualities))
			return qualities, nil
		}

		fmt.Fprintf(output, "输入无效：%v\n", parseErr)
		fmt.Fprintln(output, "请直接回车，或输入 1-4、40、40,50,60、q10-q90。")

		if err == io.EOF {
			selected := batchQualityPresets[0]
			fmt.Fprintf(output, "输入结束，使用默认：%s（q%d）\n", selected.name, selected.quality)
			return []int{selected.quality}, nil
		}
	}
}

func parseQualityList(input string) ([]int, error) {
	normalized := strings.NewReplacer(
		"，", ",",
		"、", ",",
		";", ",",
		"；", ",",
	).Replace(strings.TrimSpace(input))
	if normalized == "" {
		return []int{defaultBatchQuality}, nil
	}

	fields := strings.FieldsFunc(normalized, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	seen := map[int]bool{}
	var qualities []int
	for _, field := range fields {
		item := strings.TrimSpace(field)
		if item == "" {
			continue
		}

		values, err := parseQualityItem(item)
		if err != nil {
			return nil, err
		}
		for _, quality := range values {
			if !seen[quality] {
				seen[quality] = true
				qualities = append(qualities, quality)
			}
		}
	}
	if len(qualities) == 0 {
		return nil, errors.New("没有读取到 quality")
	}
	sort.Ints(qualities)
	return qualities, nil
}

func parseQualityItem(input string) ([]int, error) {
	item := strings.TrimSpace(strings.ToLower(input))
	item = strings.TrimPrefix(item, "quality")
	item = strings.TrimPrefix(item, "q")
	item = strings.TrimSpace(item)

	if strings.Contains(item, "-") {
		parts := strings.Split(item, "-")
		if len(parts) != 2 {
			return nil, fmt.Errorf("范围格式无效: %s", input)
		}
		start, err := parseQualityNumber(parts[0])
		if err != nil {
			return nil, fmt.Errorf("范围起点无效 %q: %w", parts[0], err)
		}
		end, err := parseQualityNumber(parts[1])
		if err != nil {
			return nil, fmt.Errorf("范围终点无效 %q: %w", parts[1], err)
		}
		if start > end {
			return nil, fmt.Errorf("范围起点不能大于终点: %d-%d", start, end)
		}

		var qualities []int
		for quality := start; quality <= end; quality += 10 {
			qualities = append(qualities, quality)
		}
		if qualities[len(qualities)-1] != end {
			qualities = append(qualities, end)
		}
		return qualities, nil
	}

	quality, err := parseQualityNumber(item)
	if err != nil {
		return nil, err
	}
	return []int{quality}, nil
}

func parseQualityNumber(input string) (int, error) {
	value := strings.TrimSpace(strings.ToLower(input))
	value = strings.TrimPrefix(value, "quality")
	value = strings.TrimPrefix(value, "q")
	value = strings.TrimSpace(value)
	quality, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("不是数字: %s", input)
	}
	if quality < 1 || quality > 100 {
		return 0, fmt.Errorf("quality 必须在 1-100 之间，当前值为 %d", quality)
	}
	return quality, nil
}

func formatQualityList(qualities []int) string {
	parts := make([]string, 0, len(qualities))
	for _, quality := range qualities {
		parts = append(parts, fmt.Sprintf("q%d", quality))
	}
	return strings.Join(parts, ", ")
}
