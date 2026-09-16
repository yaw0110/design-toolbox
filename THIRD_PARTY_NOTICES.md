# Third-Party Notices

The runtime executable does not call OpenAI, Codex, Agent, MCP, Poppler,
ImageMagick, Python, web services, or separately installed helper programs.
When `ffmpeg` is placed next to the executable it is used for GIF palette
generation, but the binary also contains a built-in pure-Go GIF encoder and
works without it.

The executable statically links the following open-source Go modules:

## pdfcpu

- Module: `github.com/pdfcpu/pdfcpu`
- Version: `v0.13.0`
- License: Apache License 2.0
- Purpose: PDF parsing, validation, page inspection, and embedded-image extraction

## hhrutter/tiff

- Module: `github.com/hhrutter/tiff`
- Version: `v1.0.3`
- License: BSD 3-Clause (Go Authors)
- Purpose: Decoding TIFF images embedded in PDFs

## kettek/apng

- Module: `github.com/kettek/apng`
- Version: `v0.0.0-20250827064933-2bb5f5fcf253`
- License: BSD 3-Clause (Go Authors + Ketchetwahmeegwun T. Southall)
- Purpose: APNG (animated PNG) encoding for the SVG/SVGA converter

## oksvg

- Module: `github.com/srwiley/oksvg`
- Version: `v0.0.0-20221011165216-be6e8873101c`
- License: BSD 3-Clause
- Purpose: SVG parsing and rendering

## rasterx

- Module: `github.com/srwiley/rasterx`
- Version: `v0.0.0-20220730225603-2ab79fcdd4ef`
- License: BSD 3-Clause
- Purpose: Rasterization backend used by oksvg

## golang.org/x/image

- Module: `golang.org/x/image`
- Version: `v0.41.0`
- License: BSD 3-Clause (Go Authors)
- Purpose: Image drawing and scaling helpers

These modules use additional open-source Go modules listed in `go.mod` and
`go.sum`. Their source code and license files are retained under `vendor/`.
These modules are compiled into the executable and do not need to be installed
separately. Their original license terms remain applicable.

The project itself does not include or invoke any Agent runtime or proprietary
project service.
