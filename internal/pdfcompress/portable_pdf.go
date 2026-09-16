package pdfcompress

import (
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	_ "image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	_ "github.com/hhrutter/tiff"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

const (
	portableMaxImageBytes  = 3_600_000_000
	portableMaxImagePixels = portableMaxImageBytes / 4
)

type sourceImageInfo struct {
	width  int
	height int
}

func portablePDFConfiguration(command model.CommandMode) *model.Configuration {
	api.DisableConfigDir()
	configuration := model.NewDefaultConfiguration()
	configuration.Cmd = command
	configuration.ValidationMode = model.ValidationRelaxed
	configuration.Limits.MaxImagePixels = portableMaxImagePixels
	configuration.Limits.MaxImageBytes = portableMaxImageBytes
	configuration.Limits.MaxDecodeBytes = portableMaxImageBytes
	return configuration
}

func pageInfoFromContext(context *model.Context) (pageInfo, error) {
	if context.PageCount < 1 {
		return pageInfo{}, fmt.Errorf("PDF 不包含页面")
	}

	boundaries, err := context.PageBoundaries(types.IntSet{1: true})
	if err != nil {
		return pageInfo{}, fmt.Errorf("读取 PDF 页面边界: %w", err)
	}
	if len(boundaries) < 1 {
		return pageInfo{}, fmt.Errorf("未读取到第一页边界")
	}

	boundary := boundaries[0]
	rectangle := boundary.MediaBox()
	if rectangle == nil {
		rectangle = boundary.CropBox()
	}
	if rectangle == nil {
		return pageInfo{}, fmt.Errorf("PDF 第一页缺少 MediaBox 和 CropBox")
	}

	return pageInfo{
		pages:  context.PageCount,
		width:  rectangle.Width(),
		height: rectangle.Height(),
		rotate: boundary.Rot,
	}, nil
}

func extractPortablePDFImage(
	inputPath string,
	tempDirectory string,
) (pageInfo, sourceImageInfo, string, error) {
	input, err := os.Open(inputPath)
	if err != nil {
		return pageInfo{}, sourceImageInfo{}, "", fmt.Errorf("打开输入 PDF: %w", err)
	}

	configuration := portablePDFConfiguration(model.EXTRACTIMAGES)
	context, err := api.ReadValidateAndOptimize(input, configuration)
	if err != nil {
		input.Close()
		return pageInfo{}, sourceImageInfo{}, "", fmt.Errorf("解析输入 PDF: %w", err)
	}

	page, err := pageInfoFromContext(context)
	if err != nil {
		input.Close()
		return pageInfo{}, sourceImageInfo{}, "", err
	}
	if page.pages != 1 {
		input.Close()
		return pageInfo{}, sourceImageInfo{}, "", fmt.Errorf(
			"当前程序仅支持单页长图 PDF，输入文件共有 %d 页",
			page.pages,
		)
	}
	if page.rotate != 0 {
		input.Close()
		return pageInfo{}, sourceImageInfo{}, "", fmt.Errorf(
			"当前程序不支持旋转页面，页面旋转角度为 %d",
			page.rotate,
		)
	}
	if page.width <= 0 || page.height <= 0 {
		input.Close()
		return pageInfo{}, sourceImageInfo{}, "", fmt.Errorf(
			"PDF 页面尺寸无效: %.6fx%.6f",
			page.width,
			page.height,
		)
	}

	stubs, err := pdfcpu.ExtractPageImages(context, 1, true)
	if err != nil {
		input.Close()
		return pageInfo{}, sourceImageInfo{}, "", fmt.Errorf("读取 PDF 图像信息: %w", err)
	}

	primaryObjectNumber, primary, primaryCount := selectPrimaryImage(stubs)
	if primaryCount == 0 {
		input.Close()
		return pageInfo{}, sourceImageInfo{}, "", fmt.Errorf(
			"当前程序未找到主体图像",
		)
	}
	if primary.IsImgMask || primary.HasImgMask {
		input.Close()
		return pageInfo{}, sourceImageInfo{}, "", fmt.Errorf("当前程序不支持二值图像蒙版")
	}
	if primary.Width < 1 || primary.Height < 1 {
		input.Close()
		return pageInfo{}, sourceImageInfo{}, "", fmt.Errorf(
			"主体图像尺寸无效: %dx%d",
			primary.Width,
			primary.Height,
		)
	}
	if primary.Width > maxJPEGDimension {
		input.Close()
		return pageInfo{}, sourceImageInfo{}, "", fmt.Errorf(
			"主体图像宽度 %d 超过 JPEG 可处理上限 %d",
			primary.Width,
			maxJPEGDimension,
		)
	}
	if primary.HasSMask {
		log.Printf("检测到软透明蒙版，将使用纯 Go 在白色背景上合成")
	}

	fullImages, err := pdfcpu.ExtractPageImages(context, 1, false)
	if err != nil {
		input.Close()
		return pageInfo{}, sourceImageInfo{}, "", fmt.Errorf("提取 PDF 主体图像: %w", err)
	}
	extracted, found := fullImages[primaryObjectNumber]
	if !found || extracted.Reader == nil {
		input.Close()
		return pageInfo{}, sourceImageInfo{}, "", fmt.Errorf(
			"未能提取主体图像对象 %d",
			primaryObjectNumber,
		)
	}

	fileType := strings.ToLower(strings.TrimSpace(extracted.FileType))
	if fileType == "" {
		fileType = "png"
	}
	sourcePath := filepath.Join(tempDirectory, "source."+fileType)
	output, err := os.Create(sourcePath)
	if err != nil {
		input.Close()
		return pageInfo{}, sourceImageInfo{}, "", fmt.Errorf("创建提取图像: %w", err)
	}
	_, copyErr := io.Copy(output, extracted.Reader)
	closeOutputErr := output.Close()
	closeInputErr := input.Close()
	if copyErr != nil {
		return pageInfo{}, sourceImageInfo{}, "", fmt.Errorf("写入提取图像: %w", copyErr)
	}
	if closeOutputErr != nil {
		return pageInfo{}, sourceImageInfo{}, "", fmt.Errorf("关闭提取图像: %w", closeOutputErr)
	}
	if closeInputErr != nil {
		return pageInfo{}, sourceImageInfo{}, "", fmt.Errorf("关闭输入 PDF: %w", closeInputErr)
	}

	fullImages = nil
	stubs = nil
	context = nil
	runtime.GC()

	return page, sourceImageInfo{
		width:  primary.Width,
		height: primary.Height,
	}, sourcePath, nil
}

func selectPrimaryImage(stubs map[int]model.Image) (int, model.Image, int) {
	// ponytail: choose the largest embedded image; full page compositing is the upgrade path for layered PDFs.
	primaryObjectNumber := 0
	var primary model.Image
	primaryCount := 0
	for objectNumber, candidate := range stubs {
		if candidate.Thumb {
			continue
		}
		primaryCount++
		if primaryCount == 1 ||
			candidate.Size > primary.Size ||
			(candidate.Size == primary.Size && objectNumber < primaryObjectNumber) {
			primaryObjectNumber = objectNumber
			primary = candidate
		}
	}
	return primaryObjectNumber, primary, primaryCount
}

func generatePortableJPEGStrips(
	sourcePath string,
	tempDirectory string,
	expectedWidth int,
	expectedHeight int,
	quality int,
	stripHeight int,
) error {
	input, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("打开提取图像: %w", err)
	}
	source, _, decodeErr := image.Decode(input)
	closeErr := input.Close()
	if decodeErr != nil {
		return fmt.Errorf("解码提取图像: %w", decodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("关闭提取图像: %w", closeErr)
	}

	bounds := source.Bounds()
	if bounds.Dx() != expectedWidth || bounds.Dy() != expectedHeight {
		return fmt.Errorf(
			"提取图像尺寸为 %dx%d，预期为 %dx%d",
			bounds.Dx(),
			bounds.Dy(),
			expectedWidth,
			expectedHeight,
		)
	}

	canvasHeight := stripHeight
	if expectedHeight < canvasHeight {
		canvasHeight = expectedHeight
	}
	canvas := image.NewRGBA(image.Rect(0, 0, expectedWidth, canvasHeight))

	for y, index := 0, 0; y < expectedHeight; y, index = y+stripHeight, index+1 {
		height := stripHeight
		if remaining := expectedHeight - y; remaining < height {
			height = remaining
		}

		target := canvas
		if height != canvasHeight {
			target = canvas.SubImage(image.Rect(0, 0, expectedWidth, height)).(*image.RGBA)
		}
		draw.Draw(target, target.Bounds(), image.White, image.Point{}, draw.Src)
		draw.Draw(
			target,
			target.Bounds(),
			source,
			image.Pt(bounds.Min.X, bounds.Min.Y+y),
			draw.Over,
		)

		outputPath := filepath.Join(
			tempDirectory,
			fmt.Sprintf("strip-%05d.jpg", index),
		)
		output, err := os.Create(outputPath)
		if err != nil {
			return fmt.Errorf("创建 JPEG 条带 %d: %w", index, err)
		}
		encodeErr := jpeg.Encode(output, target, &jpeg.Options{Quality: quality})
		closeErr := output.Close()
		if encodeErr != nil {
			return fmt.Errorf("编码 JPEG 条带 %d: %w", index, encodeErr)
		}
		if closeErr != nil {
			return fmt.Errorf("关闭 JPEG 条带 %d: %w", index, closeErr)
		}
	}

	source = nil
	canvas = nil
	runtime.GC()
	return nil
}

func inspectPortablePDF(path string) (pageInfo, error) {
	input, err := os.Open(path)
	if err != nil {
		return pageInfo{}, fmt.Errorf("打开 PDF: %w", err)
	}
	defer input.Close()

	configuration := portablePDFConfiguration(model.VALIDATE)
	context, err := api.ReadValidateAndOptimize(input, configuration)
	if err != nil {
		return pageInfo{}, fmt.Errorf("解析 PDF: %w", err)
	}
	return pageInfoFromContext(context)
}
