# AI Shell 助手 (aishell)

一个基于 AI 的智能终端助手，能够自动判断用户问题是直接回答、规划步骤，还是需要执行 Shell 命令辅助完成。

## 功能特性

- 🗣️ **自然语言交互**：直接描述你的需求，AI 会自主判断是直接回答还是需要终端操作。
- 🧭 **自主规划**：复杂任务会按步骤推进，只有需要执行 Shell 命令时才请求确认。
- 🌊 **流式渲染**：使用流式接口接收 AI 回复，并通过 Streamdown 实时渲染 Markdown。
- 📝 **Markdown 渲染**：使用 `sd` 渲染 AI 的 Markdown 回复。
- 🔎 **联网搜索**：需要搜索当前信息时可通过 Tavily CLI (`tvly search`) 自动检索。
- 🧠 **智能上下文**：自动记录对话历史，支持多轮对话，能够理解之前的操作结果。
- 🔄 **执行反馈**：命令执行的输出（标准输出和错误）会自动反馈给 AI，以便进行后续的错误修正或进一步操作。
- 🛡️ **安全执行**：只有 AI 明确请求执行命令时才会提示确认，防止误操作。
- 📁 **Session 管理**：支持多会话管理，可在不同项目/任务间切换上下文。
- 🧹 **一键重置**：支持 `clear` 命令快速清理对话上下文。
- ⚙️ **可配置**：支持配置模型、接口地址、上下文长度和自动处理轮数。

## 依赖要求

- `bash`
- `curl`
- `jq` (用于处理 JSON 数据)
- `sd` (Streamdown，用于流式渲染 Markdown 输出)
- `tvly` (可选，用于联网搜索)
- `fzf` (可选，用于 Session 列表选择和删除功能)

macOS 安装依赖:

```bash
brew install jq streamdown fzf
```

`tvly` 请按 Tavily CLI 官方方式安装并登录。

## 安装与配置

### 1. 下载与授权

使用 wget 下载脚本和提示词到本地（例如放在 `~` 目录）：

```bash
wget -O ~/aishell.sh https://raw.githubusercontent.com/fifsky/aishell/refs/heads/main/aishell.sh
wget -O ~/system_prompt.md https://raw.githubusercontent.com/fifsky/aishell/refs/heads/main/system_prompt.md
```

赋予执行权限：

```bash
chmod +x ~/aishell.sh
```

### 2. 配置环境变量

在使用之前，你需要配置 API 密钥和接口地址（可选）。

**以 zsh 为例 (macOS/Linux 默认 Shell):**

编辑你的 `~/.zshrc` 文件，添加以下内容：

```bash
# 必填：配置 API 密钥
export AISHELL_API_KEY="sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"

# 选填：配置 API 接口地址
# export AISHELL_BASE_URL="https://aiproxy.fifsky.com/v1/chat/completions"

# 选填：配置模型
# export AISHELL_MODEL="deepseek-v4-flash"

# 选填：配置上下文保留条数 (默认为 100)
# export AISHELL_MAX_CONTEXT=100

# 选填：配置单次请求内最多自动处理轮数 (默认为 5)
# export AISHELL_MAX_STEPS=5

# 选填：配置系统提示词文件
# export AISHELL_SYSTEM_PROMPT_FILE="${HOME}/system_prompt.md"

# 选填：配置别名
alias ai="${HOME}/aishell.sh"
```

保存后，重载配置文件使生效：

```bash
source ~/.zshrc
```

### 3. 开始使用

配置完成后，你就可以直接使用 `ai` 命令了。

## 使用指南

### 基础用法

```bash
ai "查看当前目录下的所有 PDF 文件"
```

如果 AI 需要查看本机目录，会先给出说明和待执行命令，并等待你确认；如果只是知识性问题，则会直接回答。

```bash
ai "tar 和 gzip 有什么区别"
```

需要执行命令时，AI 必须输出 `shell-exec` 代码块；普通 `bash`、`sh`、`shell` 代码块只会作为 Markdown 渲染，不会执行。

`aishell.sh` 顶部的 `AUTO_APPROVE_COMMAND_PREFIXES` 数组定义了自动执行白名单，使用前缀匹配。默认 `tvly search ` 命中白名单，因此搜索命令会自动执行，无需确认。

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

查看所有可用的 session 并快速切换（需要安装 fzf）：

```bash
ai list
```

这将打开一个交互式列表，当前 session 会标有 `*` 号，选择后即可快速切换。

#### 删除 Session

删除不需要的 session（支持多选，需要安装 fzf）：

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

你可以直接编辑 `aishell.sh` 文件头部变量进行配置，也可以修改脚本同目录下的 `system_prompt.md` 来调整 AI 行为。

- **API_URL**: 切换 OpenAI 兼容的 Chat Completions 接口地址。
- **MODEL**: 切换使用的模型版本（默认 `deepseek-v4-flash`）。
- **MAX_CONTEXT_SIZE**: 控制保留的上下文消息数量（默认 `100`）。
- **MAX_STEPS**: 控制单次请求内最多自动处理轮数（默认 `5`）。
- **SYSTEM_PROMPT_FILE**: 控制系统提示词文件路径（默认脚本同目录的 `system_prompt.md`）。
