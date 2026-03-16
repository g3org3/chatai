package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wordwrap"
)

type appState int

const (
	stateHost appState = iota
	stateAPIKey
	stateModel
	stateChat
	stateChangeModel
	stateAddMCP
)

type chatMessage struct {
	Role       string
	Content    string
	ToolCalls  []toolCall
	ToolCallID string
	Hidden     bool
}

type apiResponseMsg struct {
	content     string
	err         error
	newMessages []chatMessage
}

type mcpConnectedMsg struct {
	server *mcpServer
	err    error
}

type model struct {
	state      appState
	width      int
	height     int
	host       string
	apiKey     string
	modelName  string
	input      textinput.Model
	viewport   viewport.Model
	messages   []chatMessage
	waiting    bool
	mcpServers []*mcpServer
	toolMap    map[string]*mcpServer
}

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205"))

	hintStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	userLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("10"))

	assistantLabelStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("12"))

	errorLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("9"))

	headerStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("236")).
			Foreground(lipgloss.Color("229")).
			Padding(0, 1)

	inputBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("62")).
				Padding(0, 1)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	waitingStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")).
			Italic(true)

	emptyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Italic(true)

	systemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")).
			Italic(true)
)

func newModel() model {
	ti := textinput.New()
	ti.Placeholder = "http://localhost:8080/v1"
	ti.Focus()
	ti.CharLimit = 512

	vp := viewport.New(0, 0)

	return model{
		state:    stateHost,
		input:    ti,
		viewport: vp,
		toolMap:  make(map[string]*mcpServer),
	}
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			if m.state == stateChangeModel || m.state == stateAddMCP {
				m.state = stateChat
				m.input.Reset()
				m.input.Placeholder = "Type your message..."
				return m, nil
			}
		case "enter":
			return m.handleEnter()
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		switch m.state {
		case stateChat, stateChangeModel, stateAddMCP:
			m.resizeChat()
		}
		return m, nil

	case apiResponseMsg:
		m.waiting = false
		if msg.err != nil {
			m.messages = append(m.messages, chatMessage{Role: "error", Content: msg.err.Error()})
		} else {
			m.messages = append(m.messages, msg.newMessages...)
			m.messages = append(m.messages, chatMessage{Role: "assistant", Content: msg.content})
		}
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, nil

	case mcpConnectedMsg:
		if msg.err != nil {
			m.messages = append(m.messages, chatMessage{Role: "error", Content: fmt.Sprintf("MCP: %s", msg.err.Error())})
		} else {
			m.mcpServers = append(m.mcpServers, msg.server)
			for _, t := range msg.server.Tools {
				m.toolMap[t.Name] = msg.server
			}
			var info string
			if len(msg.server.Tools) == 0 {
				info = fmt.Sprintf("Connected to %s (no tools)", msg.server.Name)
			} else {
				names := make([]string, len(msg.server.Tools))
				for i, t := range msg.server.Tools {
					names[i] = t.Name
				}
				info = fmt.Sprintf("Connected to %s (%d tools: %s)", msg.server.Name, len(msg.server.Tools), strings.Join(names, ", "))
			}
			m.messages = append(m.messages, chatMessage{Role: "system", Content: info})
		}
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, nil
	}

	switch m.state {
	case stateChat, stateChangeModel, stateAddMCP:
		return m.updateChat(msg)
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
}

