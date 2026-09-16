package pdfcompress

import (
	"bufio"
	"fmt"
	"image"
	_ "image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

var regressionBinary string

func TestSelectPrimaryImageChoosesLargestNonThumbnail(t *testing.T) {
	objectNumber, primary, count := selectPrimaryImage(map[int]model.Image{
		14: {ObjNr: 14, Size: 73_657_435},
		15: {ObjNr: 15, Size: 2_142_000},
		16: {ObjNr: 16, Size: 2_300_000},
		17: {ObjNr: 17, Size: 1_700_000},
		18: {ObjNr: 18, Size: 80_000, Thumb: true},
	})
	if objectNumber != 14 || primary.ObjNr != 14 || primary.Size != 73_657_435 {
		t.Fatalf(
			"selected image = #%d size=%d, want #14 size=73657435",
			objectNumber,
			primary.Size,
		)
	}
	if count != 4 {
		t.Fatalf("candidate count = %d, want 4", count)
	}
}

type observedPDFImage struct {
	width    int
	height   int
	encoding string
}

func TestMain(m *testing.M) {
	temporaryDirectory, err := os.MkdirTemp("", "toolbox-tests-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create test directory: %v\n", err)
		os.Exit(1)
	}

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		os.RemoveAll(temporaryDirectory)
		fmt.Fprintln(os.Stderr, "locate test source: runtime.Caller failed")
		os.Exit(1)
	}
	// internal/pdfcompress -> repository root
	repositoryRoot := filepath.Dir(filepath.Dir(filepath.Dir(testFile)))
	regressionBinary = filepath.Join(temporaryDirectory, "toolbox")

	build := exec.Command("go", "build", "-o", regressionBinary, "./cmd/toolbox")
	build.Dir = repositoryRoot
	if output, buildErr := build.CombinedOutput(); buildErr != nil {
		fmt.Fprintf(
			os.Stderr,
			"build regression binary: %v\n%s\n",
			buildErr,
			output,
		)
		os.RemoveAll(temporaryDirectory)
		os.Exit(1)
	}

	exitCode := m.Run()
	if err := os.RemoveAll(temporaryDirectory); err != nil {
		fmt.Fprintf(os.Stderr, "remove test directory: %v\n", err)
		if exitCode == 0 {
			exitCode = 1
		}
	}
	os.Exit(exitCode)
}

func TestCLICompressesSinglePageImagePDF(t *testing.T) {
	requireExternalTools(t, "magick", "pdfinfo", "pdfimages", "pdftoppm")

	testDirectory := t.TempDir()
	inputPath := filepath.Join(testDirectory, "gradient-source.pdf")
	outputPath := filepath.Join(testDirectory, "gradient-compressed.pdf")

	runExternal(
		t,
		"magick",
		"-size", "120x360",
		"gradient:#102030-#f0c050",
		inputPath,
	)
	inputStat := requireFileStat(t, inputPath)

	output := runCompressor(
		t,
		"-input", inputPath,
		"-output", outputPath,
		"-quality", "84",
		"-strip-height", "128",
	)
	if !strings.Contains(output, "写入单页 PDF，共 3 个图像条带") {
		t.Fatalf("compression log did not report three strips:\n%s", output)
	}

	outputStat := requireFileStat(t, outputPath)
	if outputStat.Size() >= inputStat.Size() {
		t.Fatalf(
			"compressed fixture is not smaller: input=%d output=%d",
			inputStat.Size(),
			outputStat.Size(),
		)
	}
	if outputStat.Mode().Perm() != 0o644 {
		t.Fatalf(
			"output mode = %04o, want 0644",
			outputStat.Mode().Perm(),
		)
	}

	info := readPDFInfoForTest(t, outputPath)
	requirePDFInfoValue(t, info, "Pages", "1")
	requirePDFInfoValue(t, info, "Page size", "120 x 360 pts")
	requirePDFInfoValue(t, info, "Page rot", "0")
	requirePDFInfoValue(t, info, "Encrypted", "no")

	images := readPDFImagesForTest(t, outputPath)
	expectedHeights := []int{128, 128, 104}
	if len(images) != len(expectedHeights) {
		t.Fatalf("embedded image count = %d, want %d", len(images), len(expectedHeights))
	}
	for index, item := range images {
		if item.width != 120 {
			t.Errorf("strip %d width = %d, want 120", index, item.width)
		}
		if item.height != expectedHeights[index] {
			t.Errorf(
				"strip %d height = %d, want %d",
				index,
				item.height,
				expectedHeights[index],
			)
		}
		if item.encoding != "jpeg" {
			t.Errorf("strip %d encoding = %q, want jpeg", index, item.encoding)
		}
	}

	sourceRender := renderPDF(t, inputPath, filepath.Join(testDirectory, "source-render"))
	outputRender := renderPDF(t, outputPath, filepath.Join(testDirectory, "output-render"))
	rmse := normalizedRMSE(t, sourceRender, outputRender)
	if rmse > 0.08 {
		t.Fatalf("normalized render RMSE = %.6f, want <= 0.08", rmse)
	}

	rendered := decodeImage(t, outputRender)
	minimum, maximum := luminanceRange(rendered)
	if maximum-minimum < 0.40 {
		t.Fatalf(
			"rendered output has insufficient visual range: min=%.4f max=%.4f",
			minimum,
			maximum,
		)
	}
}

