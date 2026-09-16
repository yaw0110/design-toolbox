// Package pdfcompress compresses single-page, image-based portfolio PDFs.
// It is ported verbatim from the pdf-compressor project.
package pdfcompress

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	maxJPEGDimension    = 65500
	defaultQuality      = 84
	defaultStripSize    = 16000
	defaultBatchQuality = 40
)

type options struct {
	input       string
	output      string
	quality     int
	stripHeight int
	keepTemp    bool
}

type pageInfo struct {
	pages  int
	width  float64
	height float64
	rotate int
}

type imageFile struct {
	path   string
	width  int
	height int
}

type countWriter struct {
	writer io.Writer
	count  int64
}

func (w *countWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.count += int64(n)
	return n, err
}

// ParseFlags parses single-file compression arguments, e.g.
// ["-input", "a.pdf", "-output", "b.pdf", "-quality", "84"].
func ParseFlags(args []string) (options, error) {
	var opts options

	flagSet := flag.NewFlagSet("toolbox pdf", flag.ContinueOnError)
	flagSet.StringVar(&opts.input, "input", "", "输入 PDF 路径")
	flagSet.StringVar(&opts.output, "output", "", "输出 PDF 路径")
	flagSet.IntVar(&opts.quality, "quality", defaultQuality, "JPEG 质量，范围 1-100")
	flagSet.IntVar(
		&opts.stripHeight,
		"strip-height",
		defaultStripSize,
		"每个 JPEG 条带的像素高度，最大 65500",
	)
	flagSet.BoolVar(&opts.keepTemp, "keep-temp", false, "保留临时提取文件")
	flagSet.Parse(args)

	if flagSet.NArg() > 0 {
		return options{}, fmt.Errorf("无法识别的参数: %s", strings.Join(flagSet.Args(), " "))
	}
	if opts.input == "" || opts.output == "" {
		flagSet.Usage()
		return options{}, errors.New("必须同时提供 -input 和 -output")
	}
	if opts.quality < 1 || opts.quality > 100 {
		return options{}, fmt.Errorf("-quality 必须在 1-100 之间，当前值为 %d", opts.quality)
	}
	if opts.stripHeight < 1 || opts.stripHeight > maxJPEGDimension {
		return options{}, fmt.Errorf(
			"-strip-height 必须在 1-%d 之间，当前值为 %d",
			maxJPEGDimension,
			opts.stripHeight,
		)
	}

	inputAbs, err := filepath.Abs(opts.input)
	if err != nil {
		return options{}, fmt.Errorf("解析输入路径: %w", err)
	}
	outputAbs, err := filepath.Abs(opts.output)
	if err != nil {
		return options{}, fmt.Errorf("解析输出路径: %w", err)
	}
	if inputAbs == outputAbs {
		return options{}, errors.New("输出路径不能覆盖输入文件")
	}
	opts.input = inputAbs
	opts.output = outputAbs

	return opts, nil
}

// Run compresses one PDF file and reports progress through the standard log.
func run(opts options) error {
	if info, err := os.Stat(opts.input); err != nil {
		return fmt.Errorf("读取输入文件: %w", err)
	} else if !info.Mode().IsRegular() {
		return fmt.Errorf("输入路径不是普通文件: %s", opts.input)
	}

	tempDir, err := os.MkdirTemp("", "go-pdf-compress-*")
	if err != nil {
		return fmt.Errorf("创建临时目录: %w", err)
	}
	if opts.keepTemp {
		log.Printf("临时目录将被保留: %s", tempDir)
	} else {
		defer os.RemoveAll(tempDir)
	}

	log.Printf("使用纯 Go 解析 PDF: %s", opts.input)
	sourcePage, mainImage, sourceImagePath, err := extractPortablePDFImage(
		opts.input,
		tempDir,
	)
	if err != nil {
		return err
	}

	log.Printf(
		"生成 JPEG 条带: quality=%d, strip-height=%d",
		opts.quality,
		opts.stripHeight,
	)
	if err := generatePortableJPEGStrips(
		sourceImagePath,
		tempDir,
		mainImage.width,
		mainImage.height,
		opts.quality,
		opts.stripHeight,
	); err != nil {
		return fmt.Errorf("生成 JPEG 条带: %w", err)
	}

	strips, err := inspectJPEGStrips(tempDir, mainImage.width, mainImage.height)
	if err != nil {
		return err
	}

	log.Printf("写入单页 PDF，共 %d 个图像条带", len(strips))
	if err := writePDFAtomically(opts.output, sourcePage, strips); err != nil {
		return err
	}

	outputPage, err := inspectPortablePDF(opts.output)
	if err != nil {
		return fmt.Errorf("验证输出 PDF: %w", err)
	}
	if outputPage.pages != 1 ||
		!nearlyEqual(outputPage.width, sourcePage.width, 0.001) ||
		!nearlyEqual(outputPage.height, sourcePage.height, 0.001) {
		return fmt.Errorf(
			"输出 PDF 页面信息不匹配: pages=%d, size=%.6fx%.6f",
			outputPage.pages,
			outputPage.width,
			outputPage.height,
		)
	}

	inputStat, err := os.Stat(opts.input)
	if err != nil {
		return fmt.Errorf("读取输入文件大小: %w", err)
	}
	outputStat, err := os.Stat(opts.output)
	if err != nil {
		return fmt.Errorf("读取输出文件大小: %w", err)
	}
	reduction := (1 - float64(outputStat.Size())/float64(inputStat.Size())) * 100

	log.Printf("完成: %s", opts.output)
	log.Printf(
		"体积: %.2f MiB -> %.2f MiB，减少 %.2f%%",
		bytesToMiB(inputStat.Size()),
		bytesToMiB(outputStat.Size()),
		reduction,
	)

	return nil
}

