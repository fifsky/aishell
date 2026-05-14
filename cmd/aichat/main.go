package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"go.yaml.in/yaml/v3"
)

const (
	defaultAPIURL     = "https://aiproxy.fifsky.com/v1/chat/completions"
	defaultModel      = "deepseek-v4-pro"
	defaultMaxContext = 100
	defaultMaxSteps   = 5
	defaultSession    = "default"
)

var autoApproveCommandPrefixes = []string{
	"tvly search ",
}

type config struct {
	APIURL           string
	APIKey           string
	Model            string
	MaxContext       int
	MaxSteps         int
	BaseDir          string
	SessionDir       string
	ConfigFile       string
	SystemPromptFile string
	SkillsDir        string
}

type appConfig struct {
	CurrentSession string `json:"current_session"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type frontmatter struct {
	Name               string         `yaml:"name"`
	Description        string         `yaml:"description"`
	License            string         `yaml:"license"`
	Compatibility      string         `yaml:"compatibility"`
	AllowedTools       string         `yaml:"allowed_tools"`
	AllowedToolsHyphen string         `yaml:"allowed-tools"`
	Metadata           map[string]any `yaml:"metadata"`
}

type skillResources struct {
	References map[string]string
	Assets     map[string][]byte
	Scripts    map[string]string
}

type skill struct {
	Frontmatter frontmatter
	Content     string
	Path        string
	Resources   skillResources
}

type deltaMsg string
type doneMsg struct {
	content string
	err     error
}

type streamModel struct {
	width    int
	raw      strings.Builder
	visible  streamFilter
	done     bool
	err      error
	renderer *glamour.TermRenderer
}

type streamFilter struct {
	inExec bool
	buffer strings.Builder
	out    strings.Builder
}

type confirmModel struct {
	command  string
	choice   int
	done     bool
	approved bool
}

type selectModel struct {
	title    string
	items    []string
	cursor   int
	selected map[int]bool
	multi    bool
	done     bool
	cancel   bool
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := checkConfig(cfg); err != nil {
		return err
	}
	if err := initConfig(cfg); err != nil {
		return err
	}

	if len(args) > 0 {
		switch args[0] {
		case "session":
			if len(args) < 2 {
				return errors.New("用法: aichat session <session_name>")
			}
			if err := setSession(cfg, args[1]); err != nil {
				return err
			}
			if err := initContext(cfg); err != nil {
				return err
			}
			fmt.Printf("已切换到session: \033[32m%s\033[0m\n", args[1])
			return nil
		case "list":
			return listSessions(cfg)
		case "delete":
			return deleteSessions(cfg)
		}
	}

	if err := initContext(cfg); err != nil {
		return err
	}

	userInput := strings.TrimSpace(strings.Join(args, " "))
	if userInput == "" {
		return errors.New("用法: aichat <你的需求>")
	}

	sessionName, err := currentSession(cfg)
	if err != nil {
		return err
	}
	contextFile := filepath.Join(cfg.SessionDir, sessionName+".json")
	if userInput == "clear" {
		if err := os.Remove(contextFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		fmt.Println("上下文已清理。")
		return nil
	}

	if err := updateContext(cfg, "user", userInput); err != nil {
		return err
	}

	for step := 1; step <= cfg.MaxSteps; step++ {
		messages, err := readMessages(contextFile)
		if err != nil {
			return err
		}

		aiContent, err := streamChat(ctx, cfg, messages)
		if err != nil {
			return err
		}
		if err := updateContext(cfg, "assistant", aiContent); err != nil {
			return err
		}

		execCmd := extractExecCommand(aiContent)
		if strings.TrimSpace(execCmd) == "" {
			return nil
		}

		fmt.Printf("\n\033[33m需要执行的命令:\033[0m\n")
		fmt.Printf("\033[32m%s\033[0m\n", execCmd)

		if isAutoApprovedCommand(execCmd) {
			fmt.Println("命中白名单，自动执行。")
			if err := executeShellCommand(cfg, execCmd); err != nil {
				return err
			}
			continue
		}

		approved, err := confirmCommand(execCmd)
		if err != nil {
			fmt.Println("未收到确认，已取消执行。")
			if updateErr := updateContext(cfg, "user", "未收到用户确认，上一条命令没有执行。请在不执行该命令的前提下继续回答，或提供更安全的替代方案。"); updateErr != nil {
				return updateErr
			}
			continue
		}
		if approved {
			if err := executeShellCommand(cfg, execCmd); err != nil {
				return err
			}
		} else {
			fmt.Println("已取消执行。")
			if err := updateContext(cfg, "user", "用户拒绝执行上一条命令。请在不执行该命令的前提下继续回答，或提供更安全的替代方案。"); err != nil {
				return err
			}
		}
	}

	fmt.Printf("已达到最大自动处理轮数 (%d)，如需继续请再次输入你的需求。\n", cfg.MaxSteps)
	return nil
}

func loadConfig() (config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return config{}, err
	}
	baseDir := filepath.Join(home, ".aishell")
	cfg := config{
		APIURL:     envFirst(defaultAPIURL, "AICHAT_BASE_URL", "AISHELL_BASE_URL"),
		APIKey:     envFirst("", "AICHAT_API_KEY", "AISHELL_API_KEY"),
		Model:      envFirst(defaultModel, "AICHAT_MODEL", "AISHELL_MODEL"),
		MaxContext: envInt(defaultMaxContext, "AICHAT_MAX_CONTEXT", "AISHELL_MAX_CONTEXT"),
		MaxSteps:   envInt(defaultMaxSteps, "AICHAT_MAX_STEPS", "AISHELL_MAX_STEPS"),
		BaseDir:    baseDir,
		SessionDir: filepath.Join(baseDir, "sessions"),
		ConfigFile: filepath.Join(baseDir, "config.json"),
		SkillsDir:  envFirst(filepath.Join(home, ".agents", "skills"), "AICHAT_SKILLS_DIR"),
	}
	cfg.SystemPromptFile = resolveSystemPromptFile()
	return cfg, nil
}

func envFirst(def string, keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return def
}

func envInt(def int, keys ...string) int {
	for _, key := range keys {
		value := os.Getenv(key)
		if value == "" {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err == nil && parsed > 0 {
			return parsed
		}
	}
	return def
}

func resolveSystemPromptFile() string {
	if value := envFirst("", "AICHAT_SYSTEM_PROMPT_FILE", "AISHELL_SYSTEM_PROMPT_FILE"); value != "" {
		return value
	}
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "system_prompt.md"))
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "system_prompt.md"))
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return "system_prompt.md"
}

func checkConfig(cfg config) error {
	if cfg.APIKey == "" {
		return errors.New("错误: 未找到 API 密钥。请设置 AICHAT_API_KEY 或 AISHELL_API_KEY 环境变量。")
	}
	if cfg.MaxContext < 1 {
		return errors.New("错误: MAX_CONTEXT 必须是大于 0 的整数。")
	}
	if cfg.MaxSteps < 1 {
		return errors.New("错误: MAX_STEPS 必须是大于 0 的整数。")
	}
	return nil
}

func initConfig(cfg config) error {
	if err := os.MkdirAll(cfg.SessionDir, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(cfg.ConfigFile); errors.Is(err, os.ErrNotExist) {
		data, err := json.MarshalIndent(appConfig{CurrentSession: defaultSession}, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(cfg.ConfigFile, data, 0o644)
	}
	return nil
}

func currentSession(cfg config) (string, error) {
	data, err := os.ReadFile(cfg.ConfigFile)
	if err != nil {
		return "", err
	}
	var appCfg appConfig
	if err := json.Unmarshal(data, &appCfg); err != nil {
		return "", err
	}
	if appCfg.CurrentSession == "" {
		return defaultSession, nil
	}
	return appCfg.CurrentSession, nil
}

func setSession(cfg config, session string) error {
	data, err := json.MarshalIndent(appConfig{CurrentSession: session}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cfg.ConfigFile, data, 0o644)
}

func listSessions(cfg config) error {
	sessions, err := sessionNames(cfg)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		fmt.Println("当前没有可用的session。")
		return nil
	}
	current, err := currentSession(cfg)
	if err != nil {
		return err
	}
	index := 0
	for i, session := range sessions {
		if session == current {
			index = i
			break
		}
	}
	selected, ok, err := selectSessions(fmt.Sprintf("选择session (当前: %s)", current), sessions, index, false)
	if err != nil {
		return err
	}
	if !ok || len(selected) == 0 {
		return nil
	}
	if err := setSession(cfg, sessions[selected[0]]); err != nil {
		return err
	}
	if err := initContext(cfg); err != nil {
		return err
	}
	fmt.Printf("已切换到session: \033[32m%s\033[0m\n", sessions[selected[0]])
	return nil
}

func deleteSessions(cfg config) error {
	sessions, err := sessionNames(cfg)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		fmt.Println("当前没有可用的session。")
		return nil
	}
	current, err := currentSession(cfg)
	if err != nil {
		return err
	}
	selected, ok, err := selectSessions(fmt.Sprintf("选择要删除的session (当前: %s)", current), sessions, 0, true)
	if err != nil {
		return err
	}
	if !ok || len(selected) == 0 {
		fmt.Println("已取消删除。")
		return nil
	}
	resetCurrent := false
	for _, idx := range selected {
		session := sessions[idx]
		if err := os.Remove(filepath.Join(cfg.SessionDir, session+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		fmt.Printf("已删除session: \033[31m%s\033[0m\n", session)
		if session == current {
			resetCurrent = true
		}
	}
	if resetCurrent {
		if err := setSession(cfg, defaultSession); err != nil {
			return err
		}
		if err := initContext(cfg); err != nil {
			return err
		}
		fmt.Printf("当前session已重置为: \033[32m%s\033[0m\n", defaultSession)
	}
	return nil
}

func sessionNames(cfg config) ([]string, error) {
	entries, err := os.ReadDir(cfg.SessionDir)
	if err != nil {
		return nil, err
	}
	sessions := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		sessions = append(sessions, strings.TrimSuffix(entry.Name(), ".json"))
	}
	sort.Strings(sessions)
	return sessions, nil
}

func initContext(cfg config) error {
	sessionName, err := currentSession(cfg)
	if err != nil {
		return err
	}
	contextFile := filepath.Join(cfg.SessionDir, sessionName+".json")
	systemPrompt, err := buildSystemPrompt(cfg)
	if err != nil {
		return err
	}

	messages := []message{}
	if data, err := os.ReadFile(contextFile); err == nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &messages); err != nil {
			return err
		}
	}
	if len(messages) == 0 {
		messages = []message{{Role: "system", Content: systemPrompt}}
	} else if messages[0].Role == "system" {
		messages[0].Content = systemPrompt
	} else {
		messages = append([]message{{Role: "system", Content: systemPrompt}}, messages...)
	}
	return writeMessages(contextFile, messages)
}

func buildSystemPrompt(cfg config) (string, error) {
	data, err := os.ReadFile(cfg.SystemPromptFile)
	if err != nil {
		return "", fmt.Errorf("错误: 未找到系统提示词文件: %s", cfg.SystemPromptFile)
	}
	prompt := strings.TrimSpace(string(data))
	if prompt == "" {
		return "", fmt.Errorf("错误: 系统提示词文件为空: %s", cfg.SystemPromptFile)
	}

	skills, err := loadSkills(cfg.SkillsDir)
	if err != nil || len(skills) == 0 {
		return prompt, nil
	}

	var b strings.Builder
	b.WriteString(prompt)
	b.WriteString("\n\n## 自动加载的技能\n\n")
	b.WriteString("以下技能来自 `")
	b.WriteString(cfg.SkillsDir)
	b.WriteString("`。当用户请求与某个技能描述匹配时，优先遵循对应技能。需要查看完整技能说明、references、scripts 或 assets 时，可通过 `shell-exec` 使用 bash tool 读取对应文件；需要执行技能里的命令或脚本时，也通过 `shell-exec` 请求 bash tool。\n\n")
	for _, skill := range skills {
		b.WriteString("- ")
		b.WriteString(skill.Name())
		if skill.Description() != "" {
			b.WriteString(": ")
			b.WriteString(oneLine(skill.Description()))
		}
		b.WriteString(" (")
		b.WriteString(skill.Path)
		b.WriteString(")")
		if skill.AllowedTools() != "" {
			b.WriteString(" allowed-tools=")
			b.WriteString(skill.AllowedTools())
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

func loadSkills(root string) ([]*skill, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var skills []*skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillDir := filepath.Join(root, entry.Name())
		loaded, err := parseSkillDir(skillDir)
		if err != nil {
			continue
		}
		if loaded.Frontmatter.Name == "" {
			loaded.Frontmatter.Name = entry.Name()
		}
		skills = append(skills, loaded)
	}
	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Name() < skills[j].Name()
	})
	return skills, nil
}

