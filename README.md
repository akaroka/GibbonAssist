# GibbonAss 🦍🎤 — 录音转写桌面工具

> 🎯 全局热键录音 → 🤖 whisper.cpp 转写 → 🧠 LLM 润色 → 📋 自动粘贴
>
> ⚡ 从录音到文字 **< 10 秒**

---

## 🔧 构建

### 📦 前置依赖

| 组件 | 版本 | 说明 |
|---|---|---|
| Go 🐹 | 1.26.2+ | `go version` |
| MinGW-W64 🔨 | x86_64-ucrt-posix-seh gcc 15.2.0+ | CGO 需要 |
| windres 📦 | (MinGW 自带) | 图标嵌入用 |

### 🌐 环境变量

```bash
SET CGO_ENABLED=1
SET CC=path\to\mingw64\bin\gcc.exe
SET GOPROXY=https://goproxy.cn,direct
```

### 🏗️ 构建步骤

#### 1️⃣ 图标嵌入（仅 release 需要）

```bash
# 从 .ico 生成 .rc 资源脚本
echo "IDI_ICON1 ICON \"Music_folder_35254.ico\"" > app.rc

# 编译为 .syso（Go 自动链接进 PE）
windres -o app.syso app.rc

# 清理 .rc（临时脚本）
rm app.rc
```

中间产物（.gitignore 可忽略）：
- `app.syso` 🏗️ — 编译后的资源对象，`go build` 自动链接

#### 2️⃣ 编译

```bash
# Debug 构建（带控制台日志）
go build -o GibbonAss_debug.exe .

# Release 构建（无控制台 + strip 调试符号 + 图标）
go build -ldflags="-H windowsgui -s -w" -o GibbonAss.exe .
```

| 参数 | 作用 |
|---|---|
| `-H windowsgui` | 🚫 无控制台窗口（仅 release） |
| `-s` | 🗑️ 去掉符号表 |
| `-w` | 🗑️ 去掉 DWARF 调试信息 |
| `.syso` | 🔗 自动链接, 嵌入 .ico 到 exe |

#### 3️⃣ 产物对比

| 目标 | 大小 | 控制台 | 图标 |
|---|---|---|---|
| `GibbonAss_debug.exe` 🐛 | ~42 MB | ✅ 有 | ❌ 无 |
| `GibbonAss.exe` 🚀 | ~25 MB | ❌ 无 | ✅ 有 |

### 📦 UPX 压缩（可选）

```bash
upx --best GibbonAss.exe   # 可压到 ~10 MB
```

---

## 📂 运行依赖

所有文件在 exe 同目录：

```
GibbonAss/
├── 🚀 GibbonAss.exe          # 主程序
├── ⚙️ config.json            # LLM 配置（见下方）
├── 📁 whisper/
│   ├── 🗣️ whisper-cli.exe    # 转写引擎
│   ├── 🔧 ggml.dll           # 运行时 DLL
│   ├── 🔧 ggml-base.dll
│   ├── 🔧 ggml-cpu.dll
│   └── 🔧 whisper.dll
└── 📁 models/
    └── 🧠 ggml.bin           # GGML 模型文件（可替换）
```

---

## ⚙️ 配置

编辑 `config.json`：

```json
{
  "llm_url": "https://api.deepseek.com/v1",
  "llm_key": "sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
  "llm_model": "deepseek-v4-flash",
  "llm_prompt": "你是中文语音转文字后处理助手...",
  "save_audio": false
}
```

| 字段 | 说明 |
|---|---|
| `llm_url` | 🌐 OpenAI 兼容 API base URL |
| `llm_key` | 🔑 API key |
| `llm_model` | 🧠 模型名 |
| `llm_prompt` | 📝 润色 system prompt |
| `save_audio` | 💾 true=保留录音WAV, false=转写后删除 |

---

## 🎮 使用

| 热键 | 作用 |
|---|---|
| **Ctrl + Left Alt** 🟢 | 开始录音 / 停止并处理 |
| **ESC** 🔴 | 录音中取消，丢弃音频 |

### 完整流程 🔄

```
🎤 按热键 → 录音中... → 再按热键
                         ↓
                    📝 whisper 转写
                         ↓
                    🧠 LLM 润色
                         ↓
                    📋 写入剪贴板 → Ctrl+V 粘贴 → 恢复原剪贴板
```

---

## 📚 开发文档

| 文档 | 内容 |
|---|---|
| `DEVELOPMENT_SUMMARY.md` 📖 | 完整开发历程、功能验收、踩坑记录 |
| `PRD_GA.md` 📋 | 产品需求文档 |
| `Architecture_Brief_GA.md` 🏗️ | 架构设计 |
