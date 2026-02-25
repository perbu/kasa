package repl

import (
	"fmt"
	"math"
	"os"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"golang.org/x/term"
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

// --- zigzag pattern ---

// renderZigzag generates a zigzag pattern string of the given width.
// Even rows use ╱╲╱╲, odd rows use ╲╱╲╱, creating a woven texture.
func renderZigzag(width, rowIdx, depth int, tOffset float64) string {
	if width <= 0 {
		return ""
	}
	var sb strings.Builder
	even := rowIdx%2 == 0
	for col := range width {
		var ch rune
		if even {
			if col%2 == 0 {
				ch = '╱'
			} else {
				ch = '╲'
			}
		} else {
			if col%2 == 0 {
				ch = '╲'
			} else {
				ch = '╱'
			}
		}
		// Gradient position: blend across full terminal width
		t := tOffset + float64(col)/float64(width)*(1.0-tOffset)
		if t > 1 {
			t = 1
		}
		color := logoColorAt(t, depth)
		style := lipgloss.NewStyle().
			Foreground(lipgloss.Color(color)).
			Faint(true)
		sb.WriteString(style.Render(string(ch)))
	}
	return sb.String()
}

// termWidth returns the current terminal width, defaulting to 80.
func termWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return 80
	}
	return w
}

// --- public API ---

// RenderLogo returns the fully colorized KASA ASCII-art logo with zigzag banner.
func RenderLogo() string {
	depth := colorDepth()
	var sb strings.Builder

	logoWidth := 0
	for _, line := range kasaASCII {
		if w := utf8.RuneCountInString(line); w > logoWidth {
			logoWidth = w
		}
	}
	if logoWidth == 0 {
		return ""
	}

	tw := termWidth()
	leftWidth := 8                               // pattern strip left of logo
	gap := 2                                     // space on each side of logo
	rightWidth := tw - leftWidth - gap - logoWidth - gap // pattern fills the rest
	if rightWidth < 0 {
		rightWidth = 0
	}

	rows := len(kasaASCII)

	// Main logo rows: left pattern, logo, right pattern
	for rowIdx, line := range kasaASCII {
		// Left zigzag strip
		if leftWidth > 0 {
			sb.WriteString(renderZigzag(leftWidth, rowIdx, depth, 0.0))
		}
		sb.WriteString(strings.Repeat(" ", gap))

		// Logo characters with diagonal gradient
		col := 0
		for _, r := range line {
			if r == ' ' {
				sb.WriteRune(' ')
			} else {
				t := float64(col)/float64(logoWidth)*0.85 +
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

		// Pad short logo lines to full logoWidth
		runeCount := utf8.RuneCountInString(line)
		if runeCount < logoWidth {
			sb.WriteString(strings.Repeat(" ", logoWidth-runeCount))
		}

		sb.WriteString(strings.Repeat(" ", gap))

		// Right zigzag: fills remaining terminal width
		if rightWidth > 0 {
			sb.WriteString(renderZigzag(rightWidth, rowIdx, depth, 0.5))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