func inspectJPEGStrips(tempDir string, expectedWidth, expectedHeight int) ([]imageFile, error) {
	paths, err := filepath.Glob(filepath.Join(tempDir, "strip-*.jpg"))
	if err != nil {
		return nil, fmt.Errorf("查找 JPEG 条带: %w", err)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil, errors.New("未生成任何 JPEG 条带")
	}

	strips := make([]imageFile, 0, len(paths))
	totalHeight := 0
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("打开 JPEG 条带 %s: %w", path, err)
		}
		config, _, decodeErr := image.DecodeConfig(file)
		closeErr := file.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("读取 JPEG 条带尺寸 %s: %w", path, decodeErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("关闭 JPEG 条带 %s: %w", path, closeErr)
		}
		if config.Width != expectedWidth {
			return nil, fmt.Errorf(
				"JPEG 条带 %s 的宽度为 %d，预期为 %d",
				path,
				config.Width,
				expectedWidth,
			)
		}
		if config.Height < 1 || config.Height > maxJPEGDimension {
			return nil, fmt.Errorf(
				"JPEG 条带 %s 的高度 %d 超出有效范围",
				path,
				config.Height,
			)
		}

		totalHeight += config.Height
		strips = append(strips, imageFile{
			path:   path,
			width:  config.Width,
			height: config.Height,
		})
	}
	if totalHeight != expectedHeight {
		return nil, fmt.Errorf(
			"所有 JPEG 条带总高度为 %d，源图像高度为 %d",
			totalHeight,
			expectedHeight,
		)
	}

	return strips, nil
}

func writePDFAtomically(outputPath string, page pageInfo, strips []imageFile) error {
	outputDir := filepath.Dir(outputPath)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("创建输出目录: %w", err)
	}

	tempOutput, err := os.CreateTemp(outputDir, ".pdf-compress-*.pdf")
	if err != nil {
		return fmt.Errorf("创建临时输出 PDF: %w", err)
	}
	tempPath := tempOutput.Name()
	defer os.Remove(tempPath)

	if err := writePDF(tempOutput, page, strips); err != nil {
		tempOutput.Close()
		return err
	}
	if err := tempOutput.Chmod(0o644); err != nil {
		tempOutput.Close()
		return fmt.Errorf("设置输出 PDF 权限: %w", err)
	}
	if err := tempOutput.Sync(); err != nil {
		tempOutput.Close()
		return fmt.Errorf("同步输出 PDF: %w", err)
	}
	if err := tempOutput.Close(); err != nil {
		return fmt.Errorf("关闭输出 PDF: %w", err)
	}
	if err := os.Rename(tempPath, outputPath); err != nil {
		return fmt.Errorf("保存输出 PDF: %w", err)
	}

	return nil
}

