// Package pdfmerge merges every PDF in a directory into one file, or splits
// a multi-page PDF into single-page PDFs, built directly on pdfcpu.
package pdfmerge

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"

	"github.com/yaw0110/design-toolbox/internal/app"
)

const (
	actionNone  = 0
	actionMerge = 1
	actionSplit = 2
)

// Main implements the "toolbox pdfmerge" subcommand. It has no flags;
// without arguments it runs batch mode on the input/pdfmerge directory.
func Main(args []string) error {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			printUsage()
			return nil
		}
		return fmt.Errorf("无法识别的参数: %s（pdfmerge 只支持无参数批处理）", arg)
	}

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

// RunDefaultBatch runs batch mode using input/pdfmerge and output/pdfmerge
// next to the executable. This is the flow used by the main menu.
func RunDefaultBatch(stdin io.Reader, stdout io.Writer) error {
	inputDirectory, err := app.ToolInputDir("pdfmerge")
	if err != nil {
		return err
	}
	outputDirectory, err := app.ToolOutputDir("pdfmerge")
	if err != nil {
		return err
	}
	return RunBatch(inputDirectory, outputDirectory, stdin, stdout)
}

// RunBatch inspects the PDFs in inputDirectory and merges or splits them.
// Multiple PDFs default to merging; a single multi-page PDF defaults to
// splitting; the user can always override the default or cancel.
func RunBatch(inputDirectory, outputDirectory string, stdin io.Reader, stdout io.Writer) error {
	if err := os.MkdirAll(inputDirectory, 0o755); err != nil {
		return fmt.Errorf("创建 input 目录: %w", err)
	}
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return fmt.Errorf("创建 output 目录: %w", err)
	}

	inputFiles, err := scanInputFiles(inputDirectory)
	if err != nil {
		return err
	}

	log.Printf("输入目录: %s", inputDirectory)
	log.Printf("输出目录: %s", outputDirectory)
	if len(inputFiles) == 0 {
		log.Printf("输入目录中没有 PDF 文件，无需处理")
		return nil
	}

	action, err := chooseAction(inputFiles, stdin, stdout)
	if err != nil {
		return err
	}

	switch action {
	case actionMerge:
		return mergeAll(inputFiles, outputDirectory)
	case actionSplit:
		return splitAll(inputFiles, outputDirectory)
	default:
		log.Printf("已取消，未做任何处理")
		return nil
	}
}

func chooseAction(inputFiles []string, stdin io.Reader, stdout io.Writer) (int, error) {
	if len(inputFiles) >= 2 {
		return promptForAction(stdin, stdout, fmt.Sprintf(
			"发现 %d 个 PDF，请选择操作：\n"+
				"  1. 合并为一个 PDF（直接回车）\n"+
				"  2. 把每个 PDF 按页拆分\n"+
				"  0. 取消\n"+
				"请输入选择：", len(inputFiles),
		), actionMerge)
	}

	pageCount, err := api.PageCountFile(inputFiles[0])
	if err != nil {
		return actionNone, fmt.Errorf("读取页数 %s: %w", filepath.Base(inputFiles[0]), err)
	}
	if pageCount <= 1 {
		log.Printf("输入目录中只有一个单页 PDF，无需合并或拆分")
		return actionNone, nil
	}
	return promptForAction(stdin, stdout, fmt.Sprintf(
		"%s 共 %d 页，请选择操作：\n"+
			"  1. 按页拆分为单页 PDF（直接回车）\n"+
			"  0. 取消\n"+
			"请输入选择：", filepath.Base(inputFiles[0]), pageCount,
	), actionSplit)
}

func promptForAction(stdin io.Reader, stdout io.Writer, prompt string, defaultAction int) (int, error) {
	reader := bufio.NewReader(stdin)
	for {
		fmt.Fprint(stdout, "\n"+prompt)

		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return actionNone, fmt.Errorf("读取选择: %w", err)
		}

		switch strings.TrimSpace(line) {
		case "":
			fmt.Fprintf(stdout, "已选择：默认\n")
			return defaultAction, nil
		case "1":
			return actionMerge, nil
		case "2":
			return actionSplit, nil
		case "0", "q", "Q", "取消":
			return actionNone, nil
		}

		fmt.Fprintf(stdout, "输入无效：%q，请输入 1、2 或 0。\n", strings.TrimSpace(line))
		if err == io.EOF {
			fmt.Fprintf(stdout, "输入结束，使用默认操作。\n")
			return defaultAction, nil
		}
	}
}

