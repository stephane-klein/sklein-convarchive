package ui

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// assertScreen checks that the virtual terminal screen matches the expected
// lines, with no stale content left over below the block.
func assertScreen(t *testing.T, v *vt, want []string) {
	t.Helper()
	for i, wl := range want {
		if got := v.screen[i]; got != wl {
			t.Errorf("screen line %d = %q, want %q", i, got, wl)
		}
	}
	for i := len(want); i < len(v.screen); i++ {
		if v.screen[i] != "" {
			t.Errorf("stale line at row %d: %q", i, v.screen[i])
		}
	}
}

// TestRefreshSequentialArchive replays a realistic archive sequence — a
// conversation is added, shown without children during planning, expanded with
// its month window, then collapsed on completion — and checks after every
// Redraw that the virtual terminal matches RenderAll exactly.
func TestRefreshSequentialArchive(t *testing.T) {
	v := &vt{}
	d := &Display{w: vtWriter{v}, tty: true, sp: newSpinner(), lastStatus: make(map[*Task]Status), MaxVisibleRoots: 4}

	archiveConv := func(name string, months int, complete bool) {
		conv := d.Root(name)
		conv.Status = StatusRunning
		conv.MaxVisibleChildren = 10
		conv.AnchorFirstWhenPending = true
		conv.CollapseWhenInactive = true
		d.Redraw() // planning state: running, no children yet
		assertScreen(t, v, d.renderLines())

		for i := 0; i < months; i++ {
			conv.AddChild(fmt.Sprintf("%s %02d", name, i))
		}
		conv.StatusText = "200 posts"
		d.Redraw()
		assertScreen(t, v, d.renderLines())

		if complete {
			for _, c := range conv.Children() {
				c.Status = StatusSuccess
				c.StatusText = "Ok (uploaded)"
			}
			conv.Status = StatusSuccess
			conv.StatusText = "Ok"
			conv.CollapsedSummary = "· 5 months"
			d.Redraw()
			assertScreen(t, v, d.renderLines())
		}
	}

	archiveConv("team-nimbus/chan-beta", 30, false) // active, stays expanded
	archiveConv("team-nimbus/chan-epsilon", 30, true)
	archiveConv("team-nimbus/chan-zeta", 30, true)
	archiveConv("team-nimbus/chan-eta", 30, true)
	archiveConv("team-quartz/chan-beta", 30, false) // active
	archiveConv("team-nimbus/chan-theta", 30, true)

	assertScreen(t, v, d.renderLines())
}

// TestRefreshConcurrentArchive runs the same sequence while a goroutine ticks
// Redraw, to exercise the spinner/archive goroutine interleaving. Run with
// -race to catch the data race between task mutations and rendering.
func TestRefreshConcurrentArchive(t *testing.T) {
	v := &vt{}
	d := &Display{w: vtWriter{v}, tty: true, sp: newSpinner(), lastStatus: make(map[*Task]Status), MaxVisibleRoots: 4}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(500 * time.Microsecond)
		defer ticker.Stop()
		for i := 0; i < 500; i++ {
			d.Redraw()
			<-ticker.C
		}
	}()

	for ci := 0; ci < 8; ci++ {
		name := fmt.Sprintf("team-%d/chan-%d", ci%2, ci)
		conv := d.Root(name)
		d.Update(func() {
			conv.Status = StatusRunning
			conv.MaxVisibleChildren = 10
			conv.AnchorFirstWhenPending = true
			conv.CollapseWhenInactive = true
		})
		d.Update(func() {
			for m := 0; m < 20; m++ {
				conv.AddChild(fmt.Sprintf("%s %02d", name, m))
			}
			conv.StatusText = "100 posts"
		})
		d.Update(func() {
			for _, c := range conv.Children() {
				c.Status = StatusSuccess
				c.StatusText = "Ok (uploaded)"
			}
			conv.Status = StatusSuccess
			conv.StatusText = "Ok"
			conv.CollapsedSummary = "· 5 months"
		})
	}
	wg.Wait()

	assertScreen(t, v, d.renderLines())
}