func TestMenuModeCompressesEveryPDFInInput(t *testing.T) {
	requireExternalTools(t, "magick", "pdfinfo", "pdfimages")

	projectDirectory := t.TempDir()
	inputDirectory := filepath.Join(projectDirectory, "input", "pdf")
	outputDirectory := filepath.Join(projectDirectory, "output", "pdf")
	if err := os.MkdirAll(inputDirectory, 0o755); err != nil {
		t.Fatalf("create input directory: %v", err)
	}

	clickableBinary := filepath.Join(projectDirectory, "toolbox")
	copyFile(t, regressionBinary, clickableBinary, 0o755)

	firstInput := filepath.Join(inputDirectory, "first.pdf")
	secondInput := filepath.Join(inputDirectory, "SECOND.PDF")
	runExternal(
		t,
		"magick",
		"-size", "100x240",
		"gradient:#102030-#e0a040",
		firstInput,
	)
	runExternal(
		t,
		"magick",
		"-size", "80x160",
		"gradient:#203050-#d0b060",
		secondInput,
	)
	if err := os.WriteFile(
		filepath.Join(inputDirectory, "ignore.txt"),
		[]byte("not a PDF"),
		0o644,
	); err != nil {
		t.Fatalf("write non-PDF fixture: %v", err)
	}

	command := exec.Command(clickableBinary)
	command.Stdin = strings.NewReader("1\n")
	command.Env = environmentWithPath("")
	combinedOutput, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("menu mode failed: %v\n%s", err, combinedOutput)
	}
	output := string(combinedOutput)
	if !strings.Contains(output, "批处理完成：成功 2，失败 0") {
		t.Fatalf("unexpected batch summary:\n%s", output)
	}

	expectedOutputs := []string{
		filepath.Join(outputDirectory, "first-压缩版.pdf"),
		filepath.Join(outputDirectory, "SECOND-压缩版.pdf"),
	}
	for _, path := range expectedOutputs {
		requireFileStat(t, path)
		info := readPDFInfoForTest(t, path)
		requirePDFInfoValue(t, info, "Pages", "1")
	}
	if _, err := os.Stat(filepath.Join(outputDirectory, "ignore-压缩版.pdf")); !os.IsNotExist(err) {
		t.Fatalf("non-PDF input produced an output file: %v", err)
	}
}