func mergeAll(inputFiles []string, outputDirectory string) error {
	outputName := "合并-" + time.Now().Format("20060102-150405") + ".pdf"
	outputPath := filepath.Join(outputDirectory, outputName)

	tempFile, err := os.CreateTemp(outputDirectory, ".pdfmerge-*.pdf")
	if err != nil {
		return fmt.Errorf("创建临时合并文件: %w", err)
	}
	tempPath := tempFile.Name()
	tempFile.Close()
	defer os.Remove(tempPath)

	log.Printf("按文件名顺序合并 %d 个 PDF...", len(inputFiles))
	for _, inputPath := range inputFiles {
		log.Printf("  + %s", filepath.Base(inputPath))
	}
	if err := api.MergeCreateFile(inputFiles, tempPath, false, configuration()); err != nil {
		return fmt.Errorf("合并 PDF: %w", err)
	}
	if err := os.Chmod(tempPath, 0o644); err != nil {
		return fmt.Errorf("设置输出权限: %w", err)
	}
	if err := os.Rename(tempPath, outputPath); err != nil {
		return fmt.Errorf("保存合并结果: %w", err)
	}

	pageCount, countErr := api.PageCountFile(outputPath)
	outputStat, statErr := os.Stat(outputPath)
	if countErr == nil && statErr == nil {
		log.Printf("完成: %s（共 %d 页，%.2f MiB）",
			outputPath, pageCount, bytesToMiB(outputStat.Size()))
	} else {
		log.Printf("完成: %s", outputPath)
	}
	return nil
}

func splitAll(inputFiles []string, outputDirectory string) error {
	successes := 0
	failures := 0

	for index, inputPath := range inputFiles {
		inputName := filepath.Base(inputPath)
		baseName := strings.TrimSuffix(inputName, filepath.Ext(inputName))

		log.Printf("[%d/%d] 正在拆分: %s", index+1, len(inputFiles), inputName)
		written, err := splitIntoPages(inputPath, filepath.Join(outputDirectory, baseName))
		if err != nil {
			failures++
			log.Printf("[%d/%d] 拆分失败: %v", index+1, len(inputFiles), err)
			continue
		}
		log.Printf("  输出 %d 个单页 PDF", written)
		successes++
	}

	log.Printf("")
	log.Printf("拆分完成：成功 %d，失败 %d", successes, failures)
	if failures > 0 {
		return fmt.Errorf("%d 个 PDF 拆分失败", failures)
	}
	return nil
}

func splitIntoPages(inputPath, outputDirectory string) (int, error) {
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return 0, fmt.Errorf("创建输出目录: %w", err)
	}

	input, err := os.Open(inputPath)
	if err != nil {
		return 0, fmt.Errorf("打开输入 PDF: %w", err)
	}
	defer input.Close()

	spans, err := api.SplitRaw(input, 1, configuration())
	if err != nil {
		return 0, fmt.Errorf("拆分 PDF: %w", err)
	}

	baseName := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	written := 0
	for _, span := range spans {
		outputPath := filepath.Join(
			outputDirectory,
			fmt.Sprintf("%s-第%02d页.pdf", baseName, span.From),
		)
		if err := writeSpanAtomically(span, outputPath); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

func writeSpanAtomically(span *api.PageSpan, outputPath string) error {
	tempFile, err := os.CreateTemp(filepath.Dir(outputPath), ".pdfmerge-*.pdf")
	if err != nil {
		return fmt.Errorf("创建临时单页 PDF: %w", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)

	if _, err := io.Copy(tempFile, span.Reader); err != nil {
		tempFile.Close()
		return fmt.Errorf("写入 %s: %w", filepath.Base(outputPath), err)
	}
	if err := tempFile.Chmod(0o644); err != nil {
		tempFile.Close()
		return fmt.Errorf("设置输出权限: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("关闭临时单页 PDF: %w", err)
	}
	if err := os.Rename(tempPath, outputPath); err != nil {
		return fmt.Errorf("保存 %s: %w", filepath.Base(outputPath), err)
	}
	return nil
}

func configuration() *model.Configuration {
	api.DisableConfigDir()
	configuration := model.NewDefaultConfiguration()
	configuration.ValidationMode = model.ValidationRelaxed
	return configuration
}

func scanInputFiles(inputDirectory string) ([]string, error) {
	entries, err := os.ReadDir(inputDirectory)
	if err != nil {
		return nil, fmt.Errorf("读取输入目录: %w", err)
	}

	var inputFiles []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".pdf") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil, fmt.Errorf("读取 input/%s: %w", entry.Name(), infoErr)
		}
		if info.Mode().IsRegular() {
			inputFiles = append(inputFiles, filepath.Join(inputDirectory, entry.Name()))
		}
	}
	sort.Slice(inputFiles, func(left, right int) bool {
		return strings.ToLower(filepath.Base(inputFiles[left])) <
			strings.ToLower(filepath.Base(inputFiles[right]))
	})
	return inputFiles, nil
}

func bytesToMiB(value int64) float64 {
	return float64(value) / 1024 / 1024
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `toolbox pdfmerge - 批量合并或按页拆分 PDF

用法:
  toolbox pdfmerge          批处理 input/pdfmerge/ → output/pdfmerge/

规则:
  多个 PDF      默认合并为一个（按文件名顺序），也可改为逐个按页拆分
  单个多页 PDF  默认按页拆分为单页 PDF
  单个单页 PDF  无需处理

示例:
  把 a.pdf、b.pdf 放进 input/pdfmerge/ 后运行 toolbox pdfmerge，
  选择 1 得到 output/pdfmerge/合并-20260101-120000.pdf，
  选择 2 得到 output/pdfmerge/a/a-第01页.pdf …
`)
}
