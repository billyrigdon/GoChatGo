package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alecthomas/chroma/styles"
	tiktoken "github.com/pkoukk/tiktoken-go"
)

var (
	_ = styles.Fallback
)
var (
	encoder *tiktoken.Tiktoken
)

var useFusion *bool

func init() {
	var err error
	encoder, err = tiktoken.EncodingForModel("gpt-4o")
	if err != nil {
		log.Fatalf("tiktoken init: %v", err)
	}
}

func tokens(s string) int     { return len(encoder.EncodeOrdinary(s)) }
func tokensMsg(m Message) int { return 4 + tokens(m.Role) + tokens(m.Content) }

func queryGPT(model, systemPrompt string, temp float64, maxTok int,
	msgs []Message, stream bool) string {

	msgs = append([]Message{{Role: "system", Content: systemPrompt}}, msgs...)

	payload := map[string]any{
		"model":             model,
		"messages":          msgs,
		"temperature":       temp,
		"max_tokens":        maxTok,
		"top_p":             0.96,
		"frequency_penalty": 0.3,
		"presence_penalty":  0.0,
		"stream":            stream,
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		log.Fatalf("encode payload: %v", err)
	}

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		apiURL+"/v1/chat/completions",
		&buf,
	)
	if err != nil {
		log.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Fatalf("http: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		log.Fatalf("openai: %s – %s", resp.Status, body)
	}

	if !stream {
		var out struct {
			Choices []struct {
				Message Message `json:"message"`
			} `json:"choices"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			log.Fatalf("decode: %v", err)
		}
		resp.Body.Close()
		return out.Choices[0].Message.Content
	}

	reader := bufio.NewReader(resp.Body)
	var answer strings.Builder

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Printf("stream read: %v", err)
			}
			break
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(line[len("data:"):])
		if data == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		text := chunk.Choices[0].Delta.Content
		fmt.Print(text)
		answer.WriteString(text)
	}
	resp.Body.Close()

	return answer.String()
}

func clearChatLog() {
	_ = os.RemoveAll(logDirPath)
	_ = os.MkdirAll(logDirPath, 0o755)
	fmt.Println("chat history cleared")
}

func dailyLogPath() string {
	return filepath.Join(logDirPath, time.Now().Format("2006-01-02")+".json")
}

func appendLog(req, resp string) error {
	var logs []ChatLog
	p := dailyLogPath()
	if data, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(data, &logs)
	}
	logs = append(logs, ChatLog{Timestamp: time.Now(), Request: req, Response: resp})
	data, _ := json.MarshalIndent(logs, "", "  ")
	return os.WriteFile(p, data, 0o644)
}

func printChatLog(n int) {
	p := dailyLogPath()
	data, err := os.ReadFile(p)
	if err != nil {
		log.Fatalf("read log: %v", err)
	}
	var logs []ChatLog
	_ = json.Unmarshal(data, &logs)

	if n > 0 && len(logs) > n {
		logs = logs[len(logs)-n:]
	}
	for _, l := range logs {
		fmt.Printf("%s\n> %s\n%s\n\n",
			l.Timestamp.Format(time.RFC822), l.Request, l.Response)
	}
}

func getConfig() Config {
	var cfg Config

	data, err := os.ReadFile(configFilePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg.AIName = "Archie"
			cfg.UserName = "User"
			return cfg
		}
		log.Fatalf("read config: %v", err)
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("parse config: %v", err)
	}

	if cfg.AIName == "" {
		cfg.AIName = "Archie"
	}
	if cfg.UserName == "" {
		cfg.UserName = "User"
	}
	return cfg
}

func savePersonality(p string) {
	cfg := getConfig()
	cfg.Personality = p
	saveConfig(cfg)
	fmt.Println("personality saved")
}

func updateConfig(user, ai, bio string) {
	cfg := getConfig()
	if user != "" {
		cfg.UserName = user
	}
	if ai != "" {
		cfg.AIName = ai
	}
	if bio != "" {
		cfg.Bio = bio
	}
	saveConfig(cfg)
	fmt.Println("config updated")
}

func saveConfig(c Config) {
	data, _ := json.MarshalIndent(c, "", "  ")
	_ = os.WriteFile(configFilePath, data, 0o644)
}

func enterInteractiveMode() {
	r := bufio.NewReader(os.Stdin)
	fmt.Println("interactive mode – type 'exit' to quit")
	for {
		fmt.Print("> ")
		line, _ := r.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "exit" {
			break
		}
		if line == "" {
			continue
		}
		sendChat(line)
	}
}

type AppState struct {
	CheckInEnabled bool      `json:"check_in_enabled"`
	LastChecked    time.Time `json:"last_checked"`
}

func runAsDaemon() {
	for {
		checkInUser()
		time.Sleep(30 * time.Minute)
	}
}

func toggleCheckInFeature() {
	st := getState()
	st.CheckInEnabled = !st.CheckInEnabled
	saveState(st)
	fmt.Printf("check‑ins now %v\n", st.CheckInEnabled)
}

func checkInUser() {
	st := getState()
	if !st.CheckInEnabled || time.Since(st.LastChecked) < 2*time.Hour {
		return
	}
	st.LastChecked = time.Now()
	saveState(st)

	sendChat("Hey there! Just checking in – how are you doing?")
}

func getState() AppState {
	var st AppState
	if data, err := os.ReadFile(stateFilePath); err == nil {
		_ = json.Unmarshal(data, &st)
	} else {
		st.CheckInEnabled = true
	}
	return st
}

func saveState(st AppState) {
	data, _ := json.MarshalIndent(st, "", "  ")
	_ = os.WriteFile(stateFilePath, data, 0o644)
}

func promptUserForInstructions(filePath string) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		log.Fatalf("read file: %v", err)
	}
	fmt.Print("What should I do with this file? ")
	instr, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	instr = strings.TrimSpace(instr)

	sendChat(instr + "\n\n```text\n" + string(content) + "\n```")
}

var (
	apiKey = os.Getenv("OPENAI_API_KEY")
	apiURL = os.Getenv("OPENAI_API_BASE")
)

const (
	defaultAPIBase = "https://api.openai.com"

	modelExec      = "gpt-4o"
	modelLogic     = "gpt-4o-mini"
	modelCreative  = "gpt-4o-mini"
	modelSummarise = "gpt-4o-mini"

	contextWindowTokens = 128000 // gpt‑4o context window
)

const (
	tagMem   = "<MEMORY>"
	tagLeft  = "<LEFT>"
	tagRight = "<RIGHT>"
	tagEnd   = "</END>"
)

type ChatLog struct {
	Timestamp time.Time `json:"timestamp"`
	Request   string    `json:"request"`
	Response  string    `json:"response"`
}

type State struct {
	LastInteraction time.Time `json:"last_interaction"`
	CheckInEnabled  bool      `json:"check_in_enabled"`
}

type Config struct {
	UserName    string `json:"user_name"`
	AIName      string `json:"ai_name"`
	Bio         string `json:"bio"`
	Personality string `json:"personality"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

var (
	homeDir        string
	logDirPath     string
	stateFilePath  string
	configFilePath string
	httpClient     *http.Client
)

func init() {
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY env missing")
	}
	if apiURL == "" {
		apiURL = defaultAPIBase
	}

	var err error
	encoder, err = tiktoken.EncodingForModel(modelExec)
	if err != nil {
		log.Fatalf("tokeniser: %v", err)
	}

	usr, err := user.Current()
	if err != nil {
		log.Fatalf("user.Current(): %v", err)
	}
	homeDir = usr.HomeDir
	logDirPath = filepath.Join(homeDir, ".go-chat-logs")
	stateFilePath = filepath.Join(homeDir, ".go-chat-state")
	configFilePath = filepath.Join(homeDir, ".go-chat-config")

	if err := os.MkdirAll(logDirPath, 0o755); err != nil {
		log.Fatalf("mkdir logs: %v", err)
	}

	httpClient = &http.Client{Timeout: 30 * time.Second}
}

func main() {
	useFusion = flag.Bool("fusion", false, "Use multi-model fusion mode")
	clearLog := flag.Bool("c", false, "Clear chat log")
	personality := flag.String("p", "", "Set AI personality")
	printLog := flag.Bool("a", false, "Print today's log")
	printLines := flag.Int("n", 0, "Print last N log lines")
	interactive := flag.Bool("i", false, "Interactive mode")
	daemon := flag.Bool("d", false, "Daemon mode (check‑ins)")
	toggle := flag.Bool("t", false, "Toggle check‑ins")
	upload := flag.String("f", "", "Upload file")
	setUser := flag.String("u", "", "Set user name")
	setAI := flag.String("ai", "", "Set AI name")
	setBio := flag.String("b", "", "Set bio")
	flag.Parse()

	switch {
	case *clearLog:
		clearChatLog()
		return
	case *personality != "":
		savePersonality(*personality)
		return
	case *setUser != "" || *setAI != "" || *setBio != "":
		updateConfig(*setUser, *setAI, *setBio)
		return
	case *printLog:
		printChatLog(*printLines)
		return
	case *interactive:
		enterInteractiveMode()
		return
	case *daemon:
		runAsDaemon()
		return
	case *toggle:
		toggleCheckInFeature()
		return
	case *upload != "":
		promptUserForInstructions(*upload)
		return
	}

	if args := flag.Args(); len(args) > 0 {
		sendChat(strings.Join(args, " "))
	} else {
		fmt.Println("No prompt given. Use -h.")
	}
}

func trimHistory(hist []Message, limit int) []Message {
	total := 0
	for i := len(hist) - 1; i >= 0; i-- {
		total += tokensMsg(hist[i])
		if total > limit {
			return hist[i+1:]
		}
	}
	return hist
}

func buildHistory(system, latest string) []Message {
	hist := trimHistory(getChatHistory(), contextWindowTokens-2048)

	return append(
		[]Message{{Role: "system", Content: system}},
		append(hist, Message{Role: "user", Content: latest})...,
	)
}

func summarizeDayLogs() {
	p := dailyLogPath()

	data, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var logs []ChatLog
	if err := json.Unmarshal(data, &logs); err != nil {
		return
	}

	var msgs []Message
	for _, l := range logs {
		msgs = append(msgs, Message{Role: "user", Content: l.Request})
		msgs = append(msgs, Message{Role: "assistant", Content: l.Response})
	}

	summary := queryGPT(
		modelSummarise,
		"Summarize this conversation to preserve key facts, decisions, tone, and ongoing themes.",
		0.4, 512, msgs, false,
	)

	saveVectorMemory(summary)
}

func sendChat(userPrompt string) {
	cfg := getConfig()
	relevant := getRelevantMemories(userPrompt, 3)
	memories := strings.Join(relevant, "\n\n")

	system := fmt.Sprintf(
		"You are %s. User = %s. Bio: %s. Personality: %s.\nYour relevant memories:\n%s",
		cfg.AIName, cfg.UserName, cfg.Bio, cfg.Personality, memories,
	)

	if !*useFusion {
		msgs := buildHistory(system, userPrompt)
		answer := queryGPT(modelExec, system, 0.6, 1024, msgs, true)
		if err := appendLog(userPrompt, answer); err != nil {
			log.Printf("append log: %v", err)
		}

		summarizeDayLogs()

		return
	}

	// Fusion path (as-is)
	mem := queryGPT(modelSummarise, "Summarise the dialogue so far.", 0.4, 512, buildHistory(system, userPrompt), false)

	leftMsgs := []Message{{Role: "system", Content: tagMem + mem + tagEnd}, {Role: "user", Content: userPrompt}}

	left := queryGPT(modelLogic, "Answer logically.", 0.2, 512, leftMsgs, false)
	right := queryGPT(modelCreative, "Answer creatively.", 0.9, 512, leftMsgs, false)

	execMsgs := []Message{
		{Role: "system", Content: system},
		{Role: "system", Content: fmt.Sprintf("%s%s%s%s%s%s%s", tagMem, mem, tagLeft, left, tagRight, right, tagEnd)},
		{Role: "user", Content: userPrompt},
	}

	answer := queryGPT(modelExec, "Combine the information inside the tags into one balanced answer.", 0.55, 1024, execMsgs, true)

	if err := appendLog(userPrompt, answer); err != nil {
		log.Printf("append log: %v", err)
	}
}

var promptFilePath string

var summaryFilePath string
var checkInMessage = "Hey there! Just checking in to see how you're doing. Let me know if you need anything!"

var monokai = map[string]string{
	"import":    "\033[32m", // Green
	"package":   "\033[32m", // Green
	"class":     "\033[32m", // Green
	"extends":   "\033[32m", // Green
	"final":     "\033[32m", // Green
	"async":     "\033[32m", // Green
	"await":     "\033[32m", // Green
	"if":        "\033[32m", // Green
	"else":      "\033[32m", // Green
	"throw":     "\033[32m", // Green
	"return":    "\033[32m", // Green
	"String":    "\033[35m", // Purple
	"void":      "\033[35m", // Purple
	"int":       "\033[35m", // Purple
	"bool":      "\033[35m", // Purple
	"const":     "\033[35m", // Purple
	"@override": "\033[36m", // Cyan
	"@required": "\033[36m", // Cyan
	"null":      "\033[31m", // Red
	"true":      "\033[31m", // Red
	"false":     "\033[31m", // Red
	"0":         "\033[31m", // Red
	"200":       "\033[31m", // Red
	"=>":        "\033[30m", // Gray
	"==":        "\033[30m", // Gray
	"!=":        "\033[30m", // Gray
	"<":         "\033[30m", // Gray
	">":         "\033[30m", // Gray
	"(":         "\033[31m", // Red
	")":         "\033[31m", // Red
	"{":         "\033[31m", // Red
	"}":         "\033[31m", // Red
	"[":         "\033[31m", // Red
	"]":         "\033[31m", // Red
	"'":         "\033[31m", // Red
	"\"":        "\033[31m", // Red
	".":         "\033[31m", // Red
	";":         "\033[31m", // Red
	",":         "\033[31m", // Red
}

func init() {
	user, err := user.Current()
	if err != nil {
		log.Fatalf("Error retrieving user info: %v", err)
	}
	homeDir := user.HomeDir
	promptFilePath = filepath.Join(homeDir, ".go-chat-personality")
	logDirPath = filepath.Join(homeDir, ".go-chat-logs")
	stateFilePath = filepath.Join(homeDir, ".go-chat-state")
	configFilePath = filepath.Join(homeDir, ".go-chat-config")
	summaryFilePath = filepath.Join(homeDir, ".go-chat-summary")
	if _, err := os.Stat(logDirPath); os.IsNotExist(err) {
		err = os.Mkdir(logDirPath, 0755)
		if err != nil {
			log.Fatalf("Error creating log directory: %v", err)
		}
	}
}

func getChatHistory() []Message {
	var msgs []Message

	data, err := os.ReadFile(dailyLogPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return msgs // no history yet
		}
		log.Fatalf("read chat log: %v", err)
	}

	var logs []ChatLog
	if err := json.Unmarshal(data, &logs); err != nil {
		log.Fatalf("parse chat log: %v", err)
	}

	for _, l := range logs {
		msgs = append(msgs,
			Message{Role: "user", Content: l.Request},
			Message{Role: "assistant", Content: l.Response},
		)
	}
	return msgs
}

type VectorMemory struct {
	Text      string    `json:"text"`
	Embedding []float32 `json:"embedding"`
}

const vectorStorePath = ".go-chat-memory-vectors.json"

func embedText(text string) ([]float32, error) {
	payload := map[string]any{
		"model": "text-embedding-3-small",
		"input": text,
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", apiURL+"/v1/embeddings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, errors.New("no embeddings returned")
	}
	return out.Data[0].Embedding, nil
}

func saveVectorMemory(text string) {
	vec, err := embedText(text)
	if err != nil {
		log.Printf("embedding error: %v", err)
		return
	}

	p := filepath.Join(homeDir, vectorStorePath)
	var store []VectorMemory

	if data, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(data, &store)
	}

	store = append(store, VectorMemory{Text: text, Embedding: vec})
	data, _ := json.MarshalIndent(store, "", "  ")
	_ = os.WriteFile(p, data, 0644)
}

func cosineSim(a, b []float32) float64 {
	var sum, normA, normB float64
	for i := range a {
		sum += float64(a[i] * b[i])
		normA += float64(a[i] * a[i])
		normB += float64(b[i] * b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return sum / (math.Sqrt(normA) * math.Sqrt(normB))
}

func getRelevantMemories(prompt string, topK int) []string {
	vec, err := embedText(prompt)
	if err != nil {
		return nil
	}

	p := filepath.Join(homeDir, vectorStorePath)
	var store []VectorMemory
	if data, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(data, &store)
	}

	type Scored struct {
		Text  string
		Score float64
	}
	var scored []Scored
	for _, mem := range store {
		score := cosineSim(mem.Embedding, vec)
		scored = append(scored, Scored{Text: mem.Text, Score: score})
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	var top []string
	for i := 0; i < topK && i < len(scored); i++ {
		top = append(top, scored[i].Text)
	}
	return top
}