func TestMenuModeCanGenerateMultipleQualities(t *testing.T) {
	requireExternalTools(t, "magick", "pdfinfo")

	projectDirectory := t.TempDir()
	inputDirectory := filepath.Join(projectDirectory, "input", "pdf")
	outputDirectory := filepath.Join(projectDirectory, "output", "pdf")
	if err := os.MkdirAll(inputDirectory, 0o755); err != nil {
		t.Fatalf("create input directory: %v", err)
	}

	clickableBinary := filepath.Join(projectDirectory, "toolbox")
	copyFile(t, regressionBinary, clickableBinary, 0o755)

	inputPath := filepath.Join(inputDirectory, "portfolio.pdf")
	runExternal(
		t,
		"magick",
		"-size", "80x220",
		"gradient:#102030-#e0a040",
		inputPath,
	)

	command := exec.Command(clickableBinary)
	command.Stdin = strings.NewReader("1\n40,50\n")
	combinedOutput, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("multi-quality menu run failed: %v\n%s", err, combinedOutput)
	}
	output := string(combinedOutput)
	if !strings.Contains(output, "批处理完成：成功 2，失败 0") {
		t.Fatalf("unexpected batch summary:\n%s", output)
	}

	expectedOutputs := []string{
		filepath.Join(outputDirectory, "portfolio-压缩版-q40.pdf"),
		filepath.Join(outputDirectory, "portfolio-压缩版-q50.pdf"),
	}
	for _, path := range expectedOutputs {
		requireFileStat(t, path)
		info := readPDFInfoForTest(t, path)
		requirePDFInfoValue(t, info, "Pages", "1")
	}
	if _, err := os.Stat(filepath.Join(outputDirectory, "portfolio-压缩版.pdf")); !os.IsNotExist(err) {
		t.Fatalf("single-quality output name was produced during multi-quality batch: %v", err)
	}
}

func TestMenuModeCreatesEmptyInputAndOutputDirectories(t *testing.T) {
	projectDirectory := t.TempDir()
	clickableBinary := filepath.Join(projectDirectory, "toolbox")
	copyFile(t, regressionBinary, clickableBinary, 0o755)

	command := exec.Command(clickableBinary)
	command.Stdin = strings.NewReader("1\n")
	combinedOutput, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("empty menu mode failed: %v\n%s", err, combinedOutput)
	}
	output := string(combinedOutput)
	if !strings.Contains(output, "输入目录中没有 PDF 文件，无需处理") {
		t.Fatalf("unexpected empty-input message:\n%s", output)
	}

	for _, directoryName := range []string{
		filepath.Join(projectDirectory, "input", "pdf"),
		filepath.Join(projectDirectory, "output", "pdf"),
	} {
		info, statErr := os.Stat(directoryName)
		if statErr != nil {
			t.Fatalf("stat %s: %v", directoryName, statErr)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", directoryName)
		}
	}
}

func TestBatchModeContinuesAfterOnePDFHasFailed(t *testing.T) {
	requireExternalTools(t, "magick", "pdfinfo", "pdfimages")

	projectDirectory := t.TempDir()
	inputDirectory := filepath.Join(projectDirectory, "input", "pdf")
	if err := os.MkdirAll(inputDirectory, 0o755); err != nil {
		t.Fatalf("create input directory: %v", err)
	}

	clickableBinary := filepath.Join(projectDirectory, "toolbox")
	copyFile(t, regressionBinary, clickableBinary, 0o755)
	if err := os.WriteFile(
		filepath.Join(inputDirectory, "a-broken.pdf"),
		[]byte("this is not a PDF"),
		0o644,
	); err != nil {
		t.Fatalf("write broken PDF fixture: %v", err)
	}
	runExternal(
		t,
		"magick",
		"-size", "90x180",
		"gradient:#102030-#d09040",
		filepath.Join(inputDirectory, "b-valid.pdf"),
	)

	// "toolbox pdf" is the scriptable batch entry point; it exits non-zero
	// when any input fails, unlike the interactive menu which keeps going.
	command := exec.Command(clickableBinary, "pdf")
	command.Stdin = strings.NewReader("")
	command.Env = environmentWithPath("")
	combinedOutput, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("batch with a broken PDF unexpectedly succeeded:\n%s", combinedOutput)
	}
	output := string(combinedOutput)
	if !strings.Contains(output, "批处理完成：成功 1，失败 1") {
		t.Fatalf("unexpected partial-failure summary:\n%s", output)
	}

	validOutput := filepath.Join(projectDirectory, "output", "pdf", "b-valid-压缩版.pdf")
	requireFileStat(t, validOutput)
	if _, statErr := os.Stat(
		filepath.Join(projectDirectory, "output", "pdf", "a-broken-压缩版.pdf"),
	); !os.IsNotExist(statErr) {
		t.Fatalf("broken PDF left an output file: %v", statErr)
	}
}

