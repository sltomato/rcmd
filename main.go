package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	defaultDirPerm  os.FileMode = 0755
	defaultFilePerm os.FileMode = 0644

	ctrlXTimeoutDuration   = 800 * time.Millisecond
	captureTimeoutDuration = 4 * time.Second

	listStartRow    = 1
	commandColWidth = 28
)

// Config & Shortcut Structures

type Shortcut struct {
	Key     string `json:"key"`
	Command string `json:"command"`
}

type Config struct {
	Shortcuts []Shortcut `json:"shortcuts"`
}

func getConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Println("Error getting home directory:", err)
		os.Exit(1)
	}
	return filepath.Join(home, ".config", "rcmd", "config.json")
}

func loadConfig(path string) Config {
	var cfg Config
	file, err := os.ReadFile(path)
	if err != nil {
		return Config{Shortcuts: []Shortcut{}}
	}
	_ = json.Unmarshal(file, &cfg)
	return cfg
}

func saveConfig(path string, cfg Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, defaultDirPerm); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode config: %w", err)
	}
	if err := os.WriteFile(path, data, defaultFilePerm); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	return nil
}

func cloneShortcuts(s []Shortcut) []Shortcut {
	out := make([]Shortcut, len(s))
	copy(out, s)
	return out
}

func formatKeyForDisplay(key string) string {
	if strings.HasPrefix(key, "\\C-x") && len(key) >= 5 {
		return fmt.Sprintf("Ctrl+X %s", strings.ToUpper(key[4:]))
	}
	if strings.HasPrefix(key, "\\C-") && len(key) >= 4 {
		return fmt.Sprintf("Ctrl+%s", strings.ToUpper(key[3:]))
	}
	if strings.HasPrefix(key, "\\e") && len(key) >= 3 {
		return fmt.Sprintf("Alt+%s", strings.ToUpper(key[2:]))
	}
	return key
}

func expandPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// Key Conflict & Detection Tables

var blockedBindKeys = map[string]string{
	"\\C-c": "Ctrl+C sends SIGINT to interrupt the running command. It cannot be reassigned.",
	"\\C-d": "Ctrl+D sends EOF / exits the shell on an empty line. It cannot be reassigned.",
	"\\C-z": "Ctrl+Z sends SIGTSTP to suspend the process. It cannot be reassigned.",
}

var readlineDefaultBindKeys = map[string]string{
	"\\C-a": "move to beginning of line (beginning-of-line)",
	"\\C-e": "move to end of line (end-of-line)",
	"\\C-b": "move cursor left (backward-char)",
	"\\C-f": "move cursor right (forward-char)",
	"\\C-k": "delete to end of line (kill-line)",
	"\\C-u": "delete to beginning of line (unix-line-discard)",
	"\\C-w": "delete word before cursor (unix-word-rubout)",
	"\\C-y": "paste last deleted text (yank)",
	"\\C-l": "clear screen (clear-screen)",
	"\\C-r": "incremental history search (reverse-search-history)",
	"\\C-s": "forward history search (forward-search-history)",
	"\\C-t": "transpose characters at cursor (transpose-chars)",
	"\\C-n": "next history entry (next-history)",
	"\\C-p": "previous history entry (previous-history)",
	"\\C-g": "abort current operation (abort)",
	"\\C-o": "execute current line and show next history entry (operate-and-get-next)",
}

// detectActiveBindKeys runs `bash -i -c "bind -X"` once at startup to get shell shortcuts.
// Returns nil if execution fails, or a map (possibly empty if no bindings exist).
func detectActiveBindKeys() map[string]string {
	cmd := exec.Command("bash", "-i", "-c", "bind -X")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	result := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		idx := strings.Index(line, "\":")
		if !strings.HasPrefix(line, "\"") || idx < 0 {
			continue
		}
		key := line[1:idx]
		action := strings.TrimSpace(line[idx+2:])
		result[key] = action
	}
	return result
}

func checkKeyConflict(bindKey string, active map[string]string) (description string, conflict bool) {
	if active != nil {
		if action, ok := active[bindKey]; ok {
			return fmt.Sprintf("already in use in the current shell (%s)", action), true
		}
		return "", false
	}
	if strings.HasPrefix(bindKey, "\\C-x") && len(bindKey) > len("\\C-x") {
		return "", false
	}
	if desc, ok := readlineDefaultBindKeys[bindKey]; ok {
		return fmt.Sprintf("may conflict with a default readline binding (%s)", desc), true
	}
	return "", false
}

func keyMsgToBindKey(msg tea.KeyMsg) (bindKey string, displayKey string, ok bool) {
	switch msg.Type {
	case tea.KeyCtrlX:
		return "\\C-x", "Ctrl+X", true
	case tea.KeyCtrlA, tea.KeyCtrlB, tea.KeyCtrlC, tea.KeyCtrlD, tea.KeyCtrlE,
		tea.KeyCtrlF, tea.KeyCtrlG, tea.KeyCtrlH, tea.KeyCtrlJ, tea.KeyCtrlK,
		tea.KeyCtrlL, tea.KeyCtrlN, tea.KeyCtrlO, tea.KeyCtrlP, tea.KeyCtrlQ,
		tea.KeyCtrlR, tea.KeyCtrlS, tea.KeyCtrlT, tea.KeyCtrlU, tea.KeyCtrlV,
		tea.KeyCtrlW, tea.KeyCtrlY, tea.KeyCtrlZ:
		s := msg.String()
		parts := strings.SplitN(s, "+", 2)
		if len(parts) == 2 && len(parts[1]) == 1 {
			return fmt.Sprintf("\\C-%s", parts[1]), fmt.Sprintf("Ctrl+%s", strings.ToUpper(parts[1])), true
		}
	case tea.KeyRunes:
		if msg.Alt && len(msg.Runes) == 1 {
			ch := string(msg.Runes[0])
			return fmt.Sprintf("\\e%s", ch), fmt.Sprintf("Alt+%s", strings.ToUpper(ch)), true
		}
	}
	return "", "", false
}