func (m model) handleEnter() (tea.Model, tea.Cmd) {
	value := strings.TrimSpace(m.input.Value())

	switch m.state {
	case stateHost:
		if value == "" {
			value = "http://localhost:8080/v1"
		}
		m.host = strings.TrimRight(value, "/")
		m.state = stateAPIKey
		m.input.Reset()
		m.input.Placeholder = "sk-... (press Enter to skip)"
		return m, nil

	case stateAPIKey:
		m.apiKey = value
		m.state = stateModel
		m.input.Reset()
		m.input.Placeholder = "gpt-4o-mini"
		return m, nil

	case stateModel:
		if value == "" {
			value = "gpt-4o-mini"
		}
		m.modelName = value
		m.state = stateChat
		m.input.Reset()
		m.input.Placeholder = "Type your message..."
		m.input.CharLimit = 0
		m.resizeChat()
		return m, nil

	case stateChat:
		if value == "" || m.waiting {
			return m, nil
		}
		if value == "/model" {
			m.state = stateChangeModel
			m.input.Reset()
			m.input.Placeholder = fmt.Sprintf("Current: %s — enter new model name", m.modelName)
			return m, nil
		}
		if value == "/mcp" {
			m.state = stateAddMCP
			m.input.Reset()
			m.input.Placeholder = "http://localhost:3030/mcp"
			return m, nil
		}
		m.messages = append(m.messages, chatMessage{Role: "user", Content: value})
		m.input.Reset()
		m.waiting = true
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, m.sendRequest()

	case stateChangeModel:
		if value == "" {
			m.state = stateChat
			m.input.Reset()
			m.input.Placeholder = "Type your message..."
			return m, nil
		}
		old := m.modelName
		m.modelName = value
		m.messages = append(m.messages, chatMessage{
			Role:    "system",
			Content: fmt.Sprintf("Model changed: %s -> %s", old, m.modelName),
		})
		m.state = stateChat
		m.input.Reset()
		m.input.Placeholder = "Type your message..."
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, nil

	case stateAddMCP:
		if value == "" {
			value = "http://localhost:3030/mcp"
		}
		url := value
		m.state = stateChat
		m.input.Reset()
		m.input.Placeholder = "Type your message..."
		m.messages = append(m.messages, chatMessage{
			Role:    "system",
			Content: fmt.Sprintf("Connecting to %s...", url),
		})
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, func() tea.Msg {
			server, err := mcpConnect(url)
			return mcpConnectedMsg{server: server, err: err}
		}
	}

	return m, nil
}

func (m model) updateChat(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "pgup", "pgdown", "ctrl+u", "ctrl+d":
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
	case tea.MouseMsg:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *model) resizeChat() {
	vpHeight := m.height - 5
	if vpHeight < 1 {
		vpHeight = 1
	}
	m.viewport.Width = m.width
	m.viewport.Height = vpHeight
	m.input.Width = m.width - 4
	m.viewport.SetContent(m.renderMessages())
}

func (m model) renderMessages() string {
	if len(m.messages) == 0 {
		return emptyStyle.Render("  No messages yet. Start typing to chat!")
	}

	wrapWidth := m.viewport.Width - 4
	if wrapWidth < 20 {
		wrapWidth = 20
	}

	var sb strings.Builder
	first := true
	for _, msg := range m.messages {
		if msg.Hidden {
			continue
		}
		if !first {
			sb.WriteString("\n")
		}
		first = false

		var label string
		switch msg.Role {
		case "user":
			label = userLabelStyle.Render("You: ")
		case "assistant":
			label = assistantLabelStyle.Render("Assistant: ")
		case "error":
			label = errorLabelStyle.Render("Error: ")
		case "system":
			label = systemStyle.Render(">> ")
		}

		wrapped := wordwrap.String(msg.Content, wrapWidth)
		lines := strings.Split(wrapped, "\n")
		sb.WriteString(label + lines[0] + "\n")
		indent := strings.Repeat(" ", lipgloss.Width(label))
		for _, line := range lines[1:] {
			sb.WriteString(indent + line + "\n")
		}
	}

	if m.waiting {
		sb.WriteString("\n")
		sb.WriteString(waitingStyle.Render("  Thinking..."))
		sb.WriteString("\n")
	}

	return sb.String()
}

func (m model) buildAPIMessages() []apiMessage {
	var msgs []apiMessage
	for _, msg := range m.messages {
		switch msg.Role {
		case "user":
			msgs = append(msgs, apiMessage{Role: "user", Content: strPtr(msg.Content)})
		case "assistant":
			if len(msg.ToolCalls) > 0 {
				msgs = append(msgs, apiMessage{Role: "assistant", ToolCalls: msg.ToolCalls})
			} else {
				msgs = append(msgs, apiMessage{Role: "assistant", Content: strPtr(msg.Content)})
			}
		case "tool":
			msgs = append(msgs, apiMessage{Role: "tool", Content: strPtr(msg.Content), ToolCallID: msg.ToolCallID})
		}
	}
	return msgs
}

