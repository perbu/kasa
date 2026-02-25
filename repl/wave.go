package repl

import (
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// waveTickMsg is the tick message for the wave spinner animation.
type waveTickMsg struct {
	id int
}

// waveSpinner renders a bouncing wave animation with cycling gradient colors.
// It replaces the standard braille spinner with a wave that slushes back and
// forth across a fixed width, using the logo's gradient palette.
type waveSpinner struct {
	id    int
	frame int
	width int
	fps   time.Duration
	depth int
}

const waveBg = '·'

var waveKernel = []rune{'∘', 'o', 'O', 'o', '∘'}

func newWaveSpinner() waveSpinner {
	return waveSpinner{
		width: 21,
		fps:   80 * time.Millisecond,
		depth: colorDepth(),
	}
}

// Tick returns a tea.Cmd that schedules the next animation frame.
func (w waveSpinner) Tick() tea.Cmd {
	id := w.id
	return tea.Tick(w.fps, func(time.Time) tea.Msg {
		return waveTickMsg{id: id}
	})
}

// Update advances the animation by one frame on a matching tick.
func (w waveSpinner) Update(msg tea.Msg) (waveSpinner, tea.Cmd) {
	tick, ok := msg.(waveTickMsg)
	if !ok || tick.id != w.id {
		return w, nil
	}
	w.frame++
	return w, w.Tick()
}

// View renders the current wave frame as a colored string.
func (w waveSpinner) View() string {
	kLen := len(waveKernel)
	travel := w.width - kLen
	if travel < 0 {
		travel = 0
	}

	// Bounce: position goes 0 → travel → 0 → travel …
	cycle := travel * 2
	if cycle == 0 {
		cycle = 1
	}
	raw := w.frame % cycle
	pos := raw
	if pos > travel {
		pos = cycle - pos
	}

	// Color phase shifts over time for the cycling effect
	phase := float64(w.frame) * 0.04

	var sb strings.Builder
	for i := range w.width {
		ki := i - pos
		inKernel := ki >= 0 && ki < kLen

		var ch rune
		if inKernel {
			ch = waveKernel[ki]
		} else {
			ch = waveBg
		}

		// Gradient color: position along strip + time-varying phase
		t := math.Mod(float64(i)/float64(w.width)+phase, 1.0)
		if t < 0 {
			t += 1.0
		}
		color := logoColorAt(t, w.depth)

		style := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
		if !inKernel {
			style = style.Faint(true)
		}
		sb.WriteString(style.Render(string(ch)))
	}

	return sb.String()
}
