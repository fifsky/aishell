#!/bin/bash

# 配置信息
API_URL="${AISHELL_BASE_URL:-https://aiproxy.fifsky.com/v1/chat/completions}"
API_KEY="${AISHELL_API_KEY}"
MODEL="${AISHELL_MODEL:-deepseek-v4-pro}"
MAX_CONTEXT_SIZE="${AISHELL_MAX_CONTEXT:-100}"
MAX_STEPS="${AISHELL_MAX_STEPS:-5}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SYSTEM_PROMPT_FILE="${AISHELL_SYSTEM_PROMPT_FILE:-$SCRIPT_DIR/system_prompt.md}"
AUTO_APPROVE_COMMAND_PREFIXES=(
    "tvly search "
)
BASE_DIR="$HOME/.aishell"
SESSION_DIR="$BASE_DIR/sessions"
CONFIG_FILE="$BASE_DIR/config.json"
SYSTEM_PROMPT=""
STREAM_FILTER_IN_EXEC=0
STREAM_FILTER_BUFFER=""

# 检查依赖
check_dependencies() {
    if ! command -v curl &> /dev/null; then
        echo "错误: 未找到 curl 命令。请先安装 curl。"
        exit 1
    fi

    if ! command -v jq &> /dev/null; then
        echo "错误: 未找到 jq 命令。请先安装 jq。"
        exit 1
    fi

    if ! command -v sd &> /dev/null; then
        echo "错误: 未找到 Streamdown sd 命令。请先安装 Streamdown。"
        exit 1
    fi

    if [ -z "$API_KEY" ]; then
        echo "错误: 未找到 API 密钥。请设置 AISHELL_API_KEY 环境变量。"
        echo "例如: export AISHELL_API_KEY='your_api_key'"
        exit 1
    fi
}

load_system_prompt() {
    if [ ! -f "$SYSTEM_PROMPT_FILE" ]; then
        echo "错误: 未找到系统提示词文件: $SYSTEM_PROMPT_FILE"
        echo "可设置 AISHELL_SYSTEM_PROMPT_FILE 指向自定义提示词文件。"
        exit 1
    fi

    SYSTEM_PROMPT=$(cat "$SYSTEM_PROMPT_FILE")
    if [ -z "$SYSTEM_PROMPT" ]; then
        echo "错误: 系统提示词文件为空: $SYSTEM_PROMPT_FILE"
        exit 1
    fi
}

# 初始化配置
init_config() {
    # 确保基础目录和会话目录存在
    if [ ! -d "$SESSION_DIR" ]; then
        mkdir -p "$SESSION_DIR"
    fi

    # 初始化配置文件
    if [ ! -f "$CONFIG_FILE" ]; then
        jq -n '{"current_session": "default"}' > "$CONFIG_FILE"
    fi
}

# 获取当前session名称
get_current_session() {
    local current_session
    current_session=$(jq -r '.current_session // "default"' "$CONFIG_FILE")
    echo "$current_session"
}

# 设置当前session
set_session() {
    local session_name="$1"
    local temp_config
    temp_config=$(jq --arg session "$session_name" '.current_session = $session' "$CONFIG_FILE")
    echo "$temp_config" > "$CONFIG_FILE"
}

