//go:build windows

package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

var (
	colorGray  = color.RGBA{R: 120, G: 120, B: 120, A: 255}
	colorGreen = color.RGBA{R: 76, G: 175, B: 80, A: 255}
	colorRed   = color.RGBA{R: 244, G: 67, B: 54, A: 255}
	colorWhite = color.RGBA{R: 230, G: 230, B: 230, A: 255}
	colorBg    = color.RGBA{R: 40, G: 40, B: 40, A: 255}
)

func (a *App) buildTooltip() string {
	commands := a.config.GetCommands()
	states := a.manager.GetAllStates()
	tooltip := ""
	for _, cmd := range commands {
		state, exists := states[cmd.ID]
		status := ""
		if exists && state.Running {
			status = formatDuration(time.Since(state.StartTime))
		} else if exists && state.Stopped {
			status = "已停止"
		} else if exists && state.ExitCode != 0 && !state.Running && state.PID > 0 {
			status = fmt.Sprintf("exited(%d)", state.ExitCode)
		} else if exists && state.ExitCode == 0 && !state.Running && state.PID > 0 {
			status = "✓"
		}
		if status != "" {
			tooltip += fmt.Sprintf("%s  %s\n", cmd.Name, status)
		} else {
			tooltip += cmd.Name + "\n"
		}
	}
	if tooltip == "" {
		tooltip = "命令面板 - 暂无命令"
	}
	return tooltip
}

func gridLayout(n int) int {
	switch {
	case n <= 1:
		return 1
	case n <= 4:
		return 2
	case n <= 9:
		return 3
	default:
		return 4
	}
}

func (a *App) cellColor(cmdID string, state *RunState, exists bool) color.RGBA {
	if exists && state.Running {
		return colorGreen
	}
	if exists && state.Stopped {
		return colorGray
	}
	if exists && state.ExitCode != 0 && !state.Running && state.PID > 0 {
		return colorRed
	}
	if exists && state.ExitCode == 0 && !state.Running && state.PID > 0 {
		if a.wasSessionExitOK(cmdID) {
			return colorWhite
		}
		return colorGray
	}
	return colorGray
}

func (a *App) wasSessionExitOK(cmdID string) bool {
	a.sessionMu.RLock()
	defer a.sessionMu.RUnlock()
	return a.sessionExitOK[cmdID]
}

// generateTrayIcon builds a 16x16 RGBA image and encodes it as ICO
func (a *App) generateTrayIcon() []byte {
	commands := a.config.GetCommands()
	states := a.manager.GetAllStates()

	size := 16
	n := len(commands)

	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// Fill solid background
	for i := range img.Pix {
		img.Pix[i] = 255 // white opaque default
	}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.SetRGBA(x, y, colorBg)
		}
	}

	if n > 0 {
		grid := gridLayout(n)
		displayCount := n
		if displayCount > 16 {
			displayCount = 16
		}
		gap := 1
		cellSize := (size - (grid+1)*gap) / grid
		if cellSize < 2 {
			cellSize = 2
		}

		for i := 0; i < displayCount; i++ {
			row := i / grid
			col := i % grid
			var c color.RGBA
			if i == 15 && n > 16 {
				c = color.RGBA{R: 180, G: 160, B: 80, A: 255}
			} else {
				cmd := commands[i]
				st, ex := states[cmd.ID]
				c = a.cellColor(cmd.ID, st, ex)
			}
			x0 := gap + col*(cellSize+gap)
			y0 := gap + row*(cellSize+gap)
			for y := y0; y < y0+cellSize && y < size; y++ {
				for x := x0; x < x0+cellSize && x < size; x++ {
					img.SetRGBA(x, y, c)
				}
			}
		}
	}

	return rgbaToICO(img)
}

// rgbaToICO converts an RGBA image to ICO binary format
func rgbaToICO(img *image.RGBA) []byte {
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()

	// Encode the image as PNG first
	var pngBuf bytes.Buffer
	png.Encode(&pngBuf, img)
	pngData := pngBuf.Bytes()

	// ICO header: 6 bytes
	//   reserved: 2 (0)
	//   type: 2 (1 = icon)
	//   count: 2 (1)
	header := make([]byte, 6)
	binary.LittleEndian.PutUint16(header[0:2], 0)
	binary.LittleEndian.PutUint16(header[2:4], 1) // ICO type
	binary.LittleEndian.PutUint16(header[4:6], 1) // 1 image

	// ICO entry: 16 bytes
	//   width: 1 (0 = 256)
	//   height: 1 (0 = 256)
	//   color count: 1 (0)
	//   reserved: 1 (0)
	//   color planes: 2 (1)
	//   bits per pixel: 2 (32)
	//   size of image data: 4
	//   offset of image data: 4 (22 = 6 + 16)
	entry := make([]byte, 16)
	if w >= 256 {
		entry[0] = 0
	} else {
		entry[0] = byte(w)
	}
	if h >= 256 {
		entry[1] = 0
	} else {
		entry[1] = byte(h)
	}
	entry[2] = 0                                  // color count
	entry[3] = 0                                  // reserved
	binary.LittleEndian.PutUint16(entry[4:6], 1)  // color planes
	binary.LittleEndian.PutUint16(entry[6:8], 32) // bits per pixel
	binary.LittleEndian.PutUint32(entry[8:12], uint32(len(pngData)))
	binary.LittleEndian.PutUint32(entry[12:16], 22) // offset = 6 + 16

	result := make([]byte, 0, 22+len(pngData))
	result = append(result, header...)
	result = append(result, entry...)
	result = append(result, pngData...)
	return result
}

func (a *App) startTray(onShow func(), onQuit func()) {
	a.trayDone = make(chan struct{})
	tray := a.wails.SystemTray.New()
	menu := a.wails.NewMenu()
	menu.Add("打开面板").OnClick(func(*application.Context) { onShow() })
	menu.AddSeparator()
	menu.Add("退出程序").OnClick(func(*application.Context) { onQuit() })
	tray.SetMenu(menu)
	tray.OnClick(onShow)
	update := func() { tray.SetIcon(a.generateTrayIcon()); tray.SetTooltip(a.buildTooltip()) }
	update()
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				update()
			case <-a.trayDone:
				return
			}
		}
	}()
}
