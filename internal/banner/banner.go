package banner

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const bannerText = `██╗  ██╗ ██████╗ ███╗   ███╗███████╗██████╗ ██╗   ██╗███╗   ██╗
██║  ██║██╔═══██╗████╗ ████║██╔════╝██╔══██╗██║   ██║████╗  ██║
███████║██║   ██║██╔████╔██║█████╗  ██████╔╝██║   ██║██╔██╗ ██║
██╔══██║██║   ██║██║╚██╔╝██║██╔══╝  ██╔══██╗██║   ██║██║╚██╗██║
██║  ██║╚██████╔╝██║ ╚═╝ ██║███████╗██║  ██║╚██████╔╝██║ ╚████║
╚═╝  ╚═╝ ╚═════╝ ╚═╝     ╚═╝╚══════╝╚═╝  ╚═╝ ╚═════╝ ╚═╝  ╚═══╝`

const serviceText = ` ██████╗ ██████╗ ███╗   ██╗███████╗██╗ ██████╗       ██╗   ██╗██╗███████╗██╗    ██╗███████╗██████╗
██╔════╝██╔═══██╗████╗  ██║██╔════╝██║██╔════╝       ██║   ██║██║██╔════╝██║    ██║██╔════╝██╔══██╗
██║     ██║   ██║██╔██╗ ██║█████╗  ██║██║  ███╗█████╗██║   ██║██║█████╗  ██║ █╗ ██║█████╗  ██████╔╝
██║     ██║   ██║██║╚██╗██║██╔══╝  ██║██║   ██║╚════╝╚██╗ ██╔╝██║██╔══╝  ██║███╗██║██╔══╝  ██╔══██╗
╚██████╗╚██████╔╝██║ ╚████║██║     ██║╚██████╔╝       ╚████╔╝ ██║███████╗╚███╔███╔╝███████╗██║  ██║
 ╚═════╝ ╚═════╝ ╚═╝  ╚═══╝╚═╝     ╚═╝ ╚═════╝         ╚═══╝  ╚═╝╚══════╝ ╚══╝╚══╝ ╚══════╝╚═╝  ╚═╝
`

const glitchChars = "░▒▓█▄▀▐▌╠╣╬═║╗╝╚╔"

// lensArt is a magnifying glass sweeping across the field: the viewer looks at
// the configuration, it does not touch it.
var lensArt = []string{
	"   _____     ",
	"  / .   \\    ",
	" | .     |   ",
	" |       |   ",
	"  \\_____/\\   ",
	"         \\\\  ",
	"          \\\\ ",
}

const (
	fieldWidth   = 70
	maxFrames    = 40
	glitchFrames = 10
)

var (
	cyanHot = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#00C8FF")).
		Bold(true)

	cyanBright = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7FE7FF")).
			Bold(true)

	dimCyan = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#003A4D"))

	accentStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#A855F7")).
			Bold(true)

	serviceBlockStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#6366F1")).
				Bold(true)

	lensStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E0F7FF")).
			Bold(true)
)

type tickMsg time.Time

type model struct {
	width        int
	frame        int
	glitchPhase  bool
	glitchFrames int
	done         bool
}

// Show displays the animated banner then prints the final header.
func Show() {
	p := tea.NewProgram(initialModel())
	_, _ = p.Run()
	fmt.Println(renderHeader())
}

func initialModel() model {
	return model{width: 80, glitchPhase: true}
}

func tickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m model) Init() tea.Cmd { return tickCmd() }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		m.done = true
		return m, tea.Quit
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case tickMsg:
		m.frame++
		if m.glitchPhase {
			m.glitchFrames++
			if m.glitchFrames >= glitchFrames {
				m.glitchPhase = false
			}
			return m, tickCmd()
		}
		if m.frame >= maxFrames {
			m.done = true
			return m, tea.Quit
		}
		return m, tickCmd()
	}
	return m, nil
}

func (m model) View() tea.View {
	if m.done {
		return tea.NewView("")
	}

	var b strings.Builder

	if m.glitchPhase {
		b.WriteString(glitchText(bannerText, m.glitchFrames))
	} else {
		b.WriteString(cyanHot.Render(bannerText))
	}

	the2 := accentStyle.Render("2")
	if m.glitchPhase && m.glitchFrames < 8 {
		glitchRunes := []rune(glitchChars)
		the2 = accentStyle.Render(string(glitchRunes[rand.IntN(len(glitchRunes))]))
	}
	b.WriteString(the2)
	b.WriteString("\n\n")

	if m.glitchPhase {
		b.WriteString("\n\n")
	} else {
		lensPos := (m.frame * 3) % fieldWidth
		for _, artLine := range lensArt {
			b.WriteString(lensStyle.Render(strings.Repeat(" ", lensPos) + artLine))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if m.glitchPhase {
		b.WriteString(glitchText(serviceText, m.glitchFrames))
	} else {
		b.WriteString(serviceBlockStyle.Render(serviceText))
	}
	b.WriteString("\n")

	v := tea.NewView(centerText(applyScanlines(b.String()), m.width))
	v.AltScreen = true
	return v
}

func glitchText(text string, glitchFrame int) string {
	glitchProbability := max(float64(glitchFrames-glitchFrame)/glitchFrames, 0)

	runes := []rune(text)
	glitchRunes := []rune(glitchChars)
	result := make([]rune, len(runes))

	for i, r := range runes {
		if r == '\n' || r == ' ' || rand.Float64() >= glitchProbability {
			result[i] = r
			continue
		}
		result[i] = glitchRunes[rand.IntN(len(glitchRunes))]
	}

	return cyanBright.Render(string(result))
}

func applyScanlines(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i%2 == 1 {
			lines[i] = dimCyan.Render(line)
		}
	}
	return strings.Join(lines, "\n")
}

func centerText(text string, width int) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if visLen := lipgloss.Width(line); visLen < width {
			lines[i] = strings.Repeat(" ", (width-visLen)/2) + line
		}
	}
	return strings.Join(lines, "\n")
}

func renderHeader() string {
	var b strings.Builder
	b.WriteString(cyanHot.Render(bannerText))
	b.WriteString(accentStyle.Render("2"))
	b.WriteString("\n")
	b.WriteString(serviceBlockStyle.Render(serviceText))
	b.WriteString("\n")
	return b.String()
}
