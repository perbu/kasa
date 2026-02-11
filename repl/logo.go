package repl

import (
	"fmt"
	"math"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// kasaASCII is the block-letter KASA logo (ANSI Shadow style).
var kasaASCII = []string{
	`██╗  ██╗ █████╗ ███████╗ █████╗ `,
	`██║ ██╔╝██╔══██╗██╔════╝██╔══██╗`,
	`█████╔╝ ███████║███████╗███████║`,
	`██╔═██╗ ██╔══██║╚════██║██╔══██║`,
	`██║  ██╗██║  ██║███████║██║  ██║`,
	`╚═╝  ╚═╝╚═╝  ╚═╝╚══════╝╚═╝  ╚═╝`,
}

// --- color helpers ---

type logoRGB struct{ r, g, b float64 }

func (c logoRGB) hex() string {
	return fmt.Sprintf("#%02x%02x%02x",
		int(math.Round(c.r)),
		int(math.Round(c.g)),
		int(math.Round(c.b)))
}

func lerpColor(a, b logoRGB, t float64) logoRGB {
	return logoRGB{
		r: a.r + (b.r-a.r)*t,
		g: a.g + (b.g-a.g)*t,
		b: a.b + (b.b-a.b)*t,
	}
}

// --- gradient definitions ---

// 24-bit: cyan → indigo → pink → amber
var gradient24 = []logoRGB{
	{0, 212, 255},  // bright cyan
	{99, 102, 241}, // indigo
	{236, 72, 153}, // pink
	{245, 158, 11}, // amber
}

// 8-bit: hand-picked ANSI-256 codes that approximate the same sweep
var gradient8 = []string{"51", "63", "99", "135", "170", "204", "214", "220"}

func colorAt24(t float64) string {
	n := len(gradient24) - 1
	seg := t * float64(n)
	i := int(math.Floor(seg))
	if i >= n {
		i = n - 1
	}
	return lerpColor(gradient24[i], gradient24[i+1], seg-float64(i)).hex()
}

func colorAt8(t float64) string {
	n := len(gradient8) - 1
	idx := min(int(math.Round(t*float64(n))), n)
	return gradient8[idx]
}

func logoColorAt(t float64, depth int) string {
	if depth == 24 {
		return colorAt24(t)
	}
	return colorAt8(t)
}

// --- terminal detection ---

func colorDepth() int {
	ct := os.Getenv("COLORTERM")
	if ct == "truecolor" || ct == "24bit" {
		return 24
	}
	return 8
}

// --- sparkle line ---

type sparkle struct {
	pos    float64 // 0.0–1.0 across the width
	char   string
	bright bool
}

var topSparkles = []sparkle{
	{0.06, "✦", true},
	{0.15, "·", false},
	{0.28, "✧", true},
	{0.38, "·", false},
	{0.50, "✦", false},
	{0.62, "·", false},
	{0.72, "✧", true},
	{0.85, "·", false},
	{0.94, "✦", true},
}

func renderSparkleLine(width int, depth int, sparkles []sparkle) string {
	if width <= 0 {
		return ""
	}
	var sb strings.Builder
	idx := 0
	for col := range width {
		if idx < len(sparkles) {
			pos := min(int(sparkles[idx].pos*float64(width-1)), width-1)
			if col == pos {
				s := sparkles[idx]
				color := logoColorAt(s.pos, depth)
				style := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
				if s.bright {
					style = style.Bold(true)
				} else {
					style = style.Faint(true)
				}
				sb.WriteString(style.Render(s.char))
				idx++
				continue
			}
		}
		sb.WriteRune(' ')
	}
	return sb.String()
}

// --- public API ---

// RenderLogo returns the fully colorized KASA ASCII-art logo with sparkles.
func RenderLogo(version string) string {
	depth := colorDepth()
	var sb strings.Builder

	maxWidth := 0
	for _, line := range kasaASCII {
		if w := utf8.RuneCountInString(line); w > maxWidth {
			maxWidth = w
		}
	}
	if maxWidth == 0 {
		return ""
	}

	indent := "  "
	rows := len(kasaASCII)

	// Top sparkle decoration
	sb.WriteString(indent)
	sb.WriteString(renderSparkleLine(maxWidth, depth, topSparkles))
	sb.WriteString("\n")

	// Main logo with diagonal gradient
	for rowIdx, line := range kasaASCII {
		sb.WriteString(indent)
		col := 0
		for _, r := range line {
			if r == ' ' {
				sb.WriteRune(' ')
			} else {
				// Diagonal sweep: 85 % horizontal, 15 % vertical
				t := float64(col)/float64(maxWidth)*0.85 +
					float64(rowIdx)/float64(rows)*0.15
				if t > 1 {
					t = 1
				}
				color := logoColorAt(t, depth)
				style := lipgloss.NewStyle().
					Foreground(lipgloss.Color(color)).
					Bold(true)
				sb.WriteString(style.Render(string(r)))
			}
			col++
		}
		sb.WriteString("\n")
	}

	// Subtitle line: ✧ Kubernetes Deployment Assistant vX.Y.Z ✧
	subtitle := fmt.Sprintf("Kubernetes Deployment Assistant %s", version)
	subtitleLen := utf8.RuneCountInString(subtitle)

	var accentColor, subtitleColor string
	if depth == 24 {
		accentColor = gradient24[len(gradient24)-1].hex() // amber
		subtitleColor = lerpColor(gradient24[1], gradient24[2], 0.5).hex()
	} else {
		accentColor = "220"
		subtitleColor = "135"
	}
	accentStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(accentColor)).Bold(true)
	subtitleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(subtitleColor)).Bold(true)

	// Center beneath the logo (account for "✧ " and " ✧")
	decorated := subtitleLen + 4 // "✧ " + subtitle + " ✧"
	pad := max((maxWidth-decorated)/2, 0)

	sb.WriteString(indent)
	sb.WriteString(strings.Repeat(" ", pad))
	sb.WriteString(accentStyle.Render("✧"))
	sb.WriteString(" ")
	sb.WriteString(subtitleStyle.Render(subtitle))
	sb.WriteString(" ")
	sb.WriteString(accentStyle.Render("✧"))
	sb.WriteString("\n")

	return sb.String()
}
