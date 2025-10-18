package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/filepicker"
	tea "github.com/charmbracelet/bubbletea"
	"go-bittorrent/internal/bencoding"
	"go-bittorrent/internal/config"
	"go-bittorrent/internal/daemon"
)

// viewMode represents the current view state
type viewMode int

const (
	viewSessions viewMode = iota
	viewFilePicker
)

// Model holds the state for the Bubbletea TUI
type Model struct {
	manager      *daemon.TorrentManager
	config       *config.Config
	quitting     bool
	lastUpdate   time.Time
	mode         viewMode
	cursor       int // For selecting sessions or menu items
	filePicker   filepicker.Model
	err          error
	selectedFile string
	width        int
	height       int
	logLines     []string // Recent log lines to display
	logFile      *os.File // Log file handle for reading
}

// TickMsg is sent periodically to update the UI
type TickMsg time.Time

// NewModel creates a new TUI model
func NewModel(manager *daemon.TorrentManager, cfg *config.Config, logFilePath string) Model {
	fp := filepicker.New()
	fp.AllowedTypes = []string{".torrent"}
	fp.CurrentDirectory, _ = os.UserHomeDir()

	// Configure filepicker for better visibility
	fp.ShowHidden = false
	fp.DirAllowed = true
	fp.FileAllowed = true
	fp.ShowPermissions = false
	fp.ShowSize = true
	fp.Height = 20

	// Open log file for reading
	logFile, _ := os.Open(logFilePath)

	return Model{
		manager:    manager,
		config:     cfg,
		quitting:   false,
		lastUpdate: time.Now(),
		mode:       viewSessions,
		cursor:     -1, // -1 means "New" is selected
		filePicker: fp,
		logLines:   make([]string, 0),
		logFile:    logFile,
	}
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	return tea.Batch(tick(), m.filePicker.Init())
}

// Update handles messages and updates the model
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.filePicker.Height = msg.Height - 10 // Leave room for header/footer

		// Also pass window size to filepicker
		if m.mode == viewFilePicker {
			var cmd tea.Cmd
			m.filePicker, cmd = m.filePicker.Update(msg)
			return m, cmd
		}

	case tea.KeyMsg:
		switch m.mode {
		case viewFilePicker:
			switch msg.String() {
			case "ctrl+c", "q":
				m.quitting = true
				return m, tea.Quit
			case "esc":
				m.mode = viewSessions
				return m, nil
			}
			// Pass other keys to the filepicker
			var cmd tea.Cmd
			m.filePicker, cmd = m.filePicker.Update(msg)

			// Check if a file was selected
			if didSelect, path := m.filePicker.DidSelectFile(msg); didSelect {
				m.selectedFile = path
				// Load and start the torrent
				if err := m.startTorrentFromFile(path); err != nil {
					m.err = err
				}
				m.mode = viewSessions
				return m, cmd
			}
			return m, cmd

		case viewSessions:
			switch msg.String() {
			case "q", "ctrl+c":
				m.quitting = true
				return m, tea.Quit
			case "up", "k":
				if m.cursor > -1 {
					m.cursor--
				}
			case "down", "j":
				if m.cursor < m.manager.SessionCount()-1 {
					m.cursor++
				}
			case "enter":
				if m.cursor == -1 {
					// "New" was selected
					m.mode = viewFilePicker
					return m, m.filePicker.Init()
				} else if m.cursor >= 0 && m.cursor < m.manager.SessionCount() {
					// Open the torrent's download location in file explorer
					m.openInFileExplorer(m.cursor)
				}
			case "d", "x":
				// Delete/stop selected session
				if m.cursor >= 0 && m.cursor < m.manager.SessionCount() {
					m.stopSession(m.cursor)
					if m.cursor >= m.manager.SessionCount() && m.cursor > 0 {
						m.cursor--
					}
				}
			case "n":
				// Quick key to open file picker
				m.mode = viewFilePicker
				return m, m.filePicker.Init()
			}
		}

	case TickMsg:
		m.lastUpdate = time.Time(msg)
		m.readNewLogLines()
		return m, tick()

	default:
		// Pass all other messages to filepicker if it's active
		if m.mode == viewFilePicker {
			var cmd tea.Cmd
			m.filePicker, cmd = m.filePicker.Update(msg)
			return m, cmd
		}
	}

	return m, nil
}

// View renders the UI
func (m Model) View() string {
	if m.quitting {
		return "Shutting down...\n"
	}

	switch m.mode {
	case viewFilePicker:
		return m.viewFilePicker()
	case viewSessions:
		return m.viewSessions()
	}

	return ""
}