func parseSkillDir(dir string) (*skill, error) {
	skillFile := filepath.Join(dir, "SKILL.md")
	if _, err := os.Stat(skillFile); os.IsNotExist(err) {
		skillFile = filepath.Join(dir, "skill.md")
		if _, err := os.Stat(skillFile); os.IsNotExist(err) {
			return nil, fmt.Errorf("SKILL.md not found in %s", dir)
		}
	}
	parsed, err := parseSkillFile(skillFile)
	if err != nil {
		return nil, err
	}
	parsed.Path = dir
	fsys := os.DirFS(dir)
	parsed.Resources.References, _ = loadDirFiles(fsys, "references")
	parsed.Resources.Scripts, _ = loadDirFiles(fsys, "scripts")
	parsed.Resources.Assets, _ = loadDirBinaryFiles(fsys, "assets")
	return parsed, nil
}

func parseSkillFile(path string) (*skill, error) {
	contentBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	content := string(contentBytes)
	parsed := &skill{Path: path}

	firstLine, rest, hasMore := cutLine(content)
	if !isFrontmatterDelimiterLine(firstLine) || !hasMore {
		parsed.Content = strings.TrimSpace(content)
		return parsed, nil
	}

	search := rest
	frontmatterLen := 0
	foundEnd := false
	for {
		line, remaining, hasNext := cutLine(search)
		if isFrontmatterDelimiterLine(line) {
			search = remaining
			foundEnd = true
			break
		}
		if !hasNext {
			break
		}
		frontmatterLen += len(line) + 1
		search = remaining
	}

	if !foundEnd {
		parsed.Content = strings.TrimSpace(content)
		return parsed, nil
	}

	frontmatterContent := rest[:frontmatterLen]
	if err := yaml.Unmarshal([]byte(frontmatterContent), &parsed.Frontmatter); err != nil {
		return nil, fmt.Errorf("failed to parse frontmatter: %w", err)
	}
	parsed.Content = strings.TrimSpace(search)
	return parsed, nil
}

