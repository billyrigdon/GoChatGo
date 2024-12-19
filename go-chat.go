package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	markdown "github.com/MichaelMure/go-term-markdown"
	"github.com/alecthomas/chroma/formatters"
	"github.com/alecthomas/chroma/lexers"
	"github.com/alecthomas/chroma/styles"
	"github.com/atotto/clipboard"
)

const apiKey = ""
const apiURL = "http://127.0.0.1:5001"

var promptFilePath string
var logDirPath string
var stateFilePath string
var configFilePath string
var summaryFilePath string
var checkInMessage = "Hey there! Just checking in to see how you're doing. Let me know if you need anything!"

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

// Map of Monokai colors using ANSI escape codes
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

// Function to apply Monokai theme colors
func applyMonokaiTheme(text string) string {
	for word, color := range monokai {
		regex := regexp.MustCompile(fmt.Sprintf(`\b%s\b`, escapeRegex(word)))
		text = regex.ReplaceAllString(text, color+word+"\033[0m")
	}
	return text
}

func escapeRegex(s string) string {
	return regexp.QuoteMeta(s)
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

func main() {
	clearLog := flag.Bool("c", false, "Clear the chat log")
	setPersonality := flag.String("p", "", "Set the AI's personality")
	printLog := flag.Bool("a", false, "Print the current day's chat log")
	printLogLines := flag.Int("n", 0, "Print the last n entries from the chat log")
	interactive := flag.Bool("i", false, "Enter interactive mode")
	daemonMode := flag.Bool("d", false, "Run as daemon")
	receiveMessage := flag.Bool("r", false, "Receive new message")
	toggleCheckIn := flag.Bool("t", false, "Toggle check-in feature")
	uploadFile := flag.String("f", "", "Upload a code file to GPT")
	setUserName := flag.String("u", "", "Set your name")
	setAIName := flag.String("ai", "", "Set the AI's name")
	setBio := flag.String("b", "", "Set your bio")
	help := flag.Bool("h", false, "Display help")

	flag.Parse()

	if *help {
		displayHelp()
		return
	}

	if *clearLog {
		clearChatLog()
		return
	}

	if *setPersonality != "" {
		savePersonality(*setPersonality)
		return
	}

	if *setUserName != "" || *setAIName != "" || *setBio != "" {
		updateConfig(*setUserName, *setAIName, *setBio)
		return
	}

	if *printLog {
		printChatLog(*printLogLines)
		return
	}

	if *interactive {
		enterInteractiveMode()
		return
	}

	if *daemonMode {
		runAsDaemon()
		return
	}

	if *receiveMessage {
		displayNewMessage()
		return
	}

	if *toggleCheckIn {
		toggleCheckInFeature()
		return
	}

	if *uploadFile != "" {
		promptUserForInstructions(*uploadFile)
		return
	}

	if len(flag.Args()) > 0 {
		prompt := flag.Arg(0)
		sendChat(prompt)
	} else {
		fmt.Println("No prompt provided. Use -h for help.")
	}
}

func displayHelp() {
	fmt.Println("Usage: go-chat [options] \"prompt goes here\"")
	fmt.Println("Options:")
	fmt.Println("  -c               Clear the chat log")
	fmt.Println("  -p <personality> Set the AI's personality")
	fmt.Println("  -a               Print the current day's chat log")
	fmt.Println("  -n <number>      Print the last n entries from the chat log")
	fmt.Println("  -i               Enter interactive mode")
	fmt.Println("  -d               Run as daemon")
	fmt.Println("  -r               Receive new message")
	fmt.Println("  -t               Toggle check-in feature")
	fmt.Println("  -f <file>        Upload a code file to GPT")
	fmt.Println("  -u <name>        Set your name")
	fmt.Println("  -ai <name>       Set the AI's name")
	fmt.Println("  -b <bio>         Set your bio")
	fmt.Println("  -h               Display help")
}

func savePersonality(personality string) {
	config := getConfig()
	config.Personality = personality
	saveConfig(config)
	fmt.Println("AI personality saved.")
}

func getConfig() Config {
	data, err := ioutil.ReadFile(configFilePath)
	if err != nil {
		return Config{}
	}
	var config Config
	err = json.Unmarshal(data, &config)
	if err != nil {
		log.Fatalf("Error parsing config: %v", err)
	}
	return config
}

func promptUserForInstructions(filePath string) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("Enter what you want done with the file:")
	instructions, _ := reader.ReadString('\n')
	instructions = strings.TrimSpace(instructions)
	sendChatWithFileAndInstructions(filePath, instructions)
}

