package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	barWidth       = 30
	redrawInterval = 100 * time.Millisecond
	pollInterval   = 200 * time.Millisecond
)

// Bar is a minimal, thread-safe terminal progress bar measured in bytes.
// When the output is not a terminal it only prints a single line per phase.
type Bar struct {
	mu       sync.Mutex
	w        io.Writer
	label    string
	total    int64
	current  int64
	start    time.Time
	lastDraw time.Time
	tty      bool
	enabled  bool
	done     bool
}

// NewBar creates a progress bar. A total <= 0 renders an indeterminate bar.
func NewBar(w io.Writer, label string, total int64, enabled bool) *Bar {
	b := &Bar{
		w:       w,
		label:   label,
		total:   total,
		start:   time.Now(),
		tty:     isTerminal(w),
		enabled: enabled,
	}
	b.mu.Lock()
	b.draw(true)
	b.mu.Unlock()
	return b
}

// Set updates the current progress value.
func (b *Bar) Set(n int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done {
		return
	}
	if b.total > 0 && n > b.total {
		n = b.total
	}
	if n < b.current {
		// Directory polling can briefly undercount; never move backwards.
		return
	}
	b.current = n
	b.draw(false)
}

// Finish marks the bar as complete.
func (b *Bar) Finish() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done {
		return
	}
	if b.total > 0 {
		b.current = b.total
	}
	b.done = true
	if !b.enabled {
		return
	}
	if b.tty {
		b.draw(true)
		fmt.Fprintln(b.w)
		return
	}
	fmt.Fprintf(b.w, "%s: done (%s in %s)\n", b.label, formatBytes(b.current), time.Since(b.start).Round(100*time.Millisecond))
}

// Abort stops the bar without marking it complete.
func (b *Bar) Abort() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done {
		return
	}
	b.done = true
	if b.enabled && b.tty {
		fmt.Fprintln(b.w)
	}
}

func (b *Bar) draw(force bool) {
	if !b.enabled || !b.tty {
		return
	}
	now := time.Now()
	if !force && now.Sub(b.lastDraw) < redrawInterval {
		return
	}
	b.lastDraw = now

	elapsed := now.Sub(b.start).Seconds()
	var speed float64
	if elapsed > 0 {
		speed = float64(b.current) / elapsed
	}

	var line string
	if b.total > 0 {
		ratio := float64(b.current) / float64(b.total)
		filled := int(ratio * barWidth)
		bar := strings.Repeat("=", filled)
		if filled < barWidth {
			bar += ">" + strings.Repeat(" ", barWidth-filled-1)
		}
		eta := "--"
		if speed > 0 && b.current < b.total {
			eta = (time.Duration(float64(b.total-b.current)/speed) * time.Second).Round(time.Second).String()
		} else if b.current >= b.total {
			eta = "0s"
		}
		line = fmt.Sprintf("%s [%s] %3.0f%%  %s/%s  %s/s  ETA %s",
			b.label, bar, ratio*100, formatBytes(b.current), formatBytes(b.total), formatBytes(int64(speed)), eta)
	} else {
		pos := int(elapsed*10) % barWidth
		bar := strings.Repeat(" ", pos) + "<=>"
		if len(bar) > barWidth {
			bar = bar[:barWidth]
		}
		bar += strings.Repeat(" ", barWidth-len(bar))
		line = fmt.Sprintf("%s [%s] %s  %s/s", b.label, bar, formatBytes(b.current), formatBytes(int64(speed)))
	}
	// \r returns to column 0, \033[2K clears the line.
	fmt.Fprintf(b.w, "\r\033[2K%s", line)
}

// watchDir polls the total size of dir and reports offset+size to the bar
// until the returned stop function is called.
func watchDir(dir string, offset int64, bar *Bar) (stop func()) {
	quit := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		t := time.NewTicker(pollInterval)
		defer t.Stop()
		for {
			select {
			case <-quit:
				return
			case <-t.C:
				bar.Set(offset + dirSize(dir))
			}
		}
	}()
	return func() {
		close(quit)
		<-finished
	}
}

// dirSize returns the cumulative size of regular files under dir. Errors are
// ignored because files may appear or vanish while an extractor is running.
func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