// Import / Export Operations

type importConflict struct {
	existingIndex int
	existing      Shortcut
	imported      Shortcut
}

func parseImportFile(path string) ([]Shortcut, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var shortcuts []Shortcut
	if errArr := json.Unmarshal(data, &shortcuts); errArr == nil {
		return shortcuts, nil
	}
	var cfg Config
	if errCfg := json.Unmarshal(data, &cfg); errCfg != nil {
		return nil, fmt.Errorf("expected a JSON array of shortcuts or a config object: %w", errCfg)
	}
	return cfg.Shortcuts, nil
}

func classifyImport(current []Shortcut, imported []Shortcut) (newOnes []Shortcut, conflicts []importConflict, skippedInvalid int) {
	seenKeys := make(map[string]bool)
	existingByKey := make(map[string]int, len(current))
	for i, s := range current {
		existingByKey[s.Key] = i
	}

	for _, s := range imported {
		key := strings.TrimSpace(s.Key)
		cmd := strings.TrimSpace(s.Command)
		if key == "" || cmd == "" {
			skippedInvalid++
			continue
		}
		if _, blocked := blockedBindKeys[key]; blocked {
			skippedInvalid++
			continue
		}
		if seenKeys[key] {
			skippedInvalid++
			continue
		}
		seenKeys[key] = true
		s.Key, s.Command = key, cmd

		if idx, exists := existingByKey[key]; exists {
			conflicts = append(conflicts, importConflict{existingIndex: idx, existing: current[idx], imported: s})
		} else {
			newOnes = append(newOnes, s)
		}
	}
	return newOnes, conflicts, skippedInvalid
}

// Model Definition & State Management

type mode int

const (
	modeNormal mode = iota
	modeDeleteConfirm
	modeEditing
	modeNewCommandInput
	modeNewKeyCapture
	modeNewKeyPreview
	modeSearch
	modeExportPathInput
	modeImportPathInput
	modeImportStrategy
	modeImportResolve
)

type model struct {
	configPath string
	config     Config
	cursor     int
	mode       mode
	editInput  string
	width      int
	height     int

	newCmdInput  string
	newCmdName   string
	captureError string
	pendingCtrlX bool
	ctrlXWaitSeq int

	captureIdleWaitSeq    int
	ctrlXSeqCounter       int
	captureIdleSeqCounter int

	previewBindKey      string
	previewDisplayKey   string
	previewConflictMsg  string
	previewIsConflict   bool
	previewReplaceIndex int
	previewOldCommand    string

	activeBindKeys map[string]string
	statusMsg      string

	searchQuery     string
	preSearchCursor int

	ioPathInput string
	ioError     string

	importNew            []Shortcut
	importConflicts      []importConflict
	importResolutions    []bool
	importConflictIdx    int
	importSkippedInvalid int
}

func initialModel(configPath string) model {
	return model{
		configPath:          configPath,
		config:              loadConfig(configPath),
		cursor:              0,
		mode:                modeNormal,
		editInput:           "",
		previewReplaceIndex: -1,
	}
}

func (m model) filteredIndices() []int {
	if m.searchQuery == "" {
		idx := make([]int, len(m.config.Shortcuts))
		for i := range idx {
			idx[i] = i
		}
		return idx
	}
	q := strings.ToLower(m.searchQuery)
	var result []int
	for i, s := range m.config.Shortcuts {
		if strings.Contains(strings.ToLower(s.Command), q) ||
			strings.Contains(strings.ToLower(formatKeyForDisplay(s.Key)), q) {
			result = append(result, i)
		}
	}
	return result
}

type activeBindKeysMsg struct {
	keys map[string]string
}

func detectActiveBindKeysCmd() tea.Cmd {
	return func() tea.Msg {
		return activeBindKeysMsg{keys: detectActiveBindKeys()}
	}
}

func (m model) Init() tea.Cmd {
	return detectActiveBindKeysCmd()
}

// UI Styling (Mono-Theme)

var (
	colorFG    = lipgloss.Color("#E8E8E8")
	colorDim   = lipgloss.Color("#7A7A7A")
	colorRevFG = lipgloss.Color("#000000")
	colorRevBG = lipgloss.Color("#F2F2F2")

	titleStyle      = lipgloss.NewStyle().Bold(true).Foreground(colorRevFG).Background(colorRevBG)
	selectedStyle   = lipgloss.NewStyle().Bold(true).Foreground(colorRevFG).Background(colorRevBG)
	normalStyle     = lipgloss.NewStyle().Foreground(colorFG)
	dimStyle        = lipgloss.NewStyle().Foreground(colorDim)
	guideKeyStyle   = lipgloss.NewStyle().Bold(true).Foreground(colorRevFG).Background(colorRevBG)
	guideLabelStyle = lipgloss.NewStyle().Foreground(colorFG)
	alertStyle      = lipgloss.NewStyle().Bold(true).Foreground(colorRevFG).Background(colorRevBG)
)

