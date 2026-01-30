#!/bin/bash

# 配置信息
API_URL="${AISHELL_BASE_URL:-https://api.moonshot.cn/v1/chat/completions}"
API_KEY="${AISHELL_API_KEY}"
MODEL="${AISHELL_MODEL:-kimi-k2.5}"
MAX_CONTEXT_SIZE="${AISHELL_MAX_CONTEXT:-100}"
ENABLE_THINKING="false"
CONTEXT_DIR="$HOME/.aishell"
CONTEXT_FILE="$CONTEXT_DIR/context.json"
SYSTEM_PROMPT="你是一个shell命令生成器，根据用户的需求生成对应的shell命令，不要输出其他内容"

# 检查依赖
check_dependencies() {
    if ! command -v jq &> /dev/null; then
        echo "错误: 未找到 jq 命令。请先安装 jq。"
        exit 1
    fi

    if [ -z "$API_KEY" ]; then
        echo "错误: 未找到 API 密钥。请设置 AISHELL_API_KEY 环境变量。"
        echo "例如: export AISHELL_API_KEY='your_api_key'"
        exit 1
    fi
}

# 初始化上下文
init_context() {
    # 确保上下文目录存在
    if [ ! -d "$CONTEXT_DIR" ]; then
        mkdir -p "$CONTEXT_DIR"
    fi

    # 初始化上下文文件
    if [ ! -f "$CONTEXT_FILE" ]; then
        # 如果文件不存在，创建一个包含系统提示词的初始 JSON 数组
        jq -n --arg content "$SYSTEM_PROMPT" '[{"role": "system", "content": $content}]' > "$CONTEXT_FILE"
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
    local current_context
    
    current_context=$(cat "$CONTEXT_FILE")
    
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
    
    echo "$new_context" > "$CONTEXT_FILE"
}

# 显示加载动画
start_spinner() {
    # 隐藏光标
    printf "\033[?25l" >&2
    (
        local delay=0.08
        local frames=("⠋" "⠙" "⠹" "⠸" "⠼" "⠴" "⠦" "⠧" "⠇" "⠏")
        while :; do
            for frame in "${frames[@]}"; do
                printf "\r \033[36m%s\033[0m 正在思考中..." "$frame" >&2
                sleep $delay
            done
        done
    ) &
    SPINNER_PID=$!
}

stop_spinner() {
    if [ -n "$SPINNER_PID" ]; then
        kill "$SPINNER_PID" >/dev/null 2>&1
        wait "$SPINNER_PID" >/dev/null 2>&1
        # 清除行并恢复光标
        printf "\r%s\r" "                       " >&2
        printf "\033[?25h" >&2
        SPINNER_PID=""
    fi
}

# 信号捕获：退出时恢复光标
cleanup() {
    stop_spinner
    printf "\033[?25h" >&2
    exit
}
trap cleanup SIGINT SIGTERM

# 调用 API 获取回复
# 参数: 上下文 JSON 内容
call_api() {
    local context="$1"
    
    # 准备 curl 请求数据
    local request_data
    request_data=$(jq -n \
        --arg model "$MODEL" \
        --argjson messages "$context" \
        --arg enable_thinking "$ENABLE_THINKING" \
        '{model: $model, messages: $messages} + (if $enable_thinking == "false" then {thinking: {type: "disabled"}} else {} end)')

    start_spinner

    # 调用 API
    local response
    response=$(curl -s "$API_URL" \
      -H "Authorization: Bearer $API_KEY" \
      -H "Content-Type: application/json" \
      -d "$request_data")
      
    local curl_exit_code=$?
    stop_spinner

    # 检查 curl 是否成功
    if [ $curl_exit_code -ne 0 ]; then
        echo "错误: API 请求失败。" >&2
        exit 1
    fi
    
    echo "$response"
}

# 解析 API 响应
# 参数: API 响应 JSON
parse_response() {
    local response="$1"
    
    # 检查 API 返回是否有错误
    local error_msg
    error_msg=$(echo "$response" | jq -r '.error.message // empty')
    if [ -n "$error_msg" ]; then
        echo "API 错误: $error_msg" >&2
        exit 1
    fi

    # 提取 AI 回复的内容
    local ai_content
    ai_content=$(echo "$response" | jq -r '.choices[0].message.content // empty')

    if [ -z "$ai_content" ]; then
        echo "错误: 未能获取 AI 回复。" >&2
        echo "调试信息: $response" >&2
        exit 1
    fi
    
    echo "$ai_content"
}

# 清理命令（去除 markdown 标记）
clean_command() {
    local content="$1"
    # 去除可能的 markdown 代码块标记 (```bash ... ``` 或 ``` ...)
    echo "$content" | sed 's/^```[a-z]*//g' | sed 's/```$//g' | sed 's/`//g' | awk '{$1=$1};1'
}

# 主逻辑
main() {
    check_dependencies
    init_context
    
    local user_input
    user_input=$(get_user_input "$@")

    # 检查是否为清理命令
    if [[ "$user_input" == "clear" ]]; then
        rm -f "$CONTEXT_FILE"
        echo "上下文已清理。"
        exit 0
    fi
    
    # 添加用户消息到上下文
    update_context "user" "$user_input"
    
    # 读取最新上下文用于 API 调用
    local current_context
    current_context=$(cat "$CONTEXT_FILE")
    
    # 调用 API
    local api_response
    api_response=$(call_api "$current_context")
    
    # 解析响应
    local ai_content
    ai_content=$(parse_response "$api_response")
    
    # 清理命令
    local clean_cmd
    clean_cmd=$(clean_command "$ai_content")
    
    # 更新上下文（保存 AI 回复）
    update_context "assistant" "$ai_content"
    
    # 输出命令并询问执行
    echo "生成的命令:"
    echo -e "\033[32m$clean_cmd\033[0m"

    read -p "是否执行该命令? (Y/n): " execute_confirm

    # 默认回车为 y
    if [[ -z "$execute_confirm" || "$execute_confirm" =~ ^[Yy]$ ]]; then
        echo "正在执行..."
        
        # 捕获命令输出（同时捕获 stdout 和 stderr）
        # 使用临时文件来保存输出，避免管道导致的子 shell 问题或复杂的转义问题
        local output_file
        output_file=$(mktemp)
        
        eval "$clean_cmd" > "$output_file" 2>&1
        local exit_code=$?
        
        # 显示输出到终端
        cat "$output_file"
        
        # 读取输出内容
        local cmd_output
        cmd_output=$(cat "$output_file")
        rm "$output_file"
        
        # 构建反馈给 AI 的消息
        local feedback_msg
        if [ $exit_code -eq 0 ]; then
            feedback_msg="命令执行成功，输出如下：\n$cmd_output"
        else
            feedback_msg="命令执行失败 (退出码 $exit_code)，错误输出如下：\n$cmd_output"
        fi
        
        # 将执行结果添加到上下文
        update_context "user" "$feedback_msg"
    else
        echo "已取消执行。"
    fi
}

# 执行主函数
main "$@"
