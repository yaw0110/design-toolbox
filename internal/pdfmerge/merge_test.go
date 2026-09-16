package pdfmerge

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

func TestRunBatchMergesAllFilesByDefault(t *testing.T) {
	inputDirectory, outputDirectory := prepareInput(t, map[string]int{
		"first.pdf": 1,
		"second.pdf": 2,
	})

	if err := RunBatch(inputDirectory, outputDirectory, strings.NewReader("\n"), io.Discard); err != nil {
		t.Fatalf("RunBatch failed: %v", err)
	}

	merged := globOne(t, outputDirectory, "合并-*.pdf")
	pageCount, err := api.PageCountFile(merged)
	if err != nil {
		t.Fatalf("page count of merged file: %v", err)
	}
	if pageCount != 3 {
		t.Fatalf("merged page count = %d, want 3", pageCount)
	}
	entries := readDirEntries(t, outputDirectory)
	if len(entries) != 1 {
		t.Fatalf("output directory contains %d entries, want 1 merged PDF", len(entries))
	}
}

func TestRunBatchSplitsSingleMultiPageFileByDefault(t *testing.T) {
	inputDirectory, outputDirectory := prepareInput(t, map[string]int{
		"portfolio.pdf": 3,
	})

	if err := RunBatch(inputDirectory, outputDirectory, strings.NewReader("\n"), io.Discard); err != nil {
		t.Fatalf("RunBatch failed: %v", err)
	}

	for page := 1; page <= 3; page++ {
		pagePath := filepath.Join(outputDirectory, "portfolio",
			fmt.Sprintf("portfolio-第%02d页.pdf", page))
		pageCount, err := api.PageCountFile(pagePath)
		if err != nil {
			t.Fatalf("open split page %d: %v", page, err)
		}
		if pageCount != 1 {
			t.Fatalf("split page %d contains %d pages, want 1", page, pageCount)
		}
	}
	entries := readDirEntries(t, filepath.Join(outputDirectory, "portfolio"))
	if len(entries) != 3 {
		t.Fatalf("split output contains %d files, want 3", len(entries))
	}
}

func TestRunBatchPromptCanOverrideMergeWithSplit(t *testing.T) {
	inputDirectory, outputDirectory := prepareInput(t, map[string]int{
		"a.pdf": 2,
		"b.pdf": 1,
	})

	if err := RunBatch(inputDirectory, outputDirectory, strings.NewReader("2\n"), io.Discard); err != nil {
		t.Fatalf("RunBatch failed: %v", err)
	}

	requireFileExists(t, filepath.Join(outputDirectory, "a", "a-第01页.pdf"))
	requireFileExists(t, filepath.Join(outputDirectory, "a", "a-第02页.pdf"))
	requireFileExists(t, filepath.Join(outputDirectory, "b", "b-第01页.pdf"))
	if matches := globMatches(t, outputDirectory, "合并-*.pdf"); len(matches) != 0 {
		t.Fatalf("split run produced merged files: %v", matches)
	}
}

func TestRunBatchPromptCanCancel(t *testing.T) {
	inputDirectory, outputDirectory := prepareInput(t, map[string]int{
		"a.pdf": 1,
		"b.pdf": 1,
	})

	if err := RunBatch(inputDirectory, outputDirectory, strings.NewReader("0\n"), io.Discard); err != nil {
		t.Fatalf("RunBatch failed: %v", err)
	}

	entries := readDirEntries(t, outputDirectory)
	if len(entries) != 0 {
		t.Fatalf("cancelled run left output files: %v", entries)
	}
}

func TestRunBatchWithoutFilesDoesNothing(t *testing.T) {
	inputDirectory := t.TempDir()
	outputDirectory := t.TempDir()

	if err := RunBatch(inputDirectory, outputDirectory, strings.NewReader("\n"), io.Discard); err != nil {
		t.Fatalf("RunBatch failed: %v", err)
	}
	if entries := readDirEntries(t, outputDirectory); len(entries) != 0 {
		t.Fatalf("empty input produced output: %v", entries)
	}
}

func TestRunBatchWithSinglePageFileDoesNothing(t *testing.T) {
	inputDirectory, outputDirectory := prepareInput(t, map[string]int{
		"single.pdf": 1,
	})

	if err := RunBatch(inputDirectory, outputDirectory, strings.NewReader("\n"), io.Discard); err != nil {
		t.Fatalf("RunBatch failed: %v", err)
	}
	if entries := readDirEntries(t, outputDirectory); len(entries) != 0 {
		t.Fatalf("single-page input produced output: %v", entries)
	}
}

func TestRunBatchMergeFailureLeavesNoPartialOutput(t *testing.T) {
	inputDirectory, outputDirectory := prepareInput(t, map[string]int{
		"good.pdf": 1,
	})
	if err := os.WriteFile(
		filepath.Join(inputDirectory, "broken.pdf"),
		[]byte("this is not a PDF"),
		0o644,
	); err != nil {
		t.Fatalf("write broken fixture: %v", err)
	}

	if err := RunBatch(inputDirectory, outputDirectory, strings.NewReader("\n"), io.Discard); err == nil {
		t.Fatal("merging a broken PDF unexpectedly succeeded")
	}
	if matches := globMatches(t, outputDirectory, "合并-*.pdf"); len(matches) != 0 {
		t.Fatalf("failed merge left output files: %v", matches)
	}
	if matches := globMatches(t, outputDirectory, ".pdfmerge-*"); len(matches) != 0 {
		t.Fatalf("failed merge left temporary files: %v", matches)
	}
}

// prepareInput creates PDF fixtures with the given page counts and returns
// the input and output directories used for a RunBatch call.
func prepareInput(t *testing.T, fixtures map[string]int) (string, string) {
	t.Helper()
	requireExternalTools(t, "magick")

	inputDirectory := t.TempDir()
	outputDirectory := t.TempDir()
	colors := []string{"#cc2222", "#2244cc", "#22cc44"}

	for name, pageCount := range fixtures {
		args := []string{}
		for page := 0; page < pageCount; page++ {
			args = append(args, "-size", "40x40", "xc:"+colors[page%len(colors)])
		}
		runExternal(t, "magick", append(args, filepath.Join(inputDirectory, name))...)
	}
	return inputDirectory, outputDirectory
}

func requireExternalTools(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("external test dependency %q is unavailable", name)
		}
	}
}

func runExternal(t *testing.T, name string, args ...string) string {
	t.Helper()
	command := exec.Command(name, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, output)
	}
	return string(output)
}

func globOne(t *testing.T, directory, pattern string) string {
	t.Helper()
	matches := globMatches(t, directory, pattern)
	if len(matches) != 1 {
		t.Fatalf("pattern %s matched %d files, want 1: %v", pattern, len(matches), matches)
	}
	return matches[0]
}

func globMatches(t *testing.T, directory, pattern string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(directory, pattern))
	if err != nil {
		t.Fatalf("glob %s: %v", pattern, err)
	}
	return matches
}

func requireFileExists(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("%s is not a regular file", path)
	}
}

func readDirEntries(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read %s: %v", directory, err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