func loadDirFiles(fsys fs.FS, dir string) (map[string]string, error) {
	files := map[string]string{}
	err := fs.WalkDir(fsys, dir, func(filePath string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fs.SkipDir
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := fs.ReadFile(fsys, filePath)
		if err != nil {
			return err
		}
		files[strings.TrimPrefix(filePath, dir+"/")] = string(b)
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return files, nil
}

func loadDirBinaryFiles(fsys fs.FS, dir string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := fs.WalkDir(fsys, dir, func(filePath string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fs.SkipDir
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := fs.ReadFile(fsys, filePath)
		if err != nil {
			return err
		}
		files[strings.TrimPrefix(filePath, dir+"/")] = b
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return files, nil
}

func (s *skill) Name() string {
	return s.Frontmatter.Name
}

func (s *skill) Description() string {
	return s.Frontmatter.Description
}

func (s *skill) AllowedTools() string {
	if s.Frontmatter.AllowedTools != "" {
		return s.Frontmatter.AllowedTools
	}
	return s.Frontmatter.AllowedToolsHyphen
}

func cutLine(content string) (line string, rest string, hasMore bool) {
	idx := strings.IndexByte(content, '\n')
	if idx < 0 {
		return content, "", false
	}
	return content[:idx], content[idx+1:], true
}

func isFrontmatterDelimiterLine(line string) bool {
	return strings.TrimSuffix(line, "\r") == "---"
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func readMessages(path string) ([]message, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var messages []message
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

func writeMessages(path string, messages []message) error {
	data, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func updateContext(cfg config, role, content string) error {
	sessionName, err := currentSession(cfg)
	if err != nil {
		return err
	}
	contextFile := filepath.Join(cfg.SessionDir, sessionName+".json")
	messages, err := readMessages(contextFile)
	if err != nil {
		return err
	}
	messages = append(messages, message{Role: role, Content: content})
	if len(messages) > cfg.MaxContext {
		keep := cfg.MaxContext - 1
		tail := messages[1:]
		if len(tail) > keep {
			tail = tail[len(tail)-keep:]
		}
		messages = append([]message{messages[0]}, tail...)
	}
	return writeMessages(contextFile, messages)
}

func streamChat(ctx context.Context, cfg config, messages []message) (string, error) {
	model := newStreamModel()
	program := tea.NewProgram(model)

	go func() {
		content, err := openAIStream(ctx, cfg, messages, program.Send)
		program.Send(doneMsg{content: content, err: err})
	}()

	finalModel, err := program.Run()
	if err != nil {
		return "", err
	}
	result := finalModel.(streamModel)
	if result.err != nil {
		return "", result.err
	}
	return result.raw.String(), nil
}

func newStreamModel() streamModel {
	renderer, _ := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(100))
	return streamModel{
		width:    100,
		renderer: renderer,
	}
}

func (m streamModel) Init() tea.Cmd {
	return nil
}

func (m streamModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		if renderer, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(max(20, msg.Width-2))); err == nil {
			m.renderer = renderer
		}
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.err = errors.New("已取消")
			return m, tea.Quit
		}
	case deltaMsg:
		delta := string(msg)
		m.raw.WriteString(delta)
		_, _ = m.visible.WriteString(delta)
	case doneMsg:
		m.done = true
		m.err = msg.err
		if m.err == nil && msg.content != "" && m.raw.Len() == 0 {
			m.raw.WriteString(msg.content)
		}
		_ = m.visible.Flush()
		return m, tea.Quit
	}
	return m, nil
}

func (m streamModel) View() string {
	if m.err != nil {
		return "\n错误: " + m.err.Error() + "\n"
	}
	content := m.visible.String()
	if content == "" && !m.done {
		return "\n正在思考...\n"
	}
	rendered := content
	if m.renderer != nil {
		if out, err := m.renderer.Render(content); err == nil {
			rendered = out
		}
	}
	if !m.done {
		rendered += "\n"
	}
	return rendered
}

func openAIStream(ctx context.Context, cfg config, messages []message, send func(tea.Msg)) (string, error) {
	client := openai.NewClient(
		option.WithAPIKey(cfg.APIKey),
		option.WithBaseURL(strings.TrimSuffix(cfg.APIURL, "/chat/completions")),
	)
	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(cfg.Model),
		Messages: toOpenAIMessages(messages),
	}
	var opts []option.RequestOption
	if strings.HasPrefix(cfg.Model, "qwen") {
		opts = append(opts, option.WithJSONSet("enable_thinking", false))
	} else {
		opts = append(opts, option.WithJSONSet("thinking", map[string]string{"type": "disabled"}))
	}

	stream := client.Chat.Completions.NewStreaming(ctx, params, opts...)
	var full strings.Builder
	for stream.Next() {
		chunk := stream.Current()
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta.Content
		if delta == "" {
			continue
		}
		full.WriteString(delta)
		send(deltaMsg(delta))
	}
	if err := stream.Err(); err != nil {
		return full.String(), err
	}
	if full.Len() == 0 {
		return "", errors.New("错误: 未能获取 AI 回复")
	}
	return full.String(), nil
}

func toOpenAIMessages(messages []message) []openai.ChatCompletionMessageParamUnion {
	result := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case "system":
			result = append(result, openai.SystemMessage(msg.Content))
		case "assistant":
			result = append(result, openai.AssistantMessage(msg.Content))
		default:
			result = append(result, openai.UserMessage(msg.Content))
		}
	}
	return result
}