func TestPromptForBatchQualitiesUsesDefaultForEmptyInput(t *testing.T) {
	var output strings.Builder
	qualities, err := promptForBatchQualities(strings.NewReader("\n"), &output, nil)
	if err != nil {
		t.Fatalf("prompt failed: %v\n%s", err, output.String())
	}
	requireQualities(t, qualities, []int{defaultBatchQuality})
	if !strings.Contains(output.String(), "已选择：推荐均衡（q40）") {
		t.Fatalf("prompt did not report default selection:\n%s", output.String())
	}
}

func TestPromptForBatchQualitiesAcceptsPresetMultipleValuesAndRange(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []int
	}{
		{
			name:     "preset",
			input:    "2\n",
			expected: []int{50},
		},
		{
			name:     "multiple values",
			input:    "q50,40 60\n",
			expected: []int{40, 50, 60},
		},
		{
			name:     "range",
			input:    "q10-q90\n",
			expected: []int{10, 20, 30, 40, 50, 60, 70, 80, 90},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output strings.Builder
			qualities, err := promptForBatchQualities(strings.NewReader(tt.input), &output, nil)
			if err != nil {
				t.Fatalf("prompt failed: %v\n%s", err, output.String())
			}
			requireQualities(t, qualities, tt.expected)
		})
	}
}

func TestPromptForBatchQualitiesRetriesAfterInvalidInput(t *testing.T) {
	var output strings.Builder
	qualities, err := promptForBatchQualities(strings.NewReader("q0\nq35\n"), &output, nil)
	if err != nil {
		t.Fatalf("prompt failed: %v\n%s", err, output.String())
	}
	requireQualities(t, qualities, []int{35})
	if !strings.Contains(output.String(), "输入无效") {
		t.Fatalf("prompt did not report invalid input:\n%s", output.String())
	}
}

func TestPromptForBatchQualitiesUsesRememberedDefault(t *testing.T) {
	var output strings.Builder
	qualities, err := promptForBatchQualities(strings.NewReader("\n"), &output, []int{35, 45})
	if err != nil {
		t.Fatalf("prompt failed: %v\n%s", err, output.String())
	}
	requireQualities(t, qualities, []int{35, 45})
	if !strings.Contains(output.String(), "上次使用（q35, q45）") {
		t.Fatalf("prompt did not offer remembered default:\n%s", output.String())
	}
}

func TestRememberedQualitiesRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name     string
		values   []int
		expected []int
	}{
		{name: "empty", values: nil, expected: nil},
		{name: "valid list", values: []int{40, 50}, expected: []int{40, 50}},
		{name: "invalid entry discards list", values: []int{40, 0}, expected: nil},
		{name: "out of range discards list", values: []int{101}, expected: nil},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := rememberedQualities(tt.values)
			if tt.expected == nil {
				if got != nil {
					t.Fatalf("remembered = %v, want nil", got)
				}
				return
			}
			requireQualities(t, got, tt.expected)
		})
	}
}

func TestCLIRejectsOutputEqualToInput(t *testing.T) {
	testDirectory := t.TempDir()
	inputPath := filepath.Join(testDirectory, "same.pdf")
	if err := os.WriteFile(inputPath, []byte("%PDF-test"), 0o644); err != nil {
		t.Fatalf("write input fixture: %v", err)
	}

	output, err := runCompressorExpectFailure(
		"-input", inputPath,
		"-output", inputPath,
	)
	if err == nil {
		t.Fatal("compressor succeeded when output path equaled input path")
	}
	if !strings.Contains(output, "输出路径不能覆盖输入文件") {
		t.Fatalf("unexpected error output:\n%s", output)
	}
}

