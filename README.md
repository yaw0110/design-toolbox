# 设计百宝箱 (Design Toolbox)

一个面向设计行业的离线小工具集，单个二进制、纯 Go 实现、跨平台。
由 `pdf-compressor` 与 `svg2gif` 两个独立项目合并而来。

## 工具列表

| 工具 | 功能 | 子命令 |
| --- | --- | --- |
| PDF 压缩 | 压缩单页长图型 PDF（作品集、长图导出稿） | `toolbox pdf` |
| SVG/SVGA 转 GIF | 批量转换 SVG（含 SMIL 动画）/SVGA 为 GIF 与 APNG | `toolbox svg2gif` |

后续按需扩展（图片批量压缩、多尺寸导出、PDF 合并拆分等），每个工具注册进主菜单即可。

## 使用方式

### 双击 / 无参数运行：交互菜单

```text
====================================
  设计百宝箱 Design Toolbox v0.1.0
====================================

请选择工具：
  1. PDF 压缩        （input/pdf/ 中发现 2 个文件）
  2. SVG/SVGA 转 GIF （input/svg/ 中发现 1 个文件）
  0. 退出
```

菜单会实时显示各输入目录中的待处理文件数量。

### 目录约定（相对于程序所在目录）

```text
input/pdf/      待压缩 PDF
input/svg/      待转换 SVG/SVGA
output/pdf/     压缩结果
output/svg/     转换结果（gif/ 与 apng/ 子目录）
```

目录不存在时程序会自动创建。输入文件永远不会被修改或删除。

### 命令行子命令

```bash
toolbox                      # 交互主菜单
toolbox pdf                  # 批处理 input/pdf/ → output/pdf/（交互选档位）
toolbox pdf -input a.pdf -output b.pdf -quality 40   # 单文件模式
toolbox svg2gif ./source ./target -w 800 --height 800 -f 20
toolbox version
```

`toolbox pdf`（无参数）是可脚本化的批处理入口：有文件处理失败时以非零码退出；
交互菜单则始终回到主界面，方便连续处理。

## 仓库结构

```text
design-toolbox/
├── cmd/toolbox/main.go          # 唯一入口：菜单 + 子命令分发
├── internal/
│   ├── app/                     # 共享：程序目录定位、交互暂停
│   ├── pdfcompress/             # PDF 压缩（含回归测试）
│   │   ├── compress.go          #   核心压缩与 PDF 写入
│   │   ├── batch.go             #   批处理与档位交互
│   │   └── portable_pdf.go      #   纯 Go PDF 解析/提取（pdfcpu）
│   └── svg2gif/
│       ├── convert.go           #   单文件转换（ffmpeg 或内置 GIF 编码）
│       ├── batch.go             #   批处理与 CLI 子命令
│       ├── converter/           #   SVG / SVGA 解析渲染
│       ├── apng/                #   APNG 编码
│       └── gifenc/              #   纯 Go GIF 编码（无 ffmpeg 兜底）
├── docs/用户使用说明.md          # 面向最终用户的说明，构建时复制进 releases
├── build.sh                     # 多平台构建脚本
├── vendor/                      # 离线构建所需第三方源码
├── input/  output/              # 开发时的工作目录（不入库）
└── releases/beta/               # 构建产物（不入库）
```

## 构建与测试

开发环境要求：Go 1.25+，macOS 需自带 `/usr/bin/lipo`。

```bash
# 回归测试（离线，使用 vendor）
GOPROXY=off go test -mod=vendor -count=1 ./...

# 静态检查
go vet -mod=vendor ./...

# 构建发布产物到 releases/beta/
chmod +x ./build.sh && ./build.sh
```

构建产物：

```text
releases/beta/toolbox               # macOS Universal（arm64 + x86_64）
releases/beta/toolbox.exe           # Windows amd64
releases/beta/toolbox-linux-amd64   # Linux amd64
releases/beta/readme.md             # 用户说明
```

## 运行时独立性

二进制不依赖网络、Python、Ghostscript、ImageMagick、Poppler 或任何在线服务：

- PDF 解析由 `pdfcpu` 编译进二进制完成；
- SVG/SVGA 渲染由 `oksvg` 与自研 SVGA 解析完成；
- GIF 输出优先使用程序旁边的 `ffmpeg`（质量更优），没有 ffmpeg 时自动改用内置
  纯 Go 编码器（Plan9 调色板 + Floyd-Steinberg 抖动），单文件分发不受影响。

第三方模块与许可证见 `THIRD_PARTY_NOTICES.md`。

## 从旧项目迁移

| 旧仓库 | 迁移位置 | 说明 |
| --- | --- | --- |
| `pdf-compressor` | `internal/pdfcompress` | 核心逻辑逐行迁移，测试同步迁移并改为驱动子命令 |
| `svg2gif` | `internal/svg2gif` | converter/apng 原样迁移；CLI 语法保持兼容 |

两个旧仓库的行为差异：

- 输入输出目录从 `input/`、`source/` 统一为 `input/pdf|svg` 与 `output/pdf|svg`；
- 无 ffmpeg 时不再报错退出，改用内置 GIF 编码器；
- 交互菜单替代了"双击直接跑批"的默认行为，批处理仍可通过 `toolbox pdf` 使用。

## 隐私

`.gitignore` 排除 `input/`、`output/`、`releases/` 中的用户文件与构建产物。
提交前检查 `git status --short`，不要提交客户文件、个人信息或临时输出。