type guideItem struct {
	key   string
	label string
}

func renderGuide(items []guideItem, width int) string {
	if width <= 0 {
		width = 80
	}
	cellWidth := 24
	cols := width / cellWidth
	if cols < 1 {
		cols = 1
	}

	var rows []string
	var row strings.Builder
	for i, it := range items {
		cell := guideKeyStyle.Render(" "+it.key+" ") + " " + guideLabelStyle.Render(it.label)
		pad := cellWidth - lipgloss.Width(cell)
		if pad > 0 {
			cell += strings.Repeat(" ", pad)
		}
		row.WriteString(cell)
		if (i+1)%cols == 0 {
			rows = append(rows, row.String())
			row.Reset()
		}
	}
	if row.Len() > 0 {
		rows = append(rows, row.String())
	}
	return strings.Join(rows, "\n")
}

func normalModeGuide(width int) string {
	return renderGuide([]guideItem{
		{"Up/Down", "Move"},
		{"^N", "New"},
		{"^W", "Search"},
		{"^E", "Edit"},
		{"^D", "Delete"},
		{"^O", "Export"},
		{"^R", "Import"},
		{"^Q", "Quit"},
	}, width)
}

func searchModeGuide(width int) string {
	return renderGuide([]guideItem{
		{"Up/Down", "Move"},
		{"Enter", "Select"},
		{"Esc", "Cancel"},
	}, width)
}

func previewModeGuide(width int) string {
	return renderGuide([]guideItem{
		{"Enter", "Confirm"},
		{"Any key", "Try Other Key"},
		{"Esc", "Cancel"},
	}, width)
}

func pathInputGuide(width int) string {
	return renderGuide([]guideItem{
		{"Enter", "Confirm"},
		{"Esc", "Cancel"},
	}, width)
}

func importStrategyGuide(width int) string {
	return renderGuide([]guideItem{
		{"S", "Skip Conflicts"},
		{"O", "Overwrite All"},
		{"I", "Decide 1-by-1"},
		{"Esc", "Cancel"},
	}, width)
}

func importResolveGuide(width int) string {
	return renderGuide([]guideItem{
		{"E", "Keep Existing"},
		{"I", "Use Imported"},
		{"Esc", "Cancel All"},
	}, width)
}

// Update Loop & Handlers

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case activeBindKeysMsg:
		m.activeBindKeys = msg.keys
		if msg.keys == nil {
			m.statusMsg = "Note: couldn't inspect the current shell's keybindings (bash -i failed) — Ctrl+X combo conflict checks fall back to built-in defaults only."
		}
		return m, nil

	case tea.KeyMsg:
		switch m.mode {
		case modeNormal:
			return m.updateNormal(msg)
		case modeDeleteConfirm:
			return m.updateDeleteConfirm(msg)
		case modeEditing:
			return m.updateEditing(msg)
		case modeNewCommandInput:
			return m.updateNewCommandInput(msg)
		case modeNewKeyCapture:
			return m.updateNewKeyCapture(msg)
		case modeNewKeyPreview:
			return m.updateNewKeyPreview(msg)
		case modeSearch:
			return m.updateSearch(msg)
		case modeExportPathInput:
			return m.updateExportPathInput(msg)
		case modeImportPathInput:
			return m.updateImportPathInput(msg)
		case modeImportStrategy:
			return m.updateImportStrategy(msg)
		case modeImportResolve:
			return m.updateImportResolve(msg)
		}

	case ctrlXTimeoutMsg:
		if m.mode == modeNewKeyCapture && m.pendingCtrlX && msg.seq == m.ctrlXWaitSeq {
			return m.finishKeyCapture("\\C-x", "Ctrl+X")
		}
		return m, nil

	case captureIdleTimeoutMsg:
		if m.mode == modeNewKeyCapture && msg.seq == m.captureIdleWaitSeq {
			m.captureError = "No key received. Some keys (e.g. Ctrl+V) may be captured by your terminal or SSH client before reaching here — try a different key (Esc to cancel)"
		}
		return m, nil
	}

	return m, nil
}

func (m model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.statusMsg = ""
	switch msg.String() {
	case "ctrl+q", "esc", "q":
		return m, tea.Quit

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.config.Shortcuts)-1 {
			m.cursor++
		}

	case "ctrl+d", "delete", "backspace":
		if len(m.config.Shortcuts) > 0 {
			m.mode = modeDeleteConfirm
		}

	case "ctrl+e", "e":
		if len(m.config.Shortcuts) > 0 {
			m.mode = modeEditing
			m.editInput = m.config.Shortcuts[m.cursor].Command
		}

	case "ctrl+n", "n":
		m.mode = modeNewCommandInput
		m.newCmdInput = ""
		m.captureError = ""

	case "ctrl+w", "/":
		m.mode = modeSearch
		m.preSearchCursor = m.cursor
		m.searchQuery = ""
		m.cursor = 0

	case "ctrl+o":
		home, _ := os.UserHomeDir()
		m.mode = modeExportPathInput
		m.ioPathInput = filepath.Join(home, "rcmd-export.json")
		m.ioError = ""

	case "ctrl+r":
		m.mode = modeImportPathInput
		m.ioPathInput = ""
		m.ioError = ""
	}
	return m, nil
}