func TestCLIRejectsInvalidQuality(t *testing.T) {
	testDirectory := t.TempDir()
	inputPath := filepath.Join(testDirectory, "input.pdf")
	outputPath := filepath.Join(testDirectory, "output.pdf")

	output, err := runCompressorExpectFailure(
		"-input", inputPath,
		"-output", outputPath,
		"-quality", "101",
	)
	if err == nil {
		t.Fatal("compressor accepted quality 101")
	}
	if !strings.Contains(output, "-quality 必须在 1-100 之间") {
		t.Fatalf("unexpected error output:\n%s", output)
	}
}

func TestCLIRejectsInvalidStripHeight(t *testing.T) {
	testDirectory := t.TempDir()
	inputPath := filepath.Join(testDirectory, "input.pdf")
	outputPath := filepath.Join(testDirectory, "output.pdf")

	output, err := runCompressorExpectFailure(
		"-input", inputPath,
		"-output", outputPath,
		"-strip-height", "65501",
	)
	if err == nil {
		t.Fatal("compressor accepted strip height 65501")
	}
	if !strings.Contains(output, "-strip-height 必须在 1-65500 之间") {
		t.Fatalf("unexpected error output:\n%s", output)
	}
}

func TestCLIRejectsMultiPagePDF(t *testing.T) {
	requireExternalTools(t, "magick", "pdfinfo")

	testDirectory := t.TempDir()
	inputPath := filepath.Join(testDirectory, "two-pages.pdf")
	outputPath := filepath.Join(testDirectory, "output.pdf")

	runExternal(
		t,
		"magick",
		"-size", "40x40", "xc:#cc2222",
		"-size", "40x40", "xc:#2244cc",
		inputPath,
	)

	info := readPDFInfoForTest(t, inputPath)
	requirePDFInfoValue(t, info, "Pages", "2")

	output, err := runCompressorExpectFailure(
		"-input", inputPath,
		"-output", outputPath,
	)
	if err == nil {
		t.Fatal("compressor accepted a two-page PDF")
	}
	if !strings.Contains(output, "当前程序仅支持单页长图 PDF，输入文件共有 2 页") {
		t.Fatalf("unexpected error output:\n%s", output)
	}
	if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
		t.Fatalf("failed compression left an output file: %v", statErr)
	}
}

func requireQualities(t *testing.T, actual, expected []int) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatalf("qualities = %v, want %v", actual, expected)
	}
	for index := range expected {
		if actual[index] != expected[index] {
			t.Fatalf("qualities = %v, want %v", actual, expected)
		}
	}
}

func requireExternalTools(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("external test dependency %q is unavailable", name)
		}
	}
}

func runCompressor(t *testing.T, args ...string) string {
	t.Helper()
	output, err := runCompressorExpectFailure(args...)
	if err != nil {
		t.Fatalf("compressor failed: %v\n%s", err, output)
	}
	return output
}

func runCompressorExpectFailure(args ...string) (string, error) {
	command := exec.Command(regressionBinary, append([]string{"pdf"}, args...)...)
	output, err := command.CombinedOutput()
	return string(output), err
}

func runExternal(t *testing.T, name string, args ...string) string {
	t.Helper()
	command := exec.Command(name, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf(
			"%s %s failed: %v\n%s",
			name,
			strings.Join(args, " "),
			err,
			output,
		)
	}
	return string(output)
}

func requireFileStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("%s is not a regular file", path)
	}
	return info
}

func copyFile(t *testing.T, source, destination string, mode os.FileMode) {
	t.Helper()
	sourceFile, err := os.Open(source)
	if err != nil {
		t.Fatalf("open %s: %v", source, err)
	}
	destinationFile, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		sourceFile.Close()
		t.Fatalf("create %s: %v", destination, err)
	}
	if _, err := io.Copy(destinationFile, sourceFile); err != nil {
		sourceFile.Close()
		destinationFile.Close()
		t.Fatalf("copy %s to %s: %v", source, destination, err)
	}
	if err := sourceFile.Close(); err != nil {
		destinationFile.Close()
		t.Fatalf("close %s: %v", source, err)
	}
	if err := destinationFile.Close(); err != nil {
		t.Fatalf("close %s: %v", destination, err)
	}
}