# 列出所有session
list_sessions() {
    if ! command -v fzf &> /dev/null; then
        echo "错误: 未找到 fzf 命令。请先安装 fzf。"
        echo "安装命令: brew install fzf"
        exit 1
    fi

    local current_session
    current_session=$(get_current_session)

    # 获取所有session文件
    local sessions=()
    while IFS= read -r -d '' file; do
        local session_name
        session_name=$(basename "$file" .json)
        sessions+=("$session_name")
    done < <(find "$SESSION_DIR" -maxdepth 1 -type f -name "*.json" -print0 | sort -z)

    if [ ${#sessions[@]} -eq 0 ]; then
        echo "当前没有可用的session。"
        exit 0
    fi

    # 使用fzf选择session，当前session用*标记
    local selected_session
    selected_session=$(printf "%s\n" "${sessions[@]}" | \
        awk -v current="$current_session" '{if ($1 == current) print "* " $0; else print "  " $0}' | \
        fzf --height 40% --reverse --prompt="选择session (当前: $current_session): " --ansi | \
        sed 's/^[* ] //')

    if [ -n "$selected_session" ]; then
        set_session "$selected_session"
        printf "已切换到session: \033[32m%s\033[0m\n" "$selected_session"
    fi
}

# 删除session
delete_session() {
    if ! command -v fzf &> /dev/null; then
        echo "错误: 未找到 fzf 命令。请先安装 fzf。"
        echo "安装命令: brew install fzf"
        exit 1
    fi

    local current_session
    current_session=$(get_current_session)

    # 获取所有session文件
    local sessions=()
    while IFS= read -r -d '' file; do
        local session_name
        session_name=$(basename "$file" .json)
        sessions+=("$session_name")
    done < <(find "$SESSION_DIR" -maxdepth 1 -type f -name "*.json" -print0 | sort -z)

    if [ ${#sessions[@]} -eq 0 ]; then
        echo "当前没有可用的session。"
        exit 0
    fi

    # 使用fzf选择session，当前session用*标记
    local selected_session
    selected_session=$(printf "%s\n" "${sessions[@]}" | \
        awk -v current="$current_session" '{if ($1 == current) print "* " $0; else print "  " $0}' | \
        fzf --height 40% --reverse --prompt="选择要删除的session (当前: $current_session): " --ansi --multi | \
        sed 's/^[* ] //')

    if [ -z "$selected_session" ]; then
        echo "已取消删除。"
        exit 0
    fi

    # 支持多选删除
    local deleted_count=0
    while IFS= read -r session; do
        local context_file="$SESSION_DIR/${session}.json"
        if [ -f "$context_file" ]; then
            rm -f "$context_file"
            printf "已删除session: \033[31m%s\033[0m\n" "$session"
            ((deleted_count++))

            # 如果删除的是当前session，重置为default
            if [ "$session" = "$current_session" ]; then
                set_session "default"
                init_context
                printf "当前session已重置为: \033[32mdefault\033[0m\n"
            fi
        fi
    done <<< "$selected_session"

    if [ $deleted_count -eq 0 ]; then
        echo "没有删除任何session。"
    fi
}

# 初始化上下文
init_context() {
    local session_name
    session_name=$(get_current_session)
    local context_file="$SESSION_DIR/${session_name}.json"

    # 初始化上下文文件
    if [ ! -f "$context_file" ]; then
        # 如果文件不存在，创建一个包含系统提示词的初始 JSON 数组
        jq -n --arg content "$SYSTEM_PROMPT" '[{"role": "system", "content": $content}]' > "$context_file"
    else
        local updated_context
        updated_context=$(jq --arg content "$SYSTEM_PROMPT" '
            if length == 0 then
                [{"role": "system", "content": $content}]
            elif .[0].role == "system" then
                .[0].content = $content
            else
                [{"role": "system", "content": $content}] + .
            end
        ' "$context_file")
        echo "$updated_context" > "$context_file"
    fi
}

# 获取并验证用户输入
get_user_input() {
    local input="$*"
    if [ -z "$input" ]; then
        echo "用法: $0 <你的需求>"
        exit 1
    fi
    echo "$input"
}

# 更新上下文并保存到文件
# 参数 1: 角色 (user/assistant)
# 参数 2: 内容
update_context() {
    local role="$1"
    local content="$2"
    local session_name
    session_name=$(get_current_session)
    local context_file="$SESSION_DIR/${session_name}.json"
    local current_context

    current_context=$(cat "$context_file")

    # 使用 jq 将新消息追加到数组末尾，并限制上下文长度
    # 逻辑：总是保留第一条（系统提示词），如果超过限制，则保留最后 MAX_CONTEXT_SIZE - 1 条后续消息
    local new_context
    new_context=$(echo "$current_context" | jq --arg role "$role" --arg content "$content" --argjson max_len "$MAX_CONTEXT_SIZE" '
        . + [{"role": $role, "content": $content}] |
        if length > $max_len then
            [.[0]] + (.[1:] | .[-(($max_len - 1)):])
        else
            .
        end
    ')

    echo "$new_context" > "$context_file"
}

# 信号捕获：退出时恢复光标
cleanup() {
    printf "\033[?25h" >&2
    exit
}
trap cleanup SIGINT SIGTERM

# 去除 AI 回复中的执行协议块，仅保留可渲染给用户看的 Markdown。
strip_exec_blocks() {
    local input_file="$1"
    awk '
        /^[[:space:]]*```shell-exec[[:space:]]*$/ { in_exec = 1; next }
        in_exec && /^[[:space:]]*```[[:space:]]*$/ { in_exec = 0; next }
        !in_exec { print }
    ' "$input_file"
}

# 提取第一个 shell-exec 代码块中的命令。
extract_exec_command() {
    local input_file="$1"
    awk '
        /^[[:space:]]*```shell-exec[[:space:]]*$/ && !done { in_exec = 1; done = 1; next }
        in_exec && /^[[:space:]]*```[[:space:]]*$/ { exit }
        in_exec { print }
    ' "$input_file"
}

stream_visible_line() {
    local line="$1"

    if [ "$STREAM_FILTER_IN_EXEC" -eq 0 ] && [[ "$line" =~ ^[[:space:]]*\`\`\`shell-exec[[:space:]]*$ ]]; then
        STREAM_FILTER_IN_EXEC=1
        return
    fi

    if [ "$STREAM_FILTER_IN_EXEC" -eq 1 ]; then
        if [[ "$line" =~ ^[[:space:]]*\`\`\`[[:space:]]*$ ]]; then
            STREAM_FILTER_IN_EXEC=0
        fi
        return
    fi

    printf "%s\n" "$line" >&3
}

stream_visible_delta() {
    local delta_file="$1"
    local char

    while IFS= read -r -n 1 char || [ -n "$char" ]; do
        if [ -z "$char" ]; then
            stream_visible_line "$STREAM_FILTER_BUFFER"
            STREAM_FILTER_BUFFER=""
        else
            STREAM_FILTER_BUFFER="${STREAM_FILTER_BUFFER}${char}"
        fi
    done < "$delta_file"
}

flush_visible_stream() {
    if [ -n "$STREAM_FILTER_BUFFER" ]; then
        stream_visible_line "$STREAM_FILTER_BUFFER"
        STREAM_FILTER_BUFFER=""
    fi
}

# 调用 API，将可见 Markdown 通过管道交给 Streamdown 渲染，并保存完整 AI 回复。
# 参数 1: 上下文 JSON 内容
# 参数 2: 保存完整 AI 回复的文件
call_api_stream() {
    local context="$1"
    local output_file="$2"

    # 判断是否为 qwen 模型，使用不同的参数格式关闭思考
    local extra_params="{}"
    if [[ "$MODEL" == qwen* ]]; then
        extra_params='{"enable_thinking": false}'
    else
        extra_params='{"thinking": {"type": "disabled"}}'
    fi

    local request_data
    request_data=$(jq -n \
        --arg model "$MODEL" \
        --argjson messages "$context" \
        --argjson extra "$extra_params" \
        '{model: $model, messages: $messages, stream: true} + $extra')

    : > "$output_file"
    STREAM_FILTER_IN_EXEC=0
    STREAM_FILTER_BUFFER=""

    local pipe
    pipe=$(mktemp -u "${TMPDIR:-/tmp}/aishell_stream.XXXXXX")
    if ! mkfifo "$pipe"; then
        echo "错误: 无法创建流式输出管道。" >&2
        return 1
    fi

    local render_pipe
    render_pipe=$(mktemp -u "${TMPDIR:-/tmp}/aishell_render.XXXXXX")
    if ! mkfifo "$render_pipe"; then
        rm -f "$pipe"
        echo "错误: 无法创建流式渲染管道。" >&2
        return 1
    fi

    if [ -t 1 ]; then
        sd < "$render_pipe" &
    else
        sd < "$render_pipe" 2>/dev/null &
    fi
    local sd_pid=$!
    exec 3>"$render_pipe"

    curl -sS -N "$API_URL" \
      -H "Authorization: Bearer $API_KEY" \
      -H "Content-Type: application/json" \
      -d "$request_data" > "$pipe" &

    local curl_pid=$!
    local stream_error=""
    local line
    while IFS= read -r line; do
        case "$line" in
            data:*)
                local data
                data="${line#data:}"
                data="${data# }"
                data="${data%$'\r'}"
                ;;
            *)
                continue
                ;;
        esac

        if [ "$data" = "[DONE]" ]; then
            continue
        fi

        local error_msg
        error_msg=$(printf "%s" "$data" | jq -r '.error.message // empty' 2>/dev/null)
        if [ -n "$error_msg" ]; then
            stream_error="$error_msg"
            continue
        fi

        local delta_file
        delta_file=$(mktemp)
        printf "%s" "$data" | jq -rj '.choices[0].delta.content // .choices[0].message.content // empty' 2>/dev/null > "$delta_file"
        if [ -s "$delta_file" ]; then
            cat "$delta_file" >> "$output_file"
            stream_visible_delta "$delta_file"
        fi
        rm -f "$delta_file"
    done < "$pipe"

    flush_visible_stream
    exec 3>&-

    wait "$curl_pid"
    local curl_exit_code=$?
    wait "$sd_pid"
    rm -f "$pipe"
    rm -f "$render_pipe"

    if [ $curl_exit_code -ne 0 ]; then
        echo "错误: API 请求失败。" >&2
        return 1
    fi

    if [ -n "$stream_error" ]; then
        echo "API 错误: $stream_error" >&2
        return 1
    fi

    if [ ! -s "$output_file" ]; then
        echo "错误: 未能获取 AI 回复，接口可能未返回 OpenAI 兼容的流式数据。" >&2
        return 1
    fi

    printf "\n"

    return 0
}

is_blank() {
    local value="$1"
    [ -z "$(printf "%s" "$value" | tr -d '[:space:]')" ]
}

trim_leading_space() {
    local value="$1"
    printf "%s" "$value" | sed 's/^[[:space:]]*//'
}

is_auto_approved_command() {
    local command_text="$1"
    local normalized_command
    local prefix

    normalized_command=$(trim_leading_space "$command_text")
    for prefix in "${AUTO_APPROVE_COMMAND_PREFIXES[@]}"; do
        if [[ "$normalized_command" == "$prefix"* ]]; then
            return 0
        fi
    done

    return 1
}

execute_shell_command() {
    local command_text="$1"
    local output_file
    output_file=$(mktemp)

    echo "正在执行..."
    eval "$command_text" > "$output_file" 2>&1
    local exit_code=$?

    cat "$output_file"

    local cmd_output
    cmd_output=$(cat "$output_file")
    rm -f "$output_file"

    local feedback_msg
    if [ $exit_code -eq 0 ]; then
        feedback_msg=$(printf "命令执行成功，退出码 0，输出如下：\n%s" "$cmd_output")
    else
        feedback_msg=$(printf "命令执行失败，退出码 %s，输出如下：\n%s" "$exit_code" "$cmd_output")
    fi

    update_context "user" "$feedback_msg"
}

# 主逻辑
main() {
    check_dependencies
    load_system_prompt
    init_config

    if ! [[ "$MAX_STEPS" =~ ^[0-9]+$ ]] || [ "$MAX_STEPS" -lt 1 ]; then
        echo "错误: AISHELL_MAX_STEPS 必须是大于 0 的整数。"
        exit 1
    fi

    # 处理子命令
    if [ $# -gt 0 ]; then
        case "$1" in
            session)
                if [ -z "$2" ]; then
                    echo "用法: $0 session <session_name>"
                    exit 1
                fi
                local session_name="$2"
                # 切换到指定session
                set_session "$session_name"
                # 确保session文件存在
                init_context
                printf "已切换到session: \033[32m%s\033[0m\n" "$session_name"
                exit 0
                ;;
            list)
                list_sessions
                exit 0
                ;;
            delete)
                delete_session
                exit 0
                ;;
        esac
    fi

    init_context

    local user_input
    user_input=$(get_user_input "$@")

    local session_name
    session_name=$(get_current_session)
    local context_file="$SESSION_DIR/${session_name}.json"

    # 检查是否为清理命令
    if [[ "$user_input" == "clear" ]]; then
        rm -f "$context_file"
        echo "上下文已清理。"
        exit 0
    fi
    
    # 添加用户消息到上下文
    update_context "user" "$user_input"

    local step
    for ((step = 1; step <= MAX_STEPS; step++)); do
        local current_context
        current_context=$(cat "$context_file")

        local ai_output_file
        ai_output_file=$(mktemp)

        if ! call_api_stream "$current_context" "$ai_output_file"; then
            rm -f "$ai_output_file"
            exit 1
        fi

        local ai_content
        ai_content=$(cat "$ai_output_file")
        update_context "assistant" "$ai_content"

        local exec_cmd
        exec_cmd=$(extract_exec_command "$ai_output_file")

        rm -f "$ai_output_file"

        if is_blank "$exec_cmd"; then
            break
        fi

        printf "\n\033[33m需要执行的命令:\033[0m\n"
        printf "\033[32m%s\033[0m\n" "$exec_cmd"

        if is_auto_approved_command "$exec_cmd"; then
            echo "命中白名单，自动执行。"
            execute_shell_command "$exec_cmd"
        else
            local execute_confirm
            if ! read -p "是否执行该命令? (Y/n): " execute_confirm; then
                echo "未收到确认，已取消执行。"
                update_context "user" "未收到用户确认，上一条命令没有执行。请在不执行该命令的前提下继续回答，或提供更安全的替代方案。"
                continue
            fi

            if [[ -z "$execute_confirm" || "$execute_confirm" =~ ^[Yy]$ ]]; then
                execute_shell_command "$exec_cmd"
            else
                echo "已取消执行。"
                update_context "user" "用户拒绝执行上一条命令。请在不执行该命令的前提下继续回答，或提供更安全的替代方案。"
            fi
        fi
    done

    if [ "$step" -gt "$MAX_STEPS" ]; then
        echo "已达到最大自动处理轮数 (${MAX_STEPS})，如需继续请再次输入你的需求。"
    fi
}

# 执行主函数
main "$@"