func (m model) applyAndSave(newConfig Config) (model, error) {
	prev := m.config
	m.config = newConfig
	if err := saveConfig(m.configPath, m.config); err != nil {
		m.config = prev
		return m, err
	}
	return m, nil
}

func (m model) updateDeleteConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch strings.ToLower(msg.String()) {
	case "y":
		target := m.cursor
		newShortcuts := append(m.config.Shortcuts[:target:target], m.config.Shortcuts[target+1:]...)
		var err error
		m, err = m.applyAndSave(Config{Shortcuts: newShortcuts})
		if err != nil {
			m.statusMsg = fmt.Sprintf("Delete failed: could not save (%v). No changes were made.", err)
		} else {
			m.statusMsg = "Deleted. Run 'source ~/.bashrc' to apply."
			if target >= len(m.config.Shortcuts) && target > 0 {
				m.cursor = target - 1
			}
		}
		m.mode = modeNormal
	case "n", "esc", "q", "enter":
		m.mode = modeNormal
	}
	return m, nil
}

func (m model) updateEditing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		trimmed := strings.TrimSpace(m.editInput)
		if trimmed != "" {
			newShortcuts := cloneShortcuts(m.config.Shortcuts)
			newShortcuts[m.cursor].Command = trimmed
			var err error
			m, err = m.applyAndSave(Config{Shortcuts: newShortcuts})
			if err != nil {
				m.statusMsg = fmt.Sprintf("Edit failed: could not save (%v). No changes were made.", err)
			} else {
				m.statusMsg = "Updated. Run 'source ~/.bashrc' to apply."
			}
		}
		m.mode = modeNormal
	case tea.KeyEsc:
		m.mode = modeNormal
	case tea.KeyBackspace:
		if len(m.editInput) > 0 {
			m.editInput = m.editInput[:len(m.editInput)-1]
		}
	case tea.KeyRunes:
		m.editInput += string(msg.Runes)
	case tea.KeySpace:
		m.editInput += " "
	}
	return m, nil
}

func (m model) updateNewCommandInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		trimmed := strings.TrimSpace(m.newCmdInput)
		if trimmed == "" {
			m.captureError = "Enter a command"
			return m, nil
		}
		for _, s := range m.config.Shortcuts {
			if s.Command == trimmed {
				m.captureError = fmt.Sprintf("Same command already bound to [%s] (you can still continue)", formatKeyForDisplay(s.Key))
				break
			}
		}
		m.newCmdName = trimmed
		m.mode = modeNewKeyCapture
		m.pendingCtrlX = false
		return m, m.startCaptureIdleTimeout()
	case tea.KeyEsc:
		m.mode = modeNormal
		m.newCmdInput = ""
		m.captureError = ""
	case tea.KeyBackspace:
		if len(m.newCmdInput) > 0 {
			m.newCmdInput = m.newCmdInput[:len(m.newCmdInput)-1]
		}
	case tea.KeyRunes:
		m.newCmdInput += string(msg.Runes)
	case tea.KeySpace:
		m.newCmdInput += " "
	}
	return m, nil
}

func (m model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = modeNormal
		m.searchQuery = ""
		m.cursor = m.preSearchCursor
		return m, nil

	case tea.KeyEnter:
		filtered := m.filteredIndices()
		if len(filtered) > 0 {
			if m.cursor >= len(filtered) {
				m.cursor = len(filtered) - 1
			}
			m.cursor = filtered[m.cursor]
		} else {
			m.cursor = m.preSearchCursor
		}
		m.mode = modeNormal
		m.searchQuery = ""
		return m, nil

	case tea.KeyBackspace:
		if len(m.searchQuery) > 0 {
			m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
		}
	case tea.KeyRunes:
		m.searchQuery += string(msg.Runes)
	case tea.KeySpace:
		m.searchQuery += " "
	case tea.KeyUp, tea.KeyCtrlP:
		if m.cursor > 0 {
			m.cursor--
		}
	case tea.KeyDown, tea.KeyCtrlN:
		filtered := m.filteredIndices()
		if m.cursor < len(filtered)-1 {
			m.cursor++
		}
	}

	filtered := m.filteredIndices()
	if len(filtered) == 0 {
		m.cursor = 0
	} else if m.cursor >= len(filtered) {
		m.cursor = len(filtered) - 1
	}
	return m, nil
}

// Export / Import Handlers

func (m model) updateExportPathInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		path := expandPath(m.ioPathInput)
		if path == "" {
			m.ioError = "Enter a file path"
			return m, nil
		}
		data, err := json.MarshalIndent(m.config, "", "  ")
		if err != nil {
			m.ioError = fmt.Sprintf("Failed to encode: %v", err)
			return m, nil
		}
		if dir := filepath.Dir(path); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, defaultDirPerm); err != nil {
				m.ioError = fmt.Sprintf("Failed to create directory: %v", err)
				return m, nil
			}
		}
		if err := os.WriteFile(path, data, defaultFilePerm); err != nil {
			m.ioError = fmt.Sprintf("Failed to write file: %v", err)
			return m, nil
		}
		m.mode = modeNormal
		m.statusMsg = fmt.Sprintf("Exported %d shortcut(s) to %s", len(m.config.Shortcuts), path)
		m.ioPathInput = ""
		m.ioError = ""
	case tea.KeyEsc:
		m.mode = modeNormal
		m.ioPathInput = ""
		m.ioError = ""
	case tea.KeyBackspace:
		if len(m.ioPathInput) > 0 {
			m.ioPathInput = m.ioPathInput[:len(m.ioPathInput)-1]
		}
	case tea.KeyRunes:
		m.ioPathInput += string(msg.Runes)
	case tea.KeySpace:
		m.ioPathInput += " "
	}
	return m, nil
}