func saveConfig(config Config) {
	data, err := json.Marshal(config)
	if err != nil {
		log.Fatalf("Error serializing config: %v", err)
	}
	err = ioutil.WriteFile(configFilePath, data, 0644)
	if err != nil {
		log.Fatalf("Error writing config: %v", err)
	}
}

func clearChatLog() {
	err := os.RemoveAll(logDirPath)
	if err != nil {
		log.Fatalf("Error clearing chat log: %v", err)
	}
	err = os.Mkdir(logDirPath, 0755)
	if err != nil {
		log.Fatalf("Error creating log directory: %v", err)
	}
	err = os.Remove(summaryFilePath)
	if err != nil {
		log.Fatalf("Error clearing summary file: %v", err)
	}
	fmt.Println("Chat log cleared.")
}

func printChatLog(lines int) {
	logFiles := getLastTwoDaysLogFiles()
	for _, logFilePath := range logFiles {
		data, err := ioutil.ReadFile(logFilePath)
		if err != nil {
			log.Fatalf("Error reading chat log: %v", err)
		}

		var logs []ChatLog
		if len(data) > 0 {
			err = json.Unmarshal(data, &logs)
			if err != nil {
				log.Fatalf("Error parsing chat log: %v", err)
			}
		}

		if lines > 0 && len(logs) > lines {
			logs = logs[len(logs)-lines:]
		}

		for _, log := range logs {
			fmt.Printf("%s\nRequest: %s\nResponse: %s\n\n", log.Timestamp.Format(time.RFC1123), log.Request, log.Response)
		}
	}
}

func copyToClipboard(text string) error {
	return clipboard.WriteAll(text)
}

func enterInteractiveMode() {
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("Entering interactive mode. Type 'exit' to quit.")

	for {
		fmt.Print("Enter prompt: ")
		prompt, _ := reader.ReadString('\n')
		prompt = strings.TrimSpace(prompt)
		if prompt == "exit" {
			break
		}
		sendChat(prompt)
	}
}

func highlightSyntax(language, code string) string {
	lexer := lexers.Get(language)
	if lexer == nil {
		log.Printf("Lexer not found for language: %s", language)
		lexer = lexers.Fallback
	}
	iterator, err := lexer.Tokenise(nil, code)
	if err != nil {
		log.Printf("Error tokenizing code: %v", err)
		return code // return original code if tokenizing fails
	}

	style := styles.Get("monokai")
	if style == nil {
		log.Printf("Style 'monokai' not found, using fallback")
		style = styles.Fallback
	}

	formatter := formatters.Get("terminal")
	if formatter == nil {
		log.Printf("Formatter 'terminal' not found, using fallback")
		formatter = formatters.Fallback
	}

	var buf bytes.Buffer
	err = formatter.Format(&buf, style, iterator)
	if err != nil {
		log.Printf("Error formatting code: %v", err)
		return code // return original code if formatting fails
	}

	return buf.String()
}

