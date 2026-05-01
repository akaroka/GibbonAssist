package main

import (
	"context"
	"encoding/json"
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
	"GibbonAss/clip"
	"GibbonAss/llm"
	"GibbonAss/stt"
)

// Windows API
var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procGetAsyncKeyState = user32.NewProc("GetAsyncKeyState")
)

const (
	VK_CONTROL = 0x11
	VK_LMENU   = 0xA4
	VK_ESCAPE  = 0x1B
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

	// --- whisper engine ---
	whisperDir := filepath.Join(exeDir, "whisper")
	modelPath := filepath.Join(exeDir, "models", "ggml.bin")

	whisperEngine := stt.NewWhisper(whisperDir, modelPath)
	whisperEngine.SetLanguage("auto")
	log.Printf("Whisper 引擎初始化: dir=%s, model=%s", whisperDir, modelPath)

	// --- LLM polisher ---
	cfgPath := filepath.Join(exeDir, "config.json")
	polisher := initPolisher(cfgPath)

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

			// --- transcribe + polish + output in background ---
			curr = stateProcessing
			wavPathCopy := wavPath
			go func() {
				log.Printf("[stt] 开始转写: %s", wavPathCopy)
				text, err := whisperEngine.Transcribe(wavPathCopy)
				if err != nil {
					log.Printf("[stt] 转写失败: %v", err)
				} else {
					log.Printf("[stt] 转写结果: %q", text)
					log.Printf("[stt] 转写字符数: %d", len(text))

					// LLM 润色（如果已配置）
					if polisher.IsConfigured() {
						log.Printf("[llm] 开始润色...")
						polished, err := polisher.Polish(text)
						if err != nil {
							log.Printf("[llm] 润色失败: %v（使用原文）", err)
						} else {
							log.Printf("[llm] 润色结果: %q", polished)
							text = polished
						}
					} else {
						log.Printf("[llm] 未配置（llm_url/llm_key 为空），跳过润色")
					}

					// --- clipboard + paste ---
					if text != "" {
						log.Printf("[clip] 保存当前剪贴板内容")
						prevClip, err := clip.GetText()
						if err != nil {
							log.Printf("[clip] 读取剪贴板失败: %v（继续）", err)
						}

						log.Printf("[clip] 写入转写结果到剪贴板")
						if err := clip.SetText(text); err != nil {
							log.Printf("[clip] 写入剪贴板失败: %v", err)
						} else {
							log.Printf("[clip] 模拟 Ctrl+V 粘贴...")
							clip.SendCtrlV()

							// 等待粘贴完成，然后恢复原剪贴板
							time.Sleep(150 * time.Millisecond)
							if prevClip != "" {
								if err := clip.SetText(prevClip); err != nil {
									log.Printf("[clip] 恢复原剪贴板失败: %v", err)
								} else {
									log.Printf("[clip] 已恢复原剪贴板内容 (%d 字符)", len(prevClip))
								}
							}
						}
					}
				}

				fyne.Do(func() {
					if curr == stateProcessing {
						curr = stateIdle
						recordBtn.SetText("开始录音")
						recordBtn.Importance = widget.MediumImportance
						recordBtn.Refresh()
					}
				})
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

	// --- global hotkey: Ctrl + Left Alt (polling) + ESC cancel ---
	log.Println("启动热键监听 goroutine")
	ctx, cancel := context.WithCancel(context.Background())

	// ESC 取消录音
	cancelRecording := func() {
		log.Println("[hotkey] ESC 触发")
		if curr != stateRecording || rec == nil {
			return
		}
		log.Println("[hotkey] ESC 取消录音，丢弃音频")
		rec.Stop() // 释放 waveIn 设备，丢弃 PCM
		rec = nil
		curr = stateIdle
		recordBtn.SetText("开始录音")
		recordBtn.Importance = widget.MediumImportance
		recordBtn.Refresh()
	}

	go hotkeyLoop(ctx, func() {
		log.Println("onHotkey 回调执行")
		fyne.Do(toggle)
	}, func() {
		fyne.Do(cancelRecording)
	})

	w.SetCloseIntercept(func() {
		cancel()
		w.Close()
	})

	w.ShowAndRun()
}

// initPolisher loads config.json and creates an LLM polisher.
func initPolisher(cfgPath string) *llm.Polisher {
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		log.Printf("[llm] 无法读取配置文件 %s: %v（跳过润色）", cfgPath, err)
		return llm.NewPolisher(llm.Config{})
	}

	var cfg struct {
		URL    string `json:"llm_url"`
		Key    string `json:"llm_key"`
		Model  string `json:"llm_model"`
		Prompt string `json:"llm_prompt"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		log.Printf("[llm] 配置文件解析失败: %v（跳过润色）", err)
		return llm.NewPolisher(llm.Config{})
	}

	p := llm.NewPolisher(llm.Config{
		URL:    cfg.URL,
		Key:    cfg.Key,
		Model:  cfg.Model,
		Prompt: cfg.Prompt,
	})
	if p.IsConfigured() {
		log.Printf("[llm] 润色引擎已配置: model=%s, url=%s", cfg.Model, cfg.URL)
	} else {
		log.Printf("[llm] 润色引擎未配置（llm_url/llm_key 为空）")
	}
	return p
}

// hotkeyLoop polls Ctrl+Left Alt for toggle, ESC for cancel.
func hotkeyLoop(ctx context.Context, onHotkey func(), onCancel func()) {
	log.Println("[hotkeyLoop] 启动")
	defer log.Println("[hotkeyLoop] 退出")

	prevHotkey := false
	prevEsc := false
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Ctrl + Left Alt
		ctrl, _, _ := procGetAsyncKeyState.Call(VK_CONTROL)
		alt, _, _ := procGetAsyncKeyState.Call(VK_LMENU)
		both := ctrl&0x8000 != 0 && alt&0x8000 != 0
		if both && !prevHotkey {
			onHotkey()
		}
		prevHotkey = both

		// ESC (录音中取消)
		esc, _, _ := procGetAsyncKeyState.Call(VK_ESCAPE)
		escDown := esc&0x8000 != 0
		if escDown && !prevEsc {
			onCancel()
		}
		prevEsc = escDown

		time.Sleep(50 * time.Millisecond)
	}
}
