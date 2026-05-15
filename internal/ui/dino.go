package ui

// Dino game — runs in a goroutine while the search corpus is being built.
// Uses ANSI cursor positioning to render in a fixed area below the progress
// bar without disturbing the rest of the terminal output.

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

// ── Layout constants ──────────────────────────────────────────────────────────

const (
	gameWidth = 62 // visible columns
	groundRow = 3  // 0-based row index of the ground level within the game area
	dinoCol   = 5  // column where the dino stands

	tickInterval = 60 * time.Millisecond // ~16 fps
	scrollSpeed  = 1                     // columns per tick
)

// totalRows is the number of terminal rows the game occupies (below the
// progress line).  Layout:
//
//	0  title + score
//	1  separator
//	2  sky row (dino when jumping high, top of tall cactus)
//	3  ground row (dino standing, cactus body)
//	4  ground bar
//	5  hint
const totalRows = 6

// ── ASCII art ─────────────────────────────────────────────────────────────────

// dinoFrames[animFrame][groundRow|skyRow] = string rendered at that row.
// The dino is 4 chars wide.
var dinoFrames = [2][2]string{
	// frame 0 – right foot forward
	{
		` \o`, // sky row (head+body only when jumping)
		`/|\`, // ground row
	},
	// frame 1 – left foot forward
	{
		` \o`,
		`/|/`,
	},
}

// cactusBody is drawn only on the ground row (2 chars wide).
// Cactus has no sky part — the player can safely jump over it.
const cactusBody = `╫╫`

// ── Game state ────────────────────────────────────────────────────────────────

type obstacle struct {
	col int
}

type dinoGame struct {
	score     int
	tick      int
	animFrame int

	// dino vertical state
	jumping  bool
	jumpTick int // ticks since jump started
	jumpRow  int // current sky row offset (0 = ground, 1 = low, 2 = high)

	obstacles []obstacle
	alive     bool
}

func newDinoGame() *dinoGame {
	return &dinoGame{alive: true}
}

func (g *dinoGame) jump() {
	if !g.jumping {
		g.jumping = true
		g.jumpTick = 0
	}
}

// jumpProfile maps jumpTick → vertical offset above ground (0=ground, 1=jumping).
var jumpProfile = []int{1, 1, 1, 1, 1, 1, 1, 0}

func (g *dinoGame) update() {
	if !g.alive {
		return
	}
	g.tick++
	g.score = g.tick / 3

	// Animation frame toggles every 8 ticks
	g.animFrame = (g.tick / 8) % 2

	// Jump physics
	if g.jumping {
		if g.jumpTick < len(jumpProfile) {
			g.jumpRow = jumpProfile[g.jumpTick]
		} else {
			g.jumpRow = 0
			g.jumping = false
		}
		g.jumpTick++
	}

	// Scroll obstacles
	for i := range g.obstacles {
		g.obstacles[i].col -= scrollSpeed
	}
	// Remove off-screen
	j := 0
	for _, o := range g.obstacles {
		if o.col+2 >= 0 {
			g.obstacles[j] = o
			j++
		}
	}
	g.obstacles = g.obstacles[:j]

	// Spawn new obstacle (every 25–45 ticks, randomly)
	if len(g.obstacles) == 0 && g.tick%5 == 0 && g.tick > 20 {
		gap := 25 + rand.Intn(20)
		if g.tick%gap == 0 {
			g.obstacles = append(g.obstacles, obstacle{col: gameWidth - 1})
		}
	} else if len(g.obstacles) > 0 && g.obstacles[len(g.obstacles)-1].col < gameWidth-30 {
		if rand.Intn(40) == 0 {
			g.obstacles = append(g.obstacles, obstacle{col: gameWidth - 1})
		}
	}

	// Collision: cactus is ground-only; safe when dino is jumping (jumpRow > 0).
	if g.jumpRow == 0 {
		for _, o := range g.obstacles {
			// Cactus is 2 wide: o.col and o.col+1
			if o.col <= dinoCol+2 && o.col+1 >= dinoCol {
				g.alive = false
				return
			}
		}
	}
}

// render writes the game area to stdout using ANSI cursor positioning.
// Cursor starts and ends at the progress bar line.
func (g *dinoGame) render(isDone bool) {
	const down1 = "\033[1B\r" // move cursor 1 line down, back to column 0
	up := func(n int) string { return fmt.Sprintf("\033[%dA\r", n) }
	clearLine := "\033[2K"

	buf := strings.Builder{}

	// Move to first game row (1 below the progress bar).
	buf.WriteString(down1)

	for r := 0; r < totalRows; r++ {
		buf.WriteString(clearLine)
		switch r {
		case 0: // title + score
			title := "  DINO RUNNER"
			var scoreStr string
			switch {
			case isDone:
				scoreStr = fmt.Sprintf("FINAL: %03d  (loading done!)", g.score)
			case !g.alive:
				scoreStr = fmt.Sprintf("SCORE: %03d  [GAME OVER - SPACE restart · Q quit]", g.score)
			default:
				scoreStr = fmt.Sprintf("SCORE: %03d", g.score)
			}
			pad := gameWidth - len(title) - len(scoreStr)
			if pad < 1 {
				pad = 1
			}
			buf.WriteString(title + strings.Repeat(" ", pad) + scoreStr)

		case 1: // separator
			buf.WriteString("  " + strings.Repeat("─", gameWidth-2))

		case 2: // sky row — dino only when jumping
			line := make([]byte, gameWidth)
			for i := range line {
				line[i] = ' '
			}
			if g.jumpRow >= 1 {
				copyArt(line, dinoCol, dinoFrames[g.animFrame][0])
			}
			buf.WriteString("  " + string(line))

		case 3: // ground row — dino (standing) + cacti
			line := make([]byte, gameWidth)
			for i := range line {
				line[i] = ' '
			}
			if g.alive || g.jumpRow == 0 {
				if !g.alive {
					copyArt(line, dinoCol, `*X*`)
				} else if g.jumpRow == 0 {
					copyArt(line, dinoCol, dinoFrames[g.animFrame][1])
				}
			}
			// Cacti are ground-only
			for _, o := range g.obstacles {
				if o.col >= 0 && o.col < gameWidth {
					copyArt(line, o.col, cactusBody)
				}
			}
			buf.WriteString("  " + string(line))

		case 4: // ground bar
			buf.WriteString("  " + strings.Repeat("▓", gameWidth-2))

		case 5: // hint
			buf.WriteString("  SPACE to jump · Q to quit")
		}

		// Move to next row (except after the last one).
		if r < totalRows-1 {
			buf.WriteString(down1)
		}
	}

	// Restore cursor to progress bar line.
	buf.WriteString(up(totalRows))

	fmt.Fprint(os.Stdout, buf.String())
}

func copyArt(line []byte, col int, art string) {
	for i, ch := range []byte(art) {
		pos := col + i
		if pos >= 0 && pos < len(line) {
			line[pos] = ch
		}
	}
}

// clearGameArea erases the game rows from the terminal.
func clearGameArea() {
	buf := strings.Builder{}
	buf.WriteString("\033[1B\r")
	for r := 0; r < totalRows; r++ {
		buf.WriteString("\033[2K")
		if r < totalRows-1 {
			buf.WriteString("\033[1B\r")
		}
	}
	buf.WriteString(fmt.Sprintf("\033[%dA\r", totalRows))
	fmt.Fprint(os.Stdout, buf.String())
}

// ── Public entry point ────────────────────────────────────────────────────────

// RunDinoGame launches the dino game in the current goroutine.
// It returns when ctx is cancelled (loading done) or the user presses Q.
// Call it in a separate goroutine alongside the corpus-building work.
// Only runs when stdout is an interactive terminal.
func RunDinoGame(ctx context.Context) {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		<-ctx.Done()
		return
	}

	// Reserve space for game area
	fmt.Print(strings.Repeat("\n", totalRows+1))
	fmt.Printf("\033[%dA", totalRows+1)

	// Switch stdin to raw mode so we can read keys without Enter
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		<-ctx.Done()
		return
	}
	defer func() {
		term.Restore(int(os.Stdin.Fd()), oldState) //nolint:errcheck
		clearGameArea()
	}()

	game := newDinoGame()

	// Key reader goroutine — sends true for jump, false for quit
	keyCh := make(chan byte, 8)
	go func() {
		buf := make([]byte, 1)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil || n == 0 {
				return
			}
			keyCh <- buf[0]
		}
	}()

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	done := false
	for !done {
		select {
		case <-ctx.Done():
			done = true
			game.render(true)
			time.Sleep(1200 * time.Millisecond)

		case <-ticker.C:
			if !game.alive {
				// Dead — still render but wait for Q or done
				game.render(false)
				continue
			}
			game.update()
			game.render(false)

		case key := <-keyCh:
			switch key {
			case ' ', 'w', 'W':
				if !game.alive {
					game = newDinoGame() // restart
				} else {
					game.jump()
				}
			case 'q', 'Q', 3: // 3 = Ctrl-C
				done = true
			}
		}
	}
}