func sendChat(prompt string) {
	config := getConfig()
	chatHistory := truncateChatHistory(getChatHistory(), 1000)

	systemMessage := fmt.Sprintf("You are an AI named %s. The user's name is %s. Here is some information about the user: %s. Your personality is: %s. Avoid repeating phrases or sentences.",
		config.AIName, config.UserName, config.Bio, config.Personality)

	chatHistory = append(chatHistory,
		[]map[string]string{
			{
				"role":    "user",
				"content": prompt,
			},
		}...,
	)

	leftBrainPrompt := "Answer the prompt logically"

	rightBrainPrompt := "Answer the prompt creatively"

	memoryMoodDefensePrompt := "Summarize this conversation and determine its mood"

	execPrompt := "Take both of these answers and combine them into an answer that is both logical and creative"

	memoryMoodDefenseResponse := queryGPT(memoryMoodDefensePrompt, 0.4, 72, chatHistory)

	leftBrainResponse := queryGPT(leftBrainPrompt, 0.2, 512, []map[string]string{
		{
			"role":    "system",
			"content": "Summary of chat so far: " + memoryMoodDefenseResponse,
		},
	})

	rightBrainResponse := queryGPT(rightBrainPrompt, 1.0, 512, []map[string]string{
		{
			"role":    "system",
			"content": "Summary of chat so far: " + memoryMoodDefenseResponse,
		},
	})

	execResponse := queryGPT(execPrompt + prompt, 0.6, 1024, []map[string]string{
		{
			"role":    "system",
			"content": "Summary of chat so far: " + memoryMoodDefenseResponse,
		},
		{
			"role":    "system",
			"content": "This is what you left brain says: \n" + leftBrainResponse,
		},
		{
			"role":    "system",
			"content": "This is what your right brain says: \n" + rightBrainResponse + "",
		},
	},
	)

	displayFormattedResponse(config.AIName, execResponse)

	// Log the chat
	chatLog := ChatLog{
		Timestamp: time.Now(),
		Request:   prompt,
		Response:  execResponse,
	}

	logFilePath := getLogFilePath()
	data, err := ioutil.ReadFile(logFilePath)
	if err != nil && !os.IsNotExist(err) {
		log.Fatalf("Error reading log file: %v", err)
	}

	var logs []ChatLog
	if len(data) > 0 {
		err = json.Unmarshal(data, &logs)
		if err != nil {
			log.Fatalf("Error parsing log file: %v", err)
		}
	}

	logs = append(logs, chatLog)
	data, err = json.Marshal(logs)
	if err != nil {
		log.Fatalf("Error serializing log file: %v", err)
	}

	err = ioutil.WriteFile(logFilePath, data, 0644)
	if err != nil {
		log.Fatalf("Error writing log file: %v", err)
	}

	updateSummary()
	updateState()
}

func queryGPT(prompt string, temperature float64, tokenCount float64, chatHistory []map[string]string) string {
	chatHistory = append(chatHistory, []map[string]string{{"role": "system", "content": prompt}}...)

	requestBody, err := json.Marshal(map[string]interface{}{
		"model":             "gpt-4o",
		"messages":          chatHistory,
		"max_tokens":        tokenCount,
		"temperature":       temperature,
		"frequency_penalty": 1.4,
		"presence_penalty":  1.0,
		"top_p":             0.9,
		"n":                 1,
	})
	if err != nil {
		log.Fatalf("Error creating request body: %v", err)
	}

	req, err := http.NewRequest("POST", apiURL+"/v1/chat/completions", bytes.NewBuffer(requestBody))
	if err != nil {
		log.Fatalf("Error creating request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Error sending request to OpenAI API: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := ioutil.ReadAll(resp.Body)
		log.Fatalf("Non-OK response from API: %s", string(bodyBytes))
	}

	var response map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&response)
	if err != nil {
		log.Fatalf("Error decoding API response: %v", err)
	}

	choices, ok := response["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		log.Fatalf("Unexpected response format or empty response: %v", response)
	}

	messageContent, ok := choices[0].(map[string]interface{})["message"].(map[string]interface{})
	if !ok {
		log.Fatalf("Unexpected message format: %v", response)
	}

	responseText, ok := messageContent["content"].(string)
	if !ok {
		log.Fatalf("Unexpected content format: %v", response)
	}

	return responseText
}