func (m model) updateImportPathInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		path := expandPath(m.ioPathInput)
		if path == "" {
			m.ioError = "Enter a file path"
			return m, nil
		}
		loaded, err := parseImportFile(path)
		if err != nil {
			m.ioError = fmt.Sprintf("Failed to read/parse file: %v", err)
			return m, nil
		}

		newOnes, conflicts, skipped := classifyImport(m.config.Shortcuts, loaded)
		m.importNew = newOnes
		m.importConflicts = conflicts
		m.importSkippedInvalid = skipped
		m.ioError = ""
		m.ioPathInput = ""

		if len(newOnes) == 0 && len(conflicts) == 0 {
			m.mode = modeNormal
			m.statusMsg = "No valid shortcuts found to import."
			return m, nil
		}

		if len(conflicts) == 0 {
			return m.commitImport()
		}

		m.importResolutions = make([]bool, len(conflicts))
		m.importConflictIdx = 0
		m.mode = modeImportStrategy
	case tea.KeyEsc:
		m.mode = modeNormal
		m.ioPathInput = ""
		m.ioError = ""
	case tea.KeyBackspace:
		if len(m.ioPathInput) > 0 {
			m.ioPathInput = m.ioPathInput[:len(m.ioPathInput)-1]
		}
	case tea.KeyRunes:
		m.ioPathInput += string(msg.Runes)
	case tea.KeySpace:
		m.ioPathInput += " "
	}
	return m, nil
}

func (m model) updateImportStrategy(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch strings.ToLower(msg.String()) {
	case "s":
		for i := range m.importResolutions {
			m.importResolutions[i] = false
		}
		return m.commitImport()
	case "o":
		for i := range m.importResolutions {
			m.importResolutions[i] = true
		}
		return m.commitImport()
	case "i":
		m.importConflictIdx = 0
		m.mode = modeImportResolve
	case "esc":
		m.mode = modeNormal
		m.importNew = nil
		m.importConflicts = nil
		m.importResolutions = nil
	}
	return m, nil
}

func (m model) updateImportResolve(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.importConflictIdx >= len(m.importConflicts) {
		return m.commitImport()
	}
	switch strings.ToLower(msg.String()) {
	case "e":
		m.importResolutions[m.importConflictIdx] = false
		return m.advanceImportResolve()
	case "i", "y":
		m.importResolutions[m.importConflictIdx] = true
		return m.advanceImportResolve()
	case "esc":
		m.mode = modeNormal
		m.importNew = nil
		m.importConflicts = nil
		m.importResolutions = nil
		m.importConflictIdx = 0
	}
	return m, nil
}

func (m model) advanceImportResolve() (tea.Model, tea.Cmd) {
	m.importConflictIdx++
	if m.importConflictIdx >= len(m.importConflicts) {
		return m.commitImport()
	}
	return m, nil
}

func (m model) commitImport() (tea.Model, tea.Cmd) {
	added := len(m.importNew)
	overwritten := 0

	newShortcuts := cloneShortcuts(m.config.Shortcuts)
	newShortcuts = append(newShortcuts, m.importNew...)
	for i, c := range m.importConflicts {
		if i < len(m.importResolutions) && m.importResolutions[i] {
			newShortcuts[c.existingIndex].Command = c.imported.Command
			overwritten++
		}
	}

	kept := len(m.importConflicts) - overwritten
	skipped := m.importSkippedInvalid

	var err error
	m, err = m.applyAndSave(Config{Shortcuts: newShortcuts})

	m.mode = modeNormal
	m.importNew = nil
	m.importConflicts = nil
	m.importResolutions = nil
	m.importConflictIdx = 0
	m.importSkippedInvalid = 0

	if err != nil {
		m.statusMsg = fmt.Sprintf("Import failed: could not save (%v). No changes were made.", err)
		return m, nil
	}
	m.statusMsg = fmt.Sprintf(
		"Imported: %d added, %d overwritten, %d kept existing, %d skipped (invalid/duplicate). Run 'source ~/.bashrc' to apply.",
		added, overwritten, kept, skipped,
	)
	return m, nil
}

// Timeout Logic for Multi-key Captures

type ctrlXTimeoutMsg struct{ seq int }
type captureIdleTimeoutMsg struct{ seq int }

func (m *model) startCtrlXTimeout() tea.Cmd {
	m.ctrlXSeqCounter++
	seq := m.ctrlXSeqCounter
	m.ctrlXWaitSeq = seq
	return tea.Tick(ctrlXTimeoutDuration, func(time.Time) tea.Msg {
		return ctrlXTimeoutMsg{seq: seq}
	})
}

