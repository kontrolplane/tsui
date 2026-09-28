package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// raceEnabled skips the slowest sweeps, which take minutes under the race detector.
var raceEnabled bool

func TestReviewTinySizesDoNotPanic(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("slow sweep")
	}
	defer setLayout(142, 34)
	for _, w := range []int{1, 2, 3, 50, 101, 102, 103, 400} {
		for _, h := range []int{1, 2, 3, 10, 24, 25, 26, 200} {
			for name, m := range layoutPages(t, 160, 50, false) {
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Errorf("%s at %dx%d panicked: %v", name, w, h, r)
						}
					}()
					m, _ = update(t, m, tea.WindowSizeMsg{Width: w, Height: h})
					_ = m.View()
					for _, k := range []string{"j", "G", "k", "tab", "tab", "tab", "enter", "esc"} {
						m, _ = update(t, m, press(k))
						_ = m.View()
					}
				}()
			}
		}
	}
}