func truncateChatHistory(chatHistory []map[string]string, maxTokens int) []map[string]string {
	totalTokens := 0
	truncatedHistory := []map[string]string{}

	for i := len(chatHistory) - 1; i >= 0; i-- {
		messageTokens := len(strings.Split(chatHistory[i]["content"], " "))
		if totalTokens+messageTokens > maxTokens {
			break
		}
		truncatedHistory = append([]map[string]string{chatHistory[i]}, truncatedHistory...)
		totalTokens += messageTokens
	}

	return truncatedHistory
}

func removeRepeatedSentences(input string) string {
	sentences := strings.Split(input, ". ")
	seen := make(map[string]bool)
	var result []string

	for _, sentence := range sentences {
		trimmed := strings.TrimSpace(sentence)
		if trimmed != "" && !seen[trimmed] {
			result = append(result, trimmed)
			seen[trimmed] = true
		}
	}

	return strings.Join(result, ". ")
}

func getChatHistory() []map[string]string {
	logFiles := getLastTwoDaysLogFiles()
	var logs []ChatLog

	for _, logFilePath := range logFiles {
		data, err := ioutil.ReadFile(logFilePath)
		if err != nil && !os.IsNotExist(err) {
			log.Fatalf("Error reading chat log: %v", err)
		}

		if len(data) > 0 {
			var fileLogs []ChatLog
			err = json.Unmarshal(data, &fileLogs)
			if err != nil {
				log.Fatalf("Error parsing chat log: %v", err)
			}
			logs = append(logs, fileLogs...)
		}
	}

	var chatHistory []map[string]string
	for _, log := range logs {
		chatHistory = append(chatHistory, map[string]string{
			"role":    "user",
			"content": log.Request,
		})
		chatHistory = append(chatHistory, map[string]string{
			"role":    "assistant",
			"content": log.Response,
		})
	}

	return chatHistory
}


func displayFormattedResponse(sender string, message string) {
	message = processAndHighlight(message)
	fmt.Printf("\n%s:\n%s\n", sender, message)
}

func processAndHighlight(text string) string {
	rendered := markdown.Render(text, 120, 2)

	return string(rendered)
}

func showThinkingMessage(done chan bool) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			fmt.Print("\r") // Clear the "thinking..." message
			return
		case <-ticker.C:
			fmt.Print("\rThinking...") // Print the "thinking..." message
		}
	}
}

func sendChatWithFileAndInstructions(filePath, instructions string) {
	content, err := ioutil.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Error reading file: %v", err)
	}

	prompt := fmt.Sprintf("%s\n\n%s", instructions, string(content))
	sendChat(prompt)
}

func runAsDaemon() {
	for {
		checkInUser()
		time.Sleep(30 * time.Minute)
	}
}

func checkInUser() {
	state := getState()
	if !state.CheckInEnabled {
		return
	}

	if time.Since(state.LastInteraction) > 2*time.Hour {
		sendCheckInMessage()
	}
}

func sendCheckInMessage() {
	config := getConfig()
	message := checkInMessage + " " + config.Personality

	chatHistory := getChatHistory()
	chatHistory = append(chatHistory, map[string]string{
		"role":    "user",
		"content": message,
	})

	requestBody, err := json.Marshal(map[string]interface{}{
		"model":    "gpt-4",
		"messages": chatHistory,
	})
	if err != nil {
		log.Fatalf("Error creating request body: %v", err)
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(requestBody))
	if err != nil {
		log.Fatalf("Error creating request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+os.Getenv("OPENAI_API_KEY"))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Error sending request to OpenAI API: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := ioutil.ReadAll(resp.Body)
		log.Fatalf("Non-OK response from API: %s", string(bodyBytes))
	}

	var response map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&response)
	if err != nil {
		log.Fatalf("Error decoding API response: %v", err)
	}

	choices, ok := response["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		log.Fatalf("Unexpected response format or empty response: %v", response)
	}

	messageContent, ok := choices[0].(map[string]interface{})["message"].(map[string]interface{})
	if !ok {
		log.Fatalf("Unexpected message format: %v", response)
	}

	responseText, ok := messageContent["content"].(string)
	if !ok {
		log.Fatalf("Unexpected content format: %v", response)
	}

	sendNotification("Check-in message sent", responseText)
}

