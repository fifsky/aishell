# AI Shell 助手 (aishell)

一个基于 AI 的智能终端助手，能够自动判断用户问题是直接回答、规划步骤，还是需要执行 Shell 命令辅助完成。

## 功能特性

- 🗣️ **自然语言交互**：直接描述你的需求，AI 会自主判断是直接回答还是需要终端操作。
- 🧭 **自主规划**：复杂任务会按步骤推进，只有需要执行 Shell 命令时才请求确认。
- 🌊 **流式渲染**：使用 OpenAI SDK 流式接收 AI 回复，并在终端内实时渲染 Markdown。
- 📝 **Markdown 渲染**：Go 版使用 Glamour（Glow 的 Markdown 渲染器）原生渲染输出。
- 🔎 **联网搜索**：需要搜索当前信息时可通过 Tavily CLI (`tvly search`) 自动检索。
- 🧠 **智能上下文**：自动记录对话历史，支持多轮对话，能够理解之前的操作结果。
- 🔄 **执行反馈**：命令执行的输出（标准输出和错误）会自动反馈给 AI，以便进行后续的错误修正或进一步操作。
- 🛡️ **安全执行**：只有 AI 明确请求执行命令时才会提示确认，防止误操作。
- 📁 **Session 管理**：支持多会话管理，可在不同项目/任务间切换上下文。
- 🧹 **一键重置**：支持 `clear` 命令快速清理对话上下文。
- ⚙️ **可配置**：支持配置模型、接口地址、上下文长度和自动处理轮数。

## 依赖要求

- `go` (构建 Go 版 `aichat` 时需要)
- `tvly` (可选，用于联网搜索)

macOS 安装依赖:

```bash
brew install go
```

`tvly` 请按 Tavily CLI 官方方式安装并登录。

## 安装与配置

### Go 版 aichat

本仓库提供 Go 命令行工具 `aichat`，使用 Bubble Tea 实现交互、OpenAI Go SDK 实现流式请求、Glamour 实现 Markdown 渲染，并自动加载 `~/.agents/skills` 下的技能索引。

本地构建：

```bash
go build -o aichat ./cmd/aichat
```

或安装到 `$GOBIN`：

```bash
go install ./cmd/aichat
```

Go 版默认读取：

- `AICHAT_API_KEY` / `AISHELL_API_KEY`
- `AICHAT_BASE_URL` / `AISHELL_BASE_URL`
- `AICHAT_MODEL` / `AISHELL_MODEL`
- `AICHAT_MAX_CONTEXT` / `AISHELL_MAX_CONTEXT`
- `AICHAT_MAX_STEPS` / `AISHELL_MAX_STEPS`
- `AICHAT_SYSTEM_PROMPT_FILE` / `AISHELL_SYSTEM_PROMPT_FILE`
- `AICHAT_SKILLS_DIR`，默认 `~/.agents/skills`

Go 版会复用 `~/.aishell/config.json` 和 `~/.aishell/sessions/*.json`。

### 1. 配置环境变量

在使用之前，你需要配置 API 密钥和接口地址（可选）。

**以 zsh 为例 (macOS/Linux 默认 Shell):**

编辑你的 `~/.zshrc` 文件，添加以下内容：

```bash
# 必填：配置 API 密钥
export AISHELL_API_KEY="sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"

# 选填：配置 API 接口地址
# export AISHELL_BASE_URL="https://aiproxy.fifsky.com/v1/chat/completions"

# 选填：配置模型
# export AISHELL_MODEL="deepseek-v4-pro"

# 选填：配置上下文保留条数 (默认为 100)
# export AISHELL_MAX_CONTEXT=100

# 选填：配置单次请求内最多自动处理轮数 (默认为 5)
# export AISHELL_MAX_STEPS=5

# 选填：配置系统提示词文件
# export AISHELL_SYSTEM_PROMPT_FILE="${HOME}/system_prompt.md"

# 选填：配置别名
alias ai="aichat"
```