func (f *streamFilter) WriteString(delta string) (int, error) {
	for _, r := range delta {
		if r == '\n' {
			f.writeLine(f.buffer.String())
			f.buffer.Reset()
			continue
		}
		f.buffer.WriteRune(r)
	}
	return len(delta), nil
}

func (f *streamFilter) Flush() error {
	if f.buffer.Len() == 0 {
		return nil
	}
	f.writeLine(f.buffer.String())
	f.buffer.Reset()
	return nil
}

func (f *streamFilter) writeLine(line string) {
	trimmed := strings.TrimSpace(line)
	if !f.inExec && trimmed == "```shell-exec" {
		f.inExec = true
		return
	}
	if f.inExec {
		if trimmed == "```" {
			f.inExec = false
		}
		return
	}
	f.out.WriteString(line)
	f.out.WriteByte('\n')
}

func (f *streamFilter) String() string {
	return f.out.String()
}

func extractExecCommand(content string) string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	inExec := false
	var b strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if !inExec && trimmed == "```shell-exec" {
			inExec = true
			continue
		}
		if inExec && trimmed == "```" {
			break
		}
		if inExec {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func isAutoApprovedCommand(commandText string) bool {
	normalized := strings.TrimLeft(commandText, " \t\r\n")
	for _, prefix := range autoApproveCommandPrefixes {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}

func executeShellCommand(cfg config, commandText string) error {
	fmt.Println("正在执行...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-lc", commandText)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()

	fmt.Print(output.String())
	exitCode := 0
	if err != nil {
		exitCode = 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}

	var feedback string
	if err == nil {
		feedback = fmt.Sprintf("命令执行成功，退出码 0，输出如下：\n%s", output.String())
	} else {
		feedback = fmt.Sprintf("命令执行失败，退出码 %d，输出如下：\n%s", exitCode, output.String())
	}
	return updateContext(cfg, "user", feedback)
}

func confirmCommand(command string) (bool, error) {
	model := confirmModel{command: command}
	finalModel, err := tea.NewProgram(model).Run()
	if err != nil {
		return false, err
	}
	result := finalModel.(confirmModel)
	if !result.done {
		return false, io.EOF
	}
	return result.approved, nil
}

func (m confirmModel) Init() tea.Cmd {
	return nil
}

func (m confirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.done = true
			m.approved = false
			return m, tea.Quit
		case "left", "h":
			m.choice = 0
		case "right", "l":
			m.choice = 1
		case "y", "Y":
			m.done = true
			m.approved = true
			return m, tea.Quit
		case "n", "N":
			m.done = true
			m.approved = false
			return m, tea.Quit
		case "enter":
			m.done = true
			m.approved = m.choice == 0
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m confirmModel) View() string {
	yes := "[Y 执行]"
	no := "[N 取消]"
	if m.choice == 1 {
		yes = " Y 执行 "
		no = "[N 取消]"
	}
	return fmt.Sprintf("是否执行该命令?\n\033[32m%s\033[0m\n%s  %s\n", m.command, yes, no)
}

func selectSessions(title string, items []string, cursor int, multi bool) ([]int, bool, error) {
	model := selectModel{
		title:    title,
		items:    items,
		cursor:   cursor,
		selected: map[int]bool{},
		multi:    multi,
	}
	finalModel, err := tea.NewProgram(model).Run()
	if err != nil {
		return nil, false, err
	}
	result := finalModel.(selectModel)
	if result.cancel || !result.done {
		return nil, false, nil
	}
	if !multi {
		return []int{result.cursor}, true, nil
	}
	selected := make([]int, 0, len(result.selected))
	for idx := range result.selected {
		selected = append(selected, idx)
	}
	sort.Ints(selected)
	return selected, true, nil
}

func (m selectModel) Init() tea.Cmd {
	return nil
}

func (m selectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.cancel = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case " ":
			if m.multi {
				m.selected[m.cursor] = !m.selected[m.cursor]
			}
		case "enter":
			m.done = true
			if m.multi && len(m.selected) == 0 {
				m.selected[m.cursor] = true
			}
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m selectModel) View() string {
	var b strings.Builder
	b.WriteString(m.title)
	b.WriteString("\n\n")
	for i, item := range m.items {
		cursor := " "
		if i == m.cursor {
			cursor = ">"
		}
		marker := " "
		if m.multi && m.selected[i] {
			marker = "x"
		}
		if m.multi {
			fmt.Fprintf(&b, "%s [%s] %s\n", cursor, marker, item)
		} else {
			fmt.Fprintf(&b, "%s %s\n", cursor, item)
		}
	}
	if m.multi {
		b.WriteString("\nspace 选择，enter 确认，esc 取消\n")
	} else {
		b.WriteString("\nenter 确认，esc 取消\n")
	}
	return b.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
