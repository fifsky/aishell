#!/bin/bash

# 配置信息
API_URL="${AISHELL_BASE_URL:-https://api.moonshot.cn/v1/chat/completions}"
API_KEY="${AISHELL_API_KEY}"
MODEL="${AISHELL_MODEL:-kimi-k2.5}"
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
    
    # 使用 jq 将新消息追加到数组末尾
    local new_context
    new_context=$(echo "$current_context" | jq --arg role "$role" --arg content "$content" '. + [{"role": $role, "content": $content}]')
    
    echo "$new_context" > "$CONTEXT_FILE"
}

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

    echo "正在思考中..." >&2

    # 调用 API
    local response
    response=$(curl -s "$API_URL" \
      -H "Authorization: Bearer $API_KEY" \
      -H "Content-Type: application/json" \
      -d "$request_data")
      
    # 检查 curl 是否成功
    if [ $? -ne 0 ]; then
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

    read -p "是否执行该命令? (y/n): " execute_confirm

    if [[ "$execute_confirm" =~ ^[Yy]$ ]]; then
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
