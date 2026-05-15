package ui

import (
	"fmt"
	"os"
	"strings"
)

const (
	colorBlue    = "\033[34m"
	colorCyan    = "\033[36m"
	colorGreen   = "\033[32m"
	colorMagenta = "\033[35m"
	colorRed     = "\033[31m"
	colorYellow  = "\033[33m"
	colorReset   = "\033[0m"
)

type Printer struct {
	Verbose      bool
	Color        bool
	Interactive  bool
	progressLine bool
}

func New(verbose bool) *Printer {
	stat, err := os.Stdout.Stat()
	interactive := err == nil && (stat.Mode()&os.ModeCharDevice) != 0
	return &Printer{Verbose: verbose, Color: interactive, Interactive: interactive}
}

func (p *Printer) Header() {
	p.clearProgressLine()
	fmt.Println(p.paint(colorBlue, "Odoo Method Analyzer"))
	fmt.Println(strings.Repeat("=", 44))
}

func (p *Printer) Step(current int, total int, message string) {
	p.clearProgressLine()
	fmt.Println(p.paint(colorCyan, fmt.Sprintf("Step %d/%d: %s", current, total, message)))
}

func (p *Printer) Info(message string) {
	p.clearProgressLine()
	fmt.Println(p.paint(colorCyan, "INFO  ") + message)
}

func (p *Printer) Warn(message string) {
	p.clearProgressLine()
	fmt.Println(p.paint(colorYellow, "WARN  ") + message)
}

func (p *Printer) Error(message string) {
	p.clearProgressLine()
	fmt.Fprintln(os.Stderr, p.paint(colorRed, "ERROR ")+message)
}

func (p *Printer) Success(message string) {
	p.clearProgressLine()
	fmt.Println(p.paint(colorGreen, "OK    ") + message)
}

func (p *Printer) Debug(message string) {
	if p.Verbose {
		p.clearProgressLine()
		fmt.Println(p.paint(colorCyan, "DEBUG ") + message)
	}
}

func (p *Printer) Progress(current int, total int, message string) {
	if total <= 0 {
		return
	}

	if !p.Interactive {
		if current == 1 || current == total || current%250 == 0 {
			fmt.Printf("%s: %d/%d\n", message, current, total)
		}
		return
	}

	width := 32
	filled := current * width / total
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("#", filled) + strings.Repeat("-", width-filled)
	percentage := current * 100 / total
	p.progressLine = true
	fmt.Printf("\r\033[2K%s %s [%s] %d%% (%d/%d)", p.paint(colorCyan, "PROGRESS"), message, bar, percentage, current, total)
	if current == total {
		fmt.Print("\n")
		p.progressLine = false
	}
}

func (p *Printer) clearProgressLine() {
	if !p.progressLine {
		return
	}
	if p.Interactive {
		fmt.Print("\r\033[2K")
	}
	p.progressLine = false
}

func (p *Printer) paint(color string, message string) string {
	if !p.Color {
		return message
	}
	return color + message + colorReset
}

// RouteColor returns msg wrapped in magenta — used to highlight HTTP route methods.
func (p *Printer) RouteColor(msg string) string {
	return p.paint(colorMagenta, msg)
}

// YellowColor returns msg wrapped in yellow — used for warnings.
func (p *Printer) YellowColor(msg string) string {
	return p.paint(colorYellow, msg)
}