func sendNotification(title, message string) {
	cmd := exec.Command("notify-send", title, message)
	err := cmd.Run()
	if err != nil {
		log.Fatalf("Error sending notification: %v", err)
	}
}

func toggleCheckInFeature() {
	state := getState()
	state.CheckInEnabled = !state.CheckInEnabled
	saveState(state)
	fmt.Printf("Check-in feature toggled. Now: %v\n", state.CheckInEnabled)
}

func displayNewMessage() {
	chatHistory := getChatHistory()
	if len(chatHistory) == 0 {
		fmt.Println("No new messages.")
		return
	}

	lastMessage := chatHistory[len(chatHistory)-1]
	if lastMessage["role"] == "assistant" {
		fmt.Printf("New message from assistant: %s\n", lastMessage["content"])
	} else {
		fmt.Println("No new messages.")
	}
}

func getLogFilePath() string {
	date := time.Now().Format("2006-01-02")
	return filepath.Join(logDirPath, "go-chat-log-"+date+".json")
}

func getLastTwoDaysLogFiles() []string {
	var logFiles []string
	for i := 0; i < 2; i++ {
		date := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		logFiles = append(logFiles, filepath.Join(logDirPath, "go-chat-log-"+date+".json"))
	}
	return logFiles
}

func updateSummary() {
	logFiles := getLastTwoDaysLogFiles()
	var logs []ChatLog

	for _, logFilePath := range logFiles {
		data, err := ioutil.ReadFile(logFilePath)
		if err != nil && !os.IsNotExist(err) {
			log.Fatalf("Error reading chat log: %v", err)
		}

		if len(data) > 0 {
			var fileLogs []ChatLog
			err = json.Unmarshal(data, &fileLogs)
			if err != nil {
				log.Fatalf("Error parsing chat log: %v", err)
			}
			logs = append(logs, fileLogs...)
		}
	}

	if len(logs) > 5 {
		summary := summarizeLogs(logs[:len(logs)-4])
		err := ioutil.WriteFile(summaryFilePath, []byte(summary), 0644)
		if err != nil {
			log.Fatalf("Error writing summary file: %v", err)
		}
	}
}

func summarizeLogs(logs []ChatLog) string {
	var summary strings.Builder
	for _, log := range logs {
		summary.WriteString(fmt.Sprintf("- %s: %s\n", log.Timestamp.Format(time.RFC1123), log.Response))
	}
	return summary.String()
}

func updateState() {
	state := getState()
	state.LastInteraction = time.Now()
	saveState(state)
}

func getState() State {
	var state State
	data, err := ioutil.ReadFile(stateFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			state = State{CheckInEnabled: true}
			saveState(state)
			return state
		}
		log.Fatalf("Error reading state file: %v", err)
	}
	err = json.Unmarshal(data, &state)
	if err != nil {
		log.Fatalf("Error parsing state file: %v", err)
	}
	return state
}

func saveState(state State) {
	data, err := json.Marshal(state)
	if err != nil {
		log.Fatalf("Error serializing state file: %v", err)
	}
	err = ioutil.WriteFile(stateFilePath, data, 0644)
	if err != nil {
		log.Fatalf("Error writing state file: %v", err)
	}
}

func updateConfig(userName, aiName, bio string) {
	config := getConfig()
	if userName != "" {
		config.UserName = userName
	}
	if aiName != "" {
		config.AIName = aiName
	}
	if bio != "" {
		config.Bio = bio
	}
	saveConfig(config)
	fmt.Println("Configuration updated.")
}