func (m Model) viewFilePicker() string {
	var s strings.Builder
	s.WriteString("╔═══════════════════════════════════════════════════════════════════════════════╗\n")
	s.WriteString("║                       Select a Torrent File                                   ║\n")
	s.WriteString("╚═══════════════════════════════════════════════════════════════════════════════╝\n\n")

	// Show current directory
	s.WriteString(fmt.Sprintf("Current directory: %s\n\n", m.filePicker.CurrentDirectory))

	s.WriteString(m.filePicker.View())

	s.WriteString("\n\n")
	s.WriteString("Navigation: ↑/↓ to move, → to enter directory, ← to go back\n")
	s.WriteString("Keys: Enter to select, ESC to cancel, q to quit")

	if m.err != nil {
		s.WriteString(fmt.Sprintf("\n\nError: %v", m.err))
	}
	return s.String()
}

// Column represents a single column of text with a fixed width
type Column struct {
	Width int
	Lines []string
}

// visualWidth returns the number of visible characters (runes) in a string
func visualWidth(s string) int {
	return utf8.RuneCountInString(s)
}

// padToWidth pads a string to exact visual width
func padToWidth(s string, width int) string {
	currentWidth := visualWidth(s)
	if currentWidth < width {
		return s + strings.Repeat(" ", width-currentWidth)
	} else if currentWidth > width {
		// Truncate by runes, not bytes
		runes := []rune(s)
		if len(runes) > width {
			return string(runes[:width])
		}
	}
	return s
}

// renderColumns combines multiple columns side-by-side with separators
func renderColumns(columns []Column, separator string) string {
	if len(columns) == 0 {
		return ""
	}

	// Find max number of lines across all columns
	maxLines := 0
	for _, col := range columns {
		if len(col.Lines) > maxLines {
			maxLines = len(col.Lines)
		}
	}

	// Build output line by line
	var result strings.Builder
	for lineIdx := 0; lineIdx < maxLines; lineIdx++ {
		for colIdx, col := range columns {
			// Get line from this column (or empty if past its content)
			line := ""
			if lineIdx < len(col.Lines) {
				line = col.Lines[lineIdx]
			}

			// Ensure line is exactly column width (by visual characters)
			line = padToWidth(line, col.Width)

			result.WriteString(line)

			// Add separator between columns (but not after last column)
			if colIdx < len(columns)-1 {
				result.WriteString(separator)
			}
		}
		result.WriteString("\n")
	}

	return result.String()
}

// wrapLine wraps a line to fit within maxWidth (by visual characters), breaking at spaces when possible
func wrapLine(line string, maxWidth int) []string {
	if visualWidth(line) <= maxWidth {
		return []string{line}
	}

	var wrapped []string
	runes := []rune(line)

	for len(runes) > 0 {
		if len(runes) <= maxWidth {
			wrapped = append(wrapped, string(runes))
			break
		}

		// Try to find a space to break at
		breakPoint := maxWidth
		foundSpace := false
		for i := maxWidth - 1; i > maxWidth-20 && i > 0 && i < len(runes); i-- {
			if runes[i] == ' ' {
				breakPoint = i
				foundSpace = true
				break
			}
		}

		wrapped = append(wrapped, string(runes[:breakPoint]))
		runes = runes[breakPoint:]

		// Skip leading space on continuation line
		if foundSpace && len(runes) > 0 && runes[0] == ' ' {
			runes = runes[1:]
		}
	}

	return wrapped
}

func (m Model) viewSessions() string {
	// Calculate column widths (60% / 40% split)
	separatorWidth := 3 // " │ "
	leftWidth := int(float64(m.width) * 0.6)
	rightWidth := m.width - leftWidth - separatorWidth

	// Enforce minimum widths
	if leftWidth < 40 {
		leftWidth = 40
	}
	if rightWidth < 30 {
		rightWidth = 30
	}

	// Build columns
	leftCol := Column{
		Width: leftWidth,
		Lines: m.buildSessionsColumn(leftWidth),
	}

	rightCol := Column{
		Width: rightWidth,
		Lines: m.buildLogsColumn(rightWidth),
	}

	// Render columns side-by-side
	return renderColumns([]Column{leftCol, rightCol}, " │ ")
}

