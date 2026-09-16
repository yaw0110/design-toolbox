// Command toolbox is 设计百宝箱 (Design Toolbox): a suite of small
// design-industry utilities in one binary. Running it without arguments
// opens an interactive menu; each tool is also available as a subcommand.
package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/yaw0110/design-toolbox/internal/app"
	"github.com/yaw0110/design-toolbox/internal/pdfcompress"
	"github.com/yaw0110/design-toolbox/internal/pdfmerge"
	"github.com/yaw0110/design-toolbox/internal/svg2gif"
)

var version = "0.1.0"

func main() {
	log.SetFlags(0)

	args := os.Args[1:]
	if len(args) == 0 {
		runMenu()
		return
	}

	switch args[0] {
	case "pdf":
		if err := pdfcompress.Main(args[1:]); err != nil {
			log.Fatal(err)
		}
	case "svg2gif":
		if err := svg2gif.Main(args[1:]); err != nil {
			log.Fatal(err)
		}
	case "version", "--version", "-v":
		fmt.Printf("design-toolbox version %s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n", args[0])
		printUsage()
		os.Exit(1)
	}
}

func runMenu() {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Println("====================================")
		fmt.Printf("  设计百宝箱 Design Toolbox v%s\n", version)
		fmt.Println("====================================")
		fmt.Println()
		fmt.Println("请选择工具：")
		fmt.Printf("  1. PDF 压缩        %s\n", inputSummary("pdf", ".pdf"))
		fmt.Printf("  2. SVG/SVGA 转 GIF %s\n", inputSummary("svg", ".svg", ".svga"))
		fmt.Printf("  3. PDF 合并/拆分   %s\n", inputSummary("pdfmerge", ".pdf"))
		fmt.Println("  0. 退出")
		fmt.Println()
		fmt.Print("请输入选择：")

		line, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println()
			return
		}

		switch strings.TrimSpace(line) {
		case "1":
			runTool(reader, "PDF 压缩", pdfcompress.RunDefaultBatch)
		case "2":
			runTool(reader, "SVG/SVGA 转 GIF", svg2gif.RunDefaultBatch)
		case "3":
			runTool(reader, "PDF 合并/拆分", pdfmerge.RunDefaultBatch)
		case "0", "q", "Q", "quit", "exit":
			return
		case "":
			fmt.Println()
		default:
			fmt.Printf("无效选择：%s\n\n", strings.TrimSpace(line))
		}
	}
}

// runTool passes the shared stdin reader into the tool so buffered input
// the menu has not consumed yet stays readable by the tool's own prompts.
func runTool(
	reader io.Reader,
	name string,
	runBatch func(stdin io.Reader, stdout io.Writer) error,
) {
	fmt.Printf("\n----- %s -----\n", name)
	if err := runBatch(reader, os.Stdout); err != nil {
		log.Printf("%s失败: %v", name, err)
	}
	fmt.Println()
	app.PauseIfInteractive("按回车返回主菜单...")
	fmt.Println()
}

func inputSummary(tool string, extensions ...string) string {
	inputDirectory, err := app.ToolInputDir(tool)
	if err != nil {
		return ""
	}
	count := countFiles(inputDirectory, extensions...)
	if count == 0 {
		return fmt.Sprintf("（%s 为空）", displayDir(inputDirectory))
	}
	return fmt.Sprintf("（%s 中发现 %d 个文件）", displayDir(inputDirectory), count)
}

func displayDir(path string) string {
	return filepath.Join("input", filepath.Base(path)) + "/"
}

func countFiles(directory string, extensions ...string) int {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return 0
	}

	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		for _, wanted := range extensions {
			if extension == wanted {
				count++
				break
			}
		}
	}
	return count
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `设计百宝箱 - 设计行业小工具集

用法:
  toolbox                  交互式主菜单（双击运行默认进入）
  toolbox pdf [参数]       PDF 压缩
  toolbox svg2gif [参数]   SVG/SVGA 转 GIF
  toolbox pdfmerge         PDF 合并/拆分
  toolbox version          显示版本

PDF 压缩:
  toolbox pdf                                批处理 input/pdf/ → output/pdf/
  toolbox pdf -input a.pdf -output b.pdf     单文件模式
                                             可选: -quality -strip-height -keep-temp

SVG/SVGA 转 GIF:
  toolbox svg2gif [选项] <source> <target>
    选项: -w/--width --height -f/--fps

PDF 合并/拆分:
  toolbox pdfmerge        多个 PDF 合并或单个多页 PDF 按页拆分
                          input/pdfmerge/ → output/pdfmerge/

目录约定（相对于程序所在目录）:
  input/pdf/        待压缩 PDF
  input/svg/        待转换 SVG/SVGA
  input/pdfmerge/   待合并/拆分 PDF
  output/pdf/       压缩结果
  output/svg/       转换结果（gif/ 与 apng/ 子目录）
  output/pdfmerge/  合并/拆分结果
`)
}