func writePDF(file *os.File, page pageInfo, strips []imageFile) error {
	const (
		catalogObject = 1
		pagesObject   = 2
		pageObject    = 3
		contentObject = 4
		firstImage    = 5
	)

	infoObject := firstImage + len(strips)
	objectCount := infoObject
	offsets := make([]int64, objectCount+1)

	buffered := bufio.NewWriterSize(file, 1024*1024)
	writer := &countWriter{writer: buffered}
	if _, err := io.WriteString(writer, "%PDF-1.6\n%\xE2\xE3\xCF\xD3\n"); err != nil {
		return fmt.Errorf("写入 PDF 文件头: %w", err)
	}

	writeObject := func(number int, body string) error {
		offsets[number] = writer.count
		if _, err := fmt.Fprintf(writer, "%d 0 obj\n%s\nendobj\n", number, body); err != nil {
			return fmt.Errorf("写入 PDF 对象 %d: %w", number, err)
		}
		return nil
	}

	if err := writeObject(
		catalogObject,
		fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", pagesObject),
	); err != nil {
		return err
	}
	if err := writeObject(
		pagesObject,
		fmt.Sprintf(
			"<< /Type /Pages /Kids [%d 0 R] /Count 1 >>",
			pageObject,
		),
	); err != nil {
		return err
	}

	var resources strings.Builder
	resources.WriteString("<< /ProcSet [/PDF /ImageC] /XObject <<")
	for index := range strips {
		fmt.Fprintf(
			&resources,
			" /Im%d %d 0 R",
			index,
			firstImage+index,
		)
	}
	resources.WriteString(" >> >>")

	pageBody := fmt.Sprintf(
		"<< /Type /Page /Parent %d 0 R /MediaBox [0 0 %s %s] "+
			"/Resources %s /Contents %d 0 R >>",
		pagesObject,
		formatPDFNumber(page.width),
		formatPDFNumber(page.height),
		resources.String(),
		contentObject,
	)
	if err := writeObject(pageObject, pageBody); err != nil {
		return err
	}

	totalPixelHeight := 0
	for _, strip := range strips {
		totalPixelHeight += strip.height
	}
	scaleY := page.height / float64(totalPixelHeight)

	var content bytes.Buffer
	consumedPixels := 0
	for index, strip := range strips {
		consumedPixels += strip.height
		heightPoints := float64(strip.height) * scaleY
		y := page.height - float64(consumedPixels)*scaleY
		fmt.Fprintf(
			&content,
			"q\n%s 0 0 %s 0 %s cm\n/Im%d Do\nQ\n",
			formatPDFNumber(page.width),
			formatPDFNumber(heightPoints),
			formatPDFNumber(y),
			index,
		)
	}
	if err := writeStreamObject(writer, offsets, contentObject, "", bytes.NewReader(content.Bytes()), int64(content.Len())); err != nil {
		return err
	}

	for index, strip := range strips {
		imageData, err := os.Open(strip.path)
		if err != nil {
			return fmt.Errorf("打开 JPEG 条带 %s: %w", strip.path, err)
		}
		stat, err := imageData.Stat()
		if err != nil {
			imageData.Close()
			return fmt.Errorf("读取 JPEG 条带大小 %s: %w", strip.path, err)
		}

		dictionary := fmt.Sprintf(
			"/Type /XObject /Subtype /Image /Width %d /Height %d "+
				"/ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode",
			strip.width,
			strip.height,
		)
		writeErr := writeStreamObject(
			writer,
			offsets,
			firstImage+index,
			dictionary,
			imageData,
			stat.Size(),
		)
		closeErr := imageData.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return fmt.Errorf("关闭 JPEG 条带 %s: %w", strip.path, closeErr)
		}
	}

	if err := writeObject(
		infoObject,
		"<< /Producer (Go image portfolio PDF compressor) "+
			"/Creator (design-toolbox) >>",
	); err != nil {
		return err
	}

	xrefOffset := writer.count
	if _, err := fmt.Fprintf(writer, "xref\n0 %d\n", objectCount+1); err != nil {
		return fmt.Errorf("写入 PDF xref 头: %w", err)
	}
	if _, err := io.WriteString(writer, "0000000000 65535 f \n"); err != nil {
		return fmt.Errorf("写入 PDF xref 空对象: %w", err)
	}
	for objectNumber := 1; objectCount >= objectNumber; objectNumber++ {
		if _, err := fmt.Fprintf(
			writer,
			"%010d 00000 n \n",
			offsets[objectNumber],
		); err != nil {
			return fmt.Errorf("写入 PDF xref 对象 %d: %w", objectNumber, err)
		}
	}
	if _, err := fmt.Fprintf(
		writer,
		"trailer\n<< /Size %d /Root %d 0 R /Info %d 0 R >>\n"+
			"startxref\n%d\n%%%%EOF\n",
		objectCount+1,
		catalogObject,
		infoObject,
		xrefOffset,
	); err != nil {
		return fmt.Errorf("写入 PDF trailer: %w", err)
	}
	if err := buffered.Flush(); err != nil {
		return fmt.Errorf("刷新 PDF 输出: %w", err)
	}

	return nil
}

func writeStreamObject(
	writer *countWriter,
	offsets []int64,
	number int,
	dictionary string,
	data io.Reader,
	length int64,
) error {
	offsets[number] = writer.count
	if dictionary != "" {
		dictionary += " "
	}
	if _, err := fmt.Fprintf(
		writer,
		"%d 0 obj\n<< %s/Length %d >>\nstream\n",
		number,
		dictionary,
		length,
	); err != nil {
		return fmt.Errorf("写入 PDF 流对象 %d 的字典: %w", number, err)
	}
	if _, err := io.Copy(writer, data); err != nil {
		return fmt.Errorf("写入 PDF 流对象 %d 的内容: %w", number, err)
	}
	if _, err := io.WriteString(writer, "\nendstream\nendobj\n"); err != nil {
		return fmt.Errorf("结束 PDF 流对象 %d: %w", number, err)
	}
	return nil
}

func formatPDFNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', 6, 64)
}

func nearlyEqual(left, right, tolerance float64) bool {
	difference := left - right
	if difference < 0 {
		difference = -difference
	}
	return difference <= tolerance
}

func bytesToMiB(value int64) float64 {
	return float64(value) / 1024 / 1024
}