func (m Model) buildSessionsColumn(width int) []string {
	var lines []string

	// Header line (simple, guaranteed to fit)
	lines = append(lines, "Active Sessions")

	// New torrent menu option
	cursor := " "
	if m.cursor == -1 {
		cursor = ">"
	}
	lines = append(lines, cursor+" [+] New Torrent (n)")
	lines = append(lines, "")

	// Sessions list
	sessions := m.manager.GetSessions()
	if len(sessions) == 0 {
		lines = append(lines, "  No active sessions.")
	} else {
		for i, session := range sessions {
			selected := " "
			if m.cursor == i {
				selected = ">"
			}

			// Get formatted session (multi-line)
			sessionContent := formatSessionCompact(i+1, session, width-3) // -3 for "> " prefix
			sessionLines := strings.Split(sessionContent, "\n")

			// Add each line with selection indicator
			for j, line := range sessionLines {
				if j == 0 {
					// First line gets the cursor
					lines = append(lines, selected+" "+line)
				} else {
					// Continuation lines get spaces
					lines = append(lines, "  "+line)
				}
			}
		}
	}

	// Footer
	lines = append(lines, "")
	lines = append(lines, "Updated: "+m.lastUpdate.Format("15:04:05"))
	lines = append(lines, "Keys: ↑/↓ or j/k, n=new, d=stop, q=quit")

	if m.err != nil {
		lines = append(lines, "")
		errorLines := wrapLine("Error: "+m.err.Error(), width)
		lines = append(lines, errorLines...)
	}

	return lines
}

func (m Model) buildLogsColumn(width int) []string {
	var lines []string

	// Header line (simple, guaranteed to fit)
	lines = append(lines, "Live Logs")

	if len(m.logLines) == 0 {
		lines = append(lines, "  Waiting for logs...")
		return lines
	}

	// Calculate how many lines we can display
	maxDisplayLines := m.height - 5
	if maxDisplayLines < 1 {
		maxDisplayLines = 10
	}

	// Build list of wrapped log lines (newest first)
	var wrappedLogs []string
	for i := len(m.logLines) - 1; i >= 0; i-- {
		// Wrap this log line
		wrapped := wrapLine(m.logLines[i], width)
		for _, w := range wrapped {
			wrappedLogs = append(wrappedLogs, w)
		}

		// Add blank separator after each log entry (except the last one we process)
		if i > 0 && len(wrappedLogs) < maxDisplayLines {
			wrappedLogs = append(wrappedLogs, "")
		}

		// Stop if we have enough lines
		if len(wrappedLogs) >= maxDisplayLines {
			break
		}
	}

	// Add the wrapped logs (already in newest-first order)
	lines = append(lines, wrappedLogs...)

	return lines
}

// formatSessionCompact formats a torrent session in compact form
func formatSessionCompact(index int, session *daemon.TorrentSession, maxWidth int) string {
	// Get torrent name
	torrentName := "Unknown"
	if nameObj, ok := session.TorrentFile.Data["info"].Dict["name"]; ok && nameObj.StrVal != nil {
		torrentName = *nameObj.StrVal
	}

	// Calculate progress
	totalPieces := session.PieceManager.TotalPieces
	completedPieces := session.PieceManager.CompletedPieces()
	totalSize := session.PieceManager.TotalSize()
	bytesRemaining := session.PieceManager.BytesRemaining()
	bytesDownloaded := totalSize - bytesRemaining
	percentComplete := float64(completedPieces) / float64(totalPieces) * 100

	// Status
	status := "DL"
	if session.PieceManager.IsComplete() {
		status = "OK"
	}

	activePeers := len(session.GetActivePeers())

	// Build multi-line display for this session
	var lines []string

	// Line 1: [#] Name
	lines = append(lines, fmt.Sprintf("[%d] %s", index, truncate(torrentName, maxWidth-6)))

	// Line 2: Progress bar
	barWidth := maxWidth - 10
	if barWidth > 40 {
		barWidth = 40
	}
	if barWidth < 10 {
		barWidth = 10
	}
	filledWidth := int(float64(barWidth) * percentComplete / 100)
	bar := strings.Repeat("█", filledWidth) + strings.Repeat("░", barWidth-filledWidth)
	lines = append(lines, fmt.Sprintf("[%s] %.1f%%", bar, percentComplete))

	// Line 3: Downloaded / Total, Peers, Status
	lines = append(lines, fmt.Sprintf("%.1f/%.1f MB | %d peers | %s",
		float64(bytesDownloaded)/(1024*1024),
		float64(totalSize)/(1024*1024),
		activePeers,
		status,
	))

	return strings.Join(lines, "\n")
}