func environmentWithPath(path string) []string {
	environment := os.Environ()
	filtered := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		if strings.HasPrefix(item, "PATH=") {
			continue
		}
		filtered = append(filtered, item)
	}
	return append(filtered, "PATH="+path)
}

func readPDFInfoForTest(t *testing.T, path string) map[string]string {
	t.Helper()
	output := runExternal(t, "pdfinfo", path)
	values := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		index := strings.IndexByte(line, ':')
		if index < 0 {
			continue
		}
		key := strings.TrimSpace(line[:index])
		value := strings.TrimSpace(line[index+1:])
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan pdfinfo output: %v", err)
	}
	return values
}

func requirePDFInfoValue(
	t *testing.T,
	info map[string]string,
	key string,
	expected string,
) {
	t.Helper()
	actual, ok := info[key]
	if !ok {
		t.Fatalf("pdfinfo did not contain %q", key)
	}
	if actual != expected {
		t.Fatalf("pdfinfo %s = %q, want %q", key, actual, expected)
	}
}

func readPDFImagesForTest(t *testing.T, path string) []observedPDFImage {
	t.Helper()
	output := runExternal(t, "pdfimages", "-list", path)

	var images []observedPDFImage
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 9 || fields[2] != "image" {
			continue
		}
		width, widthErr := strconv.Atoi(fields[3])
		height, heightErr := strconv.Atoi(fields[4])
		if widthErr != nil || heightErr != nil {
			continue
		}
		images = append(images, observedPDFImage{
			width:    width,
			height:   height,
			encoding: fields[8],
		})
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan pdfimages output: %v", err)
	}
	return images
}

func renderPDF(t *testing.T, path, prefix string) string {
	t.Helper()
	runExternal(
		t,
		"pdftoppm",
		"-f", "1",
		"-l", "1",
		"-singlefile",
		"-r", "72",
		"-png",
		path,
		prefix,
	)
	renderedPath := prefix + ".png"
	requireFileStat(t, renderedPath)
	return renderedPath
}

func decodeImage(t *testing.T, path string) image.Image {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open rendered image %s: %v", path, err)
	}
	decoded, _, decodeErr := image.Decode(file)
	closeErr := file.Close()
	if decodeErr != nil {
		t.Fatalf("decode rendered image %s: %v", path, decodeErr)
	}
	if closeErr != nil {
		t.Fatalf("close rendered image %s: %v", path, closeErr)
	}
	return decoded
}

func normalizedRMSE(t *testing.T, leftPath, rightPath string) float64 {
	t.Helper()
	left := decodeImage(t, leftPath)
	right := decodeImage(t, rightPath)
	if left.Bounds() != right.Bounds() {
		t.Fatalf(
			"render bounds differ: left=%v right=%v",
			left.Bounds(),
			right.Bounds(),
		)
	}

	var sumSquared float64
	var samples uint64
	bounds := left.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			leftRed, leftGreen, leftBlue, _ := left.At(x, y).RGBA()
			rightRed, rightGreen, rightBlue, _ := right.At(x, y).RGBA()
			channels := [][2]uint32{
				{leftRed, rightRed},
				{leftGreen, rightGreen},
				{leftBlue, rightBlue},
			}
			for _, channel := range channels {
				difference := float64(channel[0]) - float64(channel[1])
				sumSquared += difference * difference
				samples++
			}
		}
	}

	return math.Sqrt(sumSquared/float64(samples)) / 65535
}

func luminanceRange(input image.Image) (float64, float64) {
	minimum := 1.0
	maximum := 0.0
	bounds := input.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			red, green, blue, _ := input.At(x, y).RGBA()
			luminance := (0.2126*float64(red) +
				0.7152*float64(green) +
				0.0722*float64(blue)) / 65535
			if luminance < minimum {
				minimum = luminance
			}
			if luminance > maximum {
				maximum = luminance
			}
		}
	}
	return minimum, maximum
}
