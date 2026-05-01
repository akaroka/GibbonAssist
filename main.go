package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
	"syscall"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"GibbonAss/audio"
)

// Windows API
var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procGetAsyncKeyState = user32.NewProc("GetAsyncKeyState")
)

const (
	VK_CONTROL = 0x11
	VK_LMENU   = 0xA4
)

type state int

const (
	stateIdle       state = iota // gray
	stateRecording               // red
	stateProcessing              // blue
)

func main() {
	a := app.New()
	w := a.NewWindow("录音转写工具")

	// --- mic dropdown (real devices) ---
	micSelect := widget.NewSelect(nil, func(string) {})
	micSelect.PlaceHolder = "选择麦克风..."

	devices, err := audio.EnumerateDevices()
	if err != nil {
		log.Printf("枚举录音设备失败: %v", err)
	}
	deviceNames := make([]string, 0, len(devices))
	for _, d := range devices {
		deviceNames = append(deviceNames, d.Name)
	}
	micSelect.Options = deviceNames
	if len(deviceNames) == 1 {
		micSelect.SetSelected(deviceNames[0])
	}
	if len(deviceNames) == 0 {
		micSelect.PlaceHolder = "未检测到麦克风"
		micSelect.Disable()
	}

	// --- record button ---
	recordBtn := widget.NewButton("开始录音", nil)
	recordBtn.Importance = widget.MediumImportance

	curr := stateIdle
	var rec *audio.Recorder

	exeDir := func() string {
		exe, err := os.Executable()
		if err != nil {
			return "."
		}
		return filepath.Dir(exe)
	}()

	// shared toggle: button + hotkey both call this
	toggle := func() {
		log.Printf("toggle 调用: curr=%d\n", curr)
		switch curr {
		case stateIdle:
			// --- start recording ---
			if len(devices) == 0 {
				log.Println("无可用录音设备")
				return
			}
			// Find selected device
			sel := micSelect.Selected
			devID := devices[0].ID // fallback
			for _, d := range devices {
				if d.Name == sel {
					devID = d.ID
					break
				}
			}
			rec = audio.NewRecorder(devID)
			if err := rec.Start(); err != nil {
				log.Printf("录音启动失败: %v", err)
				return
			}
			log.Printf("录音开始，设备=%s (ID=%d)", sel, devID)

			curr = stateRecording
			recordBtn.SetText("录音中...")
			recordBtn.Importance = widget.DangerImportance
			recordBtn.Refresh()

		case stateRecording:
			// --- stop recording & save WAV ---
			recordBtn.SetText("处理中...")
			recordBtn.Importance = widget.HighImportance
			recordBtn.Refresh()

			pcm, err := rec.Stop()
			if err != nil {
				log.Printf("录音停止失败: %v", err)
				curr = stateIdle
				recordBtn.SetText("开始录音")
				recordBtn.Importance = widget.MediumImportance
				recordBtn.Refresh()
				return
			}
			log.Printf("录音停止，PCM 数据大小: %d bytes (%d 帧, %.1f 秒)",
				len(pcm), len(pcm)/2, float64(len(pcm))/float64(audio.SampleRate*audio.BytesPerFrame))
			if len(pcm) == 0 {
				log.Println("警告: PCM 数据为空，WAV 文件将只有头部")
			}

			// Save WAV to exe directory
			filename := fmt.Sprintf("recording_%s.wav", time.Now().Format("20060102_150405"))
			wavPath := filepath.Join(exeDir, filename)
			if err := audio.WriteWAV(wavPath, pcm); err != nil {
				log.Printf("保存 WAV 文件失败: %v", err)
			} else {
				log.Printf("录音已保存: %s (%d bytes)", wavPath, len(pcm))
			}

			// Auto revert to idle after 2s (mimicking processing)
			curr = stateProcessing
			go func() {
				time.Sleep(2 * time.Second)
				if curr == stateProcessing {
					curr = stateIdle
					recordBtn.SetText("开始录音")
					recordBtn.Importance = widget.MediumImportance
					recordBtn.Refresh()
				}
			}()

		case stateProcessing:
			// ignore
		}
	}

	recordBtn.OnTapped = toggle

	// --- layout ---
	content := container.NewPadded(
		container.NewVBox(
			widget.NewLabelWithStyle("选择麦克风", fyne.TextAlignLeading, fyne.TextStyle{}),
			micSelect,
			recordBtn,
		),
	)

	w.SetContent(content)
	w.Resize(fyne.NewSize(320, 150))
	w.SetFixedSize(true)

	// --- global hotkey: Ctrl + Left Alt (polling) ---
	log.Println("启动热键监听 goroutine")
	ctx, cancel := context.WithCancel(context.Background())
	go hotkeyLoop(ctx, func() {
		log.Println("onHotkey 回调执行")
		fyne.Do(toggle)
	})

	w.SetCloseIntercept(func() {
		cancel()
		w.Close()
	})

	w.ShowAndRun()
}

// hotkeyLoop polls Ctrl + Left Alt using GetAsyncKeyState
func hotkeyLoop(ctx context.Context, onHotkey func()) {
	log.Println("[hotkeyLoop] 启动")
	defer log.Println("[hotkeyLoop] 退出")

	prev := false
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		ctrl, _, _ := procGetAsyncKeyState.Call(VK_CONTROL)
		alt, _, _ := procGetAsyncKeyState.Call(VK_LMENU)
		both := ctrl&0x8000 != 0 && alt&0x8000 != 0

		if both && !prev {
			onHotkey()
		}
		prev = both

		time.Sleep(50 * time.Millisecond)
	}
}
