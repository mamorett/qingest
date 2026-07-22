package progress

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

type ProgressBar struct {
	mu           sync.Mutex
	total        int
	current      int
	description  string
	lastStatus   string
	lastRedraw   time.Time
	lastLog      time.Time
	isTTY        bool
	finished     bool
}

const (
	colorCyan   = "\033[36m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorRed    = "\033[31m"
	colorReset  = "\033[0m"
)

func NewProgressBar(total int, description string) *ProgressBar {
	fd := int(os.Stdout.Fd())
	isTTY := term.IsTerminal(fd)

	pb := &ProgressBar{
		total:       total,
		description: description,
		isTTY:       isTTY,
		lastRedraw:  time.Now(),
		lastLog:     time.Now(),
	}

	pb.render(true)
	return pb
}

func (pb *ProgressBar) Describe(desc string) {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.description = desc
	pb.render(false)
}

func (pb *ProgressBar) Increment() {
	pb.IncrementWithStatus("")
}

func (pb *ProgressBar) IncrementWithStatus(status string) {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	if pb.finished {
		return
	}

	pb.current++
	if status != "" {
		pb.lastStatus = status
	}

	if pb.current >= pb.total {
		pb.finished = true
		pb.render(true)
		if pb.isTTY {
			fmt.Println()
		}
		return
	}

	pb.render(false)
}

func (pb *ProgressBar) UpdateWithStatus(status string) {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	if pb.finished {
		return
	}

	pb.lastStatus = status
	pb.render(false)
}

func (pb *ProgressBar) Finish() {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	if pb.finished {
		return
	}

	pb.current = pb.total
	pb.finished = true
	pb.render(true)
	if pb.isTTY {
		fmt.Println()
	}
}

func (pb *ProgressBar) render(force bool) {
	now := time.Now()

	if !pb.isTTY {
		// Non-terminal fallback
		if force || now.Sub(pb.lastLog) >= 5*time.Second || pb.finished {
			pb.lastLog = now
			pct := 0
			if pb.total > 0 {
				pct = (pb.current * 100) / pb.total
			}
			msg := fmt.Sprintf("[%s] %d/%d (%d%%)", pb.description, pb.current, pb.total, pct)
			if pb.lastStatus != "" {
				msg += " - " + pb.lastStatus
			}
			slog.Info(msg)
		}
		return
	}

	// TTY output
	if !force && now.Sub(pb.lastRedraw) < 65*time.Millisecond {
		return
	}
	pb.lastRedraw = now

	termWidth := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 20 {
		termWidth = w
	}

	pct := 0
	if pb.total > 0 {
		pct = (pb.current * 100) / pb.total
	}

	prefix := fmt.Sprintf("%s %d/%d [%d%%] ", pb.description, pb.current, pb.total, pct)
	
	// Determine bar width
	barWidth := 20
	if termWidth > len(prefix)+30 {
		barWidth = termWidth - len(prefix) - 30
		if barWidth > 40 {
			barWidth = 40
		}
	}

	filled := 0
	if pb.total > 0 {
		filled = (pb.current * barWidth) / pb.total
	}

	var barBuf strings.Builder
	barBuf.WriteString("[")
	for i := 0; i < barWidth; i++ {
		if i < filled {
			barBuf.WriteString("=")
		} else if i == filled && filled > 0 && filled < barWidth {
			barBuf.WriteString(">")
		} else {
			barBuf.WriteString(" ")
		}
	}
	barBuf.WriteString("]")

	coloredBar := colorCyan + barBuf.String() + colorReset

	// Format status with color codes
	statusStr := pb.lastStatus
	if strings.HasPrefix(statusStr, "✓") {
		statusStr = colorGreen + statusStr + colorReset
	} else if strings.HasPrefix(statusStr, "SKIP") {
		statusStr = colorYellow + statusStr + colorReset
	} else if strings.HasPrefix(statusStr, "✗") {
		statusStr = colorRed + statusStr + colorReset
	}

	line := fmt.Sprintf("\r\033[K%s%s %s", prefix, coloredBar, statusStr)
	
	// Truncate to terminal width if needed
	if len(line) > termWidth+50 { // extra padding for ANSI color codes
		// avoid truncating ANSI sequences roughly
	}

	fmt.Print(line)
}