保存后，重载配置文件使生效：

```bash
source ~/.zshrc
```

### 2. 开始使用

配置完成后，你就可以直接使用 `ai` 命令了。

## 使用指南

### 基础用法

```bash
aichat "查看当前目录下的所有 PDF 文件"
ai "查看当前目录下的所有 PDF 文件"
```

如果 AI 需要查看本机目录，会先给出说明和待执行命令，并等待你确认；如果只是知识性问题，则会直接回答。

```bash
aichat "tar 和 gzip 有什么区别"
ai "tar 和 gzip 有什么区别"
```

需要执行命令时，AI 必须输出 `shell-exec` 代码块；普通 `bash`、`sh`、`shell` 代码块只会作为 Markdown 渲染，不会执行。

Go 版 `aichat` 内置 `autoApproveCommandPrefixes` 白名单数组，使用前缀匹配。默认 `tvly search ` 命中白名单，因此搜索命令会自动执行，无需确认。其他 `shell-exec` 命令会先进入 Bubble Tea 确认界面。

### 技能加载

`aichat` 启动时会扫描 `~/.agents/skills/*/SKILL.md`，使用 YAML frontmatter 读取技能名称、描述和 `allowed-tools`，注入系统提示词。AI 可根据技能描述选择合适技能；需要查看完整技能内容或执行技能中的命令时，会通过 `shell-exec` 使用 bash tool。

### 多轮对话示例

```bash
# 第一步
ai 查找最近修改的日志文件

# 第二步 (AI 会记得上一步的文件)
ai 把它们打包成 tar.gz

# 第三步 (执行出错时 AI 会尝试修复)
ai 解压刚才的包
```

> 输入的内容包含特殊字符（如空格），请用引号括起来。文本文件内容可以使用 `ai "$(cat 文件名)"` 传入。

### Session 管理

aishell 支持多会话管理，让你可以在不同的项目或任务之间保持独立的对话上下文。

#### 切换/创建 Session

切换到指定名称的 session（如果不存在会自动创建）：

```bash
ai session myproject
```

#### 列出所有 Session

查看所有可用的 session 并快速切换：

```bash
ai list
```

这将打开一个交互式列表，当前 session 会标有 `*` 号，选择后即可快速切换。

#### 删除 Session

删除不需要的 session（支持多选）：

```bash
ai delete
```

> **注意**：删除当前正在使用的 session 后，会自动切换到 `default` session。

#### 不同 Session 的使用示例

```bash
# 在项目 A 的 session 中工作
ai session project-a
ai 查找所有的JavaScript文件

# 切换到项目 B 的 session，完全独立的上下文
ai session project-b
ai 查看当前的Git状态

# 列出所有 session
ai list

# 清理不再需要的 session
ai delete
```

### 清理上下文

当你想开始一个新的话题时，可以使用 `clear` 命令：

```bash
ai clear
```

## 高级配置

你可以通过环境变量配置运行参数，也可以修改仓库或可执行文件同目录下的 `system_prompt.md` 来调整 AI 行为。

- **AICHAT_BASE_URL / AISHELL_BASE_URL**: 切换 OpenAI 兼容的 Chat Completions 接口地址。
- **AICHAT_MODEL / AISHELL_MODEL**: 切换使用的模型版本（默认 `deepseek-v4-pro`）。
- **AICHAT_MAX_CONTEXT / AISHELL_MAX_CONTEXT**: 控制保留的上下文消息数量（默认 `100`）。
- **AICHAT_MAX_STEPS / AISHELL_MAX_STEPS**: 控制单次请求内最多自动处理轮数（默认 `5`）。
- **AICHAT_SYSTEM_PROMPT_FILE / AISHELL_SYSTEM_PROMPT_FILE**: 控制系统提示词文件路径。
- **AICHAT_SKILLS_DIR**: 控制技能目录路径（默认 `~/.agents/skills`）。
