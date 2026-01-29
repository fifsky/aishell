# AI Shell 助手 (aishell)

一个基于 AI 的智能终端命令生成工具，能够将自然语言转换为 Shell 命令并辅助执行。

## 功能特性

- 🗣️ **自然语言交互**：直接描述你的需求，自动生成对应的 Shell 命令。
- 🧠 **智能上下文**：自动记录对话历史，支持多轮对话，能够理解之前的操作结果。
- 🔄 **执行反馈**：命令执行的输出（标准输出和错误）会自动反馈给 AI，以便进行后续的错误修正或进一步操作。
- 🛡️ **安全执行**：生成的命令在执行前需要用户确认，防止误操作。
- 🧹 **一键重置**：支持 `clear` 命令快速清理对话上下文。
- ⚙️ **可配置**：支持开启/关闭 AI 思考模式 (Thinking Mode)。

## 依赖要求

- `bash`
- `curl`
- `jq` (用于处理 JSON 数据)

macOS 安装 jq:

```bash
brew install jq
```

## 安装与配置

### 1. 下载与授权

使用 wget 下载脚本到本地（例如 `~/aishell.sh`）：

```bash
wget -O ~/aishell.sh https://raw.githubusercontent.com/fifsky/aishell/refs/heads/main/aishell.sh
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

# 选填：配置 API 接口地址 (默认为 Moonshot AI 地址)
# export AISHELL_BASE_URL="https://api.moonshot.cn/v1/chat/completions"

# 选填：配置模型 (默认为 kimi-k2.5)
# export AISHELL_MODEL="kimi-k2.5"

# 选填：配置上下文保留条数 (默认为 100)
# export AISHELL_MAX_CONTEXT=100

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

### 多轮对话示例

```bash
# 第一步
ai 查找最近修改的日志文件

# 第二步 (AI 会记得上一步的文件)
ai 把它们打包成 tar.gz

# 第三步 (执行出错时 AI 会尝试修复)
ai 解压刚才的包
```

> 输入的内容包含特殊字符（如空格），请用引号括起来。或者放入文本文件中使用`cat 文件名 | ai`

### 清理上下文

当你想开始一个新的话题时，可以使用 `clear` 命令：

```bash
ai clear
```

## 高级配置

你可以直接编辑 `aishell.sh` 文件头部变量进行配置：

- **ENABLE_THINKING**: 设置为 `"true"` 可开启 AI 的思考过程展示（取决于模型支持）。
- **MODEL**: 切换使用的模型版本（默认 `kimi-k2.5`）。