func (m *model) startCaptureIdleTimeout() tea.Cmd {
	m.captureIdleSeqCounter++
	seq := m.captureIdleSeqCounter
	m.captureIdleWaitSeq = seq
	return tea.Tick(captureTimeoutDuration, func(time.Time) tea.Msg {
		return captureIdleTimeoutMsg{seq: seq}
	})
}

func (m model) updateNewKeyCapture(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.captureIdleSeqCounter++
	m.captureIdleWaitSeq = m.captureIdleSeqCounter

	if msg.Type == tea.KeyEsc {
		m.mode = modeNormal
		m.pendingCtrlX = false
		m.captureError = ""
		return m, nil
	}

	if m.pendingCtrlX {
		var ch string
		switch msg.Type {
		case tea.KeyRunes:
			ch = string(msg.Runes)
		case tea.KeySpace:
			ch = " "
		default:
			return m.finishKeyCapture("\\C-x", "Ctrl+X")
		}
		bindKey := fmt.Sprintf("\\C-x%s", ch)
		displayKey := fmt.Sprintf("Ctrl+X %s", strings.ToUpper(ch))
		return m.finishKeyCapture(bindKey, displayKey)
	}

	if msg.Type == tea.KeyCtrlX {
		m.pendingCtrlX = true
		m.captureError = "Ctrl+X detected... press a second key to complete the combo (or wait to confirm Ctrl+X alone)"
		return m, m.startCtrlXTimeout()
	}

	bindKey, displayKey, ok := keyMsgToBindKey(msg)
	if !ok {
		m.captureError = "That key isn't a Ctrl/Alt combo (or your terminal/SSH client caught it, e.g. Ctrl+V) — press a different key (Esc to cancel)"
		return m, nil
	}
	return m.finishKeyCapture(bindKey, displayKey)
}

func (m model) finishKeyCapture(bindKey, displayKey string) (tea.Model, tea.Cmd) {
	m.pendingCtrlX = false

	if reason, blocked := blockedBindKeys[bindKey]; blocked {
		m.captureError = reason + " Press a different key (Esc to cancel)"
		return m, nil
	}

	replaceIndex := -1
	oldCommand := ""
	for i, s := range m.config.Shortcuts {
		if s.Key == bindKey {
			replaceIndex = i
			oldCommand = s.Command
			break
		}
	}

	conflictMsg, isConflict := checkKeyConflict(bindKey, m.activeBindKeys)

	m.previewBindKey = bindKey
	m.previewDisplayKey = displayKey
	m.previewConflictMsg = conflictMsg
	m.previewIsConflict = isConflict
	m.previewReplaceIndex = replaceIndex
	m.previewOldCommand = oldCommand
	m.captureError = ""
	m.mode = modeNewKeyPreview
	return m, nil
}

func (m model) updateNewKeyPreview(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		return m.commitNewShortcut()
	case tea.KeyEsc:
		m.mode = modeNormal
		m.newCmdInput = ""
		m.newCmdName = ""
		m.previewBindKey = ""
		m.previewDisplayKey = ""
		m.previewConflictMsg = ""
		m.previewIsConflict = false
		m.previewReplaceIndex = -1
		m.previewOldCommand = ""
		m.captureError = ""
		return m, nil
	default:
		m.previewBindKey = ""
		m.previewDisplayKey = ""
		m.previewConflictMsg = ""
		m.previewIsConflict = false
		m.previewReplaceIndex = -1
		m.previewOldCommand = ""
		m.mode = modeNewKeyCapture
		return m.updateNewKeyCapture(msg)
	}
}

func (m model) commitNewShortcut() (tea.Model, tea.Cmd) {
	displayKey := m.previewDisplayKey
	cmdName := m.newCmdName
	replaceIndex := m.previewReplaceIndex

	newShortcuts := cloneShortcuts(m.config.Shortcuts)
	if replaceIndex >= 0 && replaceIndex < len(newShortcuts) {
		newShortcuts[replaceIndex].Command = cmdName
	} else {
		newShortcuts = append(newShortcuts, Shortcut{Key: m.previewBindKey, Command: cmdName})
	}

	var err error
	m, err = m.applyAndSave(Config{Shortcuts: newShortcuts})

	m.mode = modeNormal
	m.pendingCtrlX = false
	m.captureError = ""
	m.newCmdInput = ""
	m.newCmdName = ""
	m.previewBindKey = ""
	m.previewDisplayKey = ""
	m.previewConflictMsg = ""
	m.previewIsConflict = false
	m.previewReplaceIndex = -1
	m.previewOldCommand = ""

	if err != nil {
		m.statusMsg = fmt.Sprintf("Registration failed: could not save (%v). No changes were made.", err)
		return m, nil
	}

	if replaceIndex >= 0 && replaceIndex < len(m.config.Shortcuts) {
		m.cursor = replaceIndex
	} else {
		m.cursor = len(m.config.Shortcuts) - 1
	}
	m.statusMsg = fmt.Sprintf("Registered: [%s] => %s   Run 'source ~/.bashrc' to apply.", displayKey, cmdName)
	return m, nil
}

// Mouse Event Handling

func (m model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.MouseWheelUp:
		if m.mode == modeNormal && m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case tea.MouseWheelDown:
		if m.mode == modeNormal && m.cursor < len(m.config.Shortcuts)-1 {
			m.cursor++
		}
		return m, nil
	case tea.MouseLeft:
		if m.mode != modeNormal {
			return m, nil
		}
		row := msg.Y - listStartRow
		if row >= 0 && row < len(m.config.Shortcuts) {
			if row == m.cursor {
				return m.handleRowActionClick(msg)
			}
			m.cursor = row
		}
		return m, nil
	}
	return m, nil
}