func (m model) buildToolDefs() []toolDef {
	var tools []toolDef
	for _, server := range m.mcpServers {
		for _, t := range server.Tools {
			tools = append(tools, toolDef{
				Type: "function",
				Function: toolFunction{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.InputSchema,
				},
			})
		}
	}
	return tools
}

func (m model) sendRequest() tea.Cmd {
	apiMsgs := m.buildAPIMessages()
	tools := m.buildToolDefs()
	host, key, modelName := m.host, m.apiKey, m.modelName

	toolMap := make(map[string]*mcpServer)
	for k, v := range m.toolMap {
		toolMap[k] = v
	}

	return func() tea.Msg {
		return doToolLoop(host, key, modelName, apiMsgs, tools, toolMap)
	}
}

func doToolLoop(host, key, modelName string, msgs []apiMessage, tools []toolDef, toolMap map[string]*mcpServer) apiResponseMsg {
	var newMessages []chatMessage
	maxRounds := 10

	for i := 0; i < maxRounds; i++ {
		resp, err := chatCompletion(host, key, modelName, msgs, tools)
		if err != nil {
			return apiResponseMsg{err: err, newMessages: newMessages}
		}

		choice := resp.Choices[0]
		if len(choice.Message.ToolCalls) == 0 {
			return apiResponseMsg{content: choice.Message.Content, newMessages: newMessages}
		}

		newMessages = append(newMessages, chatMessage{
			Role:      "assistant",
			ToolCalls: choice.Message.ToolCalls,
			Hidden:    true,
		})
		msgs = append(msgs, apiMessage{
			Role:      "assistant",
			ToolCalls: choice.Message.ToolCalls,
		})

		for _, tc := range choice.Message.ToolCalls {
			newMessages = append(newMessages, chatMessage{
				Role:    "system",
				Content: fmt.Sprintf("Calling tool: %s", tc.Function.Name),
			})

			var result string
			server, ok := toolMap[tc.Function.Name]
			if !ok {
				result = fmt.Sprintf("error: tool %q not found", tc.Function.Name)
			} else {
				args := json.RawMessage(tc.Function.Arguments)
				if len(args) == 0 {
					args = json.RawMessage(`{}`)
				}
				r, callErr := mcpCallTool(server, tc.Function.Name, args)
				if callErr != nil {
					result = fmt.Sprintf("error: %s", callErr.Error())
				} else {
					result = r
				}
			}

			newMessages = append(newMessages, chatMessage{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
				Hidden:     true,
			})
			msgs = append(msgs, apiMessage{
				Role:       "tool",
				Content:    strPtr(result),
				ToolCallID: tc.ID,
			})
		}
	}

	return apiResponseMsg{
		err:         fmt.Errorf("tool calling exceeded %d rounds", maxRounds),
		newMessages: newMessages,
	}
}

func (m model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	switch m.state {
	case stateHost, stateAPIKey, stateModel:
		return m.setupView()
	case stateChat, stateChangeModel, stateAddMCP:
		return m.chatView()
	}
	return ""
}

func (m model) setupView() string {
	var title, hint string
	switch m.state {
	case stateHost:
		title = "API Host"
		hint = "Enter for default: http://localhost:8080/v1"
	case stateAPIKey:
		title = "API Key"
		hint = "Enter to skip (for local servers)"
	case stateModel:
		title = "Model"
		hint = "Enter for default: gpt-4o-mini"
	}

	content := lipgloss.JoinVertical(lipgloss.Center,
		titleStyle.Render(title),
		"",
		m.input.View(),
		"",
		hintStyle.Render(hint),
	)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
}

func (m model) chatView() string {
	header := headerStyle.Width(m.width).Render(
		fmt.Sprintf(" %s @ %s", m.modelName, m.host),
	)

	inputBox := inputBorderStyle.Width(m.width - 2).Render(m.input.View())

	var help string
	switch m.state {
	case stateChangeModel:
		help = helpStyle.Render(" enter: confirm | esc: cancel")
	case stateAddMCP:
		help = helpStyle.Render(" enter: connect | esc: cancel")
	default:
		help = helpStyle.Render(" enter: send | /model /mcp | pgup/pgdn: scroll | ctrl+c: quit")
	}

	return header + "\n" + m.viewport.View() + "\n" + inputBox + "\n" + help
}

func main() {
	p := tea.NewProgram(
		newModel(),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