// formatSession formats a torrent session for display
func formatSession(index int, session *daemon.TorrentSession) string {
	var s strings.Builder

	// Get torrent name from info dict
	torrentName := "Unknown"
	if nameObj, ok := session.TorrentFile.Data["info"].Dict["name"]; ok && nameObj.StrVal != nil {
		torrentName = *nameObj.StrVal
	}

	// Calculate progress
	totalPieces := session.PieceManager.TotalPieces
	completedPieces := session.PieceManager.CompletedPieces()
	totalSize := session.PieceManager.TotalSize()
	bytesRemaining := session.PieceManager.BytesRemaining()
	bytesDownloaded := totalSize - bytesRemaining
	percentComplete := float64(completedPieces) / float64(totalPieces) * 100

	// Count peers by status
	activePeers := len(session.GetActivePeers())
	failedPeers := len(session.GetFailedPeers())
	totalConnections := len(session.Connections)

	// Build the display
	s.WriteString(fmt.Sprintf("┌─ Session %d ─────────────────────────────────────────────────────────────────┐\n", index))
	s.WriteString(fmt.Sprintf("│ Name: %-70s │\n", truncate(torrentName, 70)))
	s.WriteString(fmt.Sprintf("│ Progress: %d/%d pieces (%.1f%%)                                              │\n",
		completedPieces, totalPieces, percentComplete))
	s.WriteString(fmt.Sprintf("│ Downloaded: %.2f MB / %.2f MB                                          │\n",
		float64(bytesDownloaded)/(1024*1024),
		float64(totalSize)/(1024*1024)))
	s.WriteString(fmt.Sprintf("│ Peers: %d active, %d failed, %d total connections                        │\n",
		activePeers, failedPeers, totalConnections))

	// Progress bar
	barWidth := 50
	filledWidth := int(float64(barWidth) * percentComplete / 100)
	bar := strings.Repeat("█", filledWidth) + strings.Repeat("░", barWidth-filledWidth)
	s.WriteString(fmt.Sprintf("│ [%s]                         │\n", bar))

	// Status
	status := "Downloading"
	if session.PieceManager.IsComplete() {
		status = "Complete"
	} else if activePeers == 0 {
		status = "Waiting for peers..."
	}
	s.WriteString(fmt.Sprintf("│ Status: %-68s │\n", status))
	s.WriteString("└───────────────────────────────────────────────────────────────────────────────┘")

	return s.String()
}

// truncate truncates a string to the specified length
func truncate(s string, length int) string {
	if len(s) <= length {
		return s
	}
	if length <= 3 {
		return s[:length]
	}
	return s[:length-3] + "..."
}

// tick returns a command that waits for 1 second and then sends a TickMsg
func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

// startTorrentFromFile loads a torrent file and starts downloading it (non-blocking)
func (m *Model) startTorrentFromFile(path string) error {
	fileData, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("error reading file: %w", err)
	}

	torrent, err := bencoding.ParseTorrentFile(string(fileData))
	if err != nil {
		return fmt.Errorf("error parsing torrent file: %w", err)
	}

	// Start the torrent session in a goroutine to avoid blocking the TUI
	go func() {
		_, err := m.manager.StartTorrentDownloadSession(torrent)
		if err != nil {
			m.err = fmt.Errorf("error starting torrent session: %w", err)
		}
	}()

	return nil
}

// stopSession stops a torrent session at the given index
func (m *Model) stopSession(index int) {
	sessions := m.manager.GetSessions()
	if index < 0 || index >= len(sessions) {
		return
	}

	session := sessions[index]
	session.Cancel()

	m.manager.RemoveSession(index)
}

// openInFileExplorer opens the torrent's download location in the file explorer
func (m *Model) openInFileExplorer(index int) {
	sessions := m.manager.GetSessions()
	if index < 0 || index >= len(sessions) {
		return
	}

	session := sessions[index]
	outputDir := session.DiskManager.GetOutputDir()

	// Use 'open' command on macOS to open in Finder
	cmd := exec.Command("open", outputDir)
	_ = cmd.Run() // Ignore errors silently
}

// readNewLogLines reads new lines from the log file
func (m *Model) readNewLogLines() {
	if m.logFile == nil {
		return
	}

	// Read new lines from log file
	reader := bufio.NewReader(m.logFile)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				return
			}
			// No more lines available
			break
		}

		// Add line to buffer (trim newline)
		line = strings.TrimSuffix(line, "\n")
		m.logLines = append(m.logLines, line)

		// Keep only last 1000 lines to avoid memory issues
		if len(m.logLines) > 1000 {
			m.logLines = m.logLines[len(m.logLines)-1000:]
		}
	}
}

// RunTUI starts the Bubbletea TUI
func RunTUI(manager *daemon.TorrentManager, cfg *config.Config, output *os.File, logFilePath string) error {
	// Create program with alternate screen to avoid log interference
	p := tea.NewProgram(
		NewModel(manager, cfg, logFilePath),
		tea.WithAltScreen(),         // Use alternate screen buffer
		tea.WithMouseCellMotion(),   // Enable mouse support
		tea.WithInput(os.Stdin),     // Explicitly use stdin
		tea.WithOutput(output),      // Output to provided file (original stdout/stderr)
	)

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}
	return nil
}