func (m model) handleRowActionClick(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	line := m.rowPlainText(m.cursor)
	editBtnStart := lipgloss.Width(line) + 2
	editBtnEnd := editBtnStart + lipgloss.Width("[Edit]")
	delBtnStart := editBtnEnd + 1
	delBtnEnd := delBtnStart + lipgloss.Width("[Delete]")

	col := msg.X
	if col >= editBtnStart && col < editBtnEnd {
		m.mode = modeEditing
		m.editInput = m.config.Shortcuts[m.cursor].Command
	} else if col >= delBtnStart && col < delBtnEnd {
		m.mode = modeDeleteConfirm
	}
	return m, nil
}

func truncateForDisplay(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s + strings.Repeat(" ", width-lipgloss.Width(s))
	}
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if w+rw > width-1 {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	out := b.String() + "…"
	if pad := width - lipgloss.Width(out); pad > 0 {
		out += strings.Repeat(" ", pad)
	}
	return out
}

func (m model) rowPlainText(i int) string {
	s := m.config.Shortcuts[i]
	readableKey := formatKeyForDisplay(s.Key)
	cmd := truncateForDisplay(s.Command, commandColWidth)
	return fmt.Sprintf(" [%2d] %-12s => %s", i+1, readableKey, cmd)
}

// View Rendering

func (m model) View() string {
	header := titleStyle.Render(padTo(" rcmd - Shortcut Manager", m.width))

	var list strings.Builder
	if len(m.config.Shortcuts) == 0 {
		list.WriteString(dimStyle.Render("  No shortcuts registered yet. Press ^N to add one, or ^R to import.") + "\n")
	} else {
		filtered := m.filteredIndices()
		isFiltering := m.mode == modeSearch && m.searchQuery != ""

		inFiltered := make(map[int]int)
		if isFiltering {
			for pos, realIdx := range filtered {
				inFiltered[realIdx] = pos
			}
		}

		for i := range m.config.Shortcuts {
			line := m.rowPlainText(i)

			switch {
			case isFiltering:
				pos, matched := inFiltered[i]
				if !matched {
					list.WriteString(dimStyle.Render(line) + "\n")
				} else if pos == m.cursor {
					list.WriteString(selectedStyle.Render(line) + "\n")
				} else {
					list.WriteString(normalStyle.Render(line) + "\n")
				}
			case i == m.cursor && m.mode == modeNormal:
				list.WriteString(selectedStyle.Render(line))
				list.WriteString("  " + dimStyle.Render("[Edit]") + " " + dimStyle.Render("[Delete]"))
				list.WriteString("\n")
			case i == m.cursor:
				list.WriteString(selectedStyle.Render(line) + "\n")
			default:
				list.WriteString(normalStyle.Render(line) + "\n")
			}
		}
	}

	prompt := m.renderPromptLine()

	var footer string
	switch m.mode {
	case modeSearch:
		footer = searchModeGuide(m.width)
	case modeNewKeyPreview:
		footer = previewModeGuide(m.width)
	case modeExportPathInput, modeImportPathInput:
		footer = pathInputGuide(m.width)
	case modeImportStrategy:
		footer = importStrategyGuide(m.width)
	case modeImportResolve:
		footer = importResolveGuide(m.width)
	default:
		footer = normalModeGuide(m.width)
	}

	headerLines := lipgloss.Height(header)
	listLines := lipgloss.Height(list.String())
	promptLines := 0
	if prompt != "" {
		promptLines = lipgloss.Height(prompt)
	}
	footerLines := lipgloss.Height(footer)

	if m.height <= 0 {
		out := header + "\n" + list.String()
		if prompt != "" {
			out += prompt + "\n"
		}
		return out + footer
	}

	fillLines := m.height - headerLines - listLines - promptLines - footerLines
	if fillLines < 0 {
		fillLines = 0
	}

	var out strings.Builder
	out.WriteString(header)
	out.WriteString("\n")
	out.WriteString(list.String())
	if fillLines > 0 {
		out.WriteString(strings.Repeat("\n", fillLines))
	}
	if prompt != "" {
		out.WriteString(prompt)
		out.WriteString("\n")
	}
	out.WriteString(footer)

	return out.String()
}

func (m model) renderPromptLine() string {
	switch m.mode {
	case modeDeleteConfirm:
		target := m.config.Shortcuts[m.cursor]
		p := fmt.Sprintf(" Delete [%s] (%s)? [y/N]", formatKeyForDisplay(target.Key), target.Command)
		return alertStyle.Render(padTo(p, m.width))

	case modeEditing:
		return fmt.Sprintf(" Edit command: %s█", m.editInput)

	case modeNewCommandInput:
		line := fmt.Sprintf(" New command: %s█", m.newCmdInput)
		if m.captureError != "" {
			line += "   " + dimStyle.Render(m.captureError)
		}
		return line

	case modeNewKeyCapture:
		line := fmt.Sprintf(" Press a key to bind to \"%s\"", m.newCmdName) + "   " + dimStyle.Render("(some keys, e.g. Ctrl+V, may be caught by your terminal first)")
		if m.captureError != "" {
			line = alertStyle.Render(padTo(" "+m.captureError, m.width))
		}
		return line

	case modeNewKeyPreview:
		switch {
		case m.previewReplaceIndex >= 0:
			p := fmt.Sprintf(" [%s] is already bound to \"%s\" — Enter to REPLACE with \"%s\" / Esc=cancel / other key=try different binding",
				m.previewDisplayKey, m.previewOldCommand, m.newCmdName)
			return alertStyle.Render(padTo(p, m.width))
		case m.previewIsConflict:
			p := fmt.Sprintf(" [%s] %s  (Enter to continue / Esc to cancel / any key to try a different binding)", m.previewDisplayKey, m.previewConflictMsg)
			return alertStyle.Render(padTo(p, m.width))
		default:
			return fmt.Sprintf(" Bind [%s] to \"%s\"? Enter=confirm / Esc=cancel / any key=try different binding", m.previewDisplayKey, m.newCmdName)
		}

	case modeSearch:
		return fmt.Sprintf(" Search: %s█", m.searchQuery)

	case modeExportPathInput:
		line := fmt.Sprintf(" Export to: %s█", m.ioPathInput)
		if m.ioError != "" {
			line = alertStyle.Render(padTo(" "+m.ioError, m.width))
		}
		return line

	case modeImportPathInput:
		line := fmt.Sprintf(" Import from: %s█", m.ioPathInput)
		if m.ioError != "" {
			line = alertStyle.Render(padTo(" "+m.ioError, m.width))
		}
		return line

	case modeImportStrategy:
		p := fmt.Sprintf(" %d new, %d conflicting key(s) found. [S]kip conflicts (keep existing) / [O]verwrite all / [I]ndividually / Esc=cancel",
			len(m.importNew), len(m.importConflicts))
		return alertStyle.Render(padTo(p, m.width))

	case modeImportResolve:
		if m.importConflictIdx < len(m.importConflicts) {
			c := m.importConflicts[m.importConflictIdx]
			p := fmt.Sprintf(" [%d/%d] Key [%s]: existing=\"%s\"  imported=\"%s\"   [E]xisting / [I]mported / Esc=cancel all",
				m.importConflictIdx+1, len(m.importConflicts), formatKeyForDisplay(c.existing.Key), c.existing.Command, c.imported.Command)
			return alertStyle.Render(padTo(p, m.width))
		}
		return ""

	default:
		if m.statusMsg != "" {
			return dimStyle.Render(" " + m.statusMsg)
		}
		return ""
	}
}

func padTo(s string, width int) string {
	if width <= 0 {
		return s
	}
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

// CLI Entrypoint

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage:")
		fmt.Println("  rcmd ls     - Launch the shortcut manager (view, add, edit, delete, search, import, export)")
		fmt.Println("  rcmd init   - Auto-configure ~/.bashrc")
		return
	}

	subCmd := os.Args[1]
	configPath := getConfigPath()
	cfg := loadConfig(configPath)

	switch subCmd {
	case "init":
		home, _ := os.UserHomeDir()
		bashrcPath := filepath.Join(home, ".bashrc")

		content, err := os.ReadFile(bashrcPath)
		if err != nil && !os.IsNotExist(err) {
			fmt.Println("Error reading ~/.bashrc:", err)
			return
		}

		const marker = "# >>> rcmd auto setup >>>"

		if strings.Contains(string(content), marker) {
			fmt.Println("~/.bashrc is already configured.")
			return
		}

		snippet := marker + `
eval "$(rcmd export)"
rcmd() {
    command rcmd "$@"
    local status=$?
    case "$1" in
        ls)
            source ~/.bashrc
            ;;
    esac
    return $status
}
# <<< rcmd auto setup <<<
`

		f, err := os.OpenFile(bashrcPath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, defaultFilePerm)
		if err != nil {
			fmt.Println("Error opening ~/.bashrc:", err)
			return
		}
		defer f.Close()

		f.WriteString("\n" + snippet)
		fmt.Println("Added rcmd auto-reload setup to ~/.bashrc")
		fmt.Println("   Run this once now to apply it to the current shell: source ~/.bashrc")
		fmt.Println("   After that, changes from rcmd ls apply automatically.")

	case "ls":
		p := tea.NewProgram(
			initialModel(configPath),
			tea.WithAltScreen(),
			tea.WithMouseCellMotion(),
		)
		if _, err := p.Run(); err != nil {
			fmt.Printf("TUI error: %v\n", err)
			os.Exit(1)
		}

	case "export":
		// Called internally by `eval "$(rcmd export)"` in .bashrc to bind shortcuts via readline.
		for _, s := range cfg.Shortcuts {
			cmdEscaped := strings.NewReplacer(
				"\\", "\\\\",
				"\"", "\\\"",
				"$", "\\$",
				"`", "\\`",
			).Replace(s.Command)
			insertedLen := utf8.RuneCountInString(s.Command)
			fmt.Printf(
				"bind -x '\"%s\": READLINE_LINE=\"${READLINE_LINE:0:READLINE_POINT}%s${READLINE_LINE:READLINE_POINT}\"; READLINE_POINT=$((READLINE_POINT+%d))'\n",
				s.Key, cmdEscaped, insertedLen,
			)
		}

	default:
		fmt.Printf("Unknown command: %s\n", subCmd)
	}
}
