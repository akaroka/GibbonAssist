// Package stt provides speech-to-text via whisper.cpp.
package stt

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Whisper handles speech-to-text using whisper-cli.exe.
type Whisper struct {
	exePath   string // full path to whisper-cli.exe
	workDir   string // directory containing DLLs (for cmd.Dir)
	modelPath string // full path to ggml model
	language  string // language code: auto, zh, en
}

// NewWhisper creates a Whisper engine.
//
//   - workDir: directory containing whisper-cli.exe + DLLs
//   - modelPath: full path to ggml model
func NewWhisper(workDir, modelPath string) *Whisper {
	return &Whisper{
		exePath:   filepath.Join(workDir, "whisper-cli.exe"),
		workDir:   workDir,
		modelPath: modelPath,
		language:  "auto",
	}
}

// SetLanguage sets transcription language ("auto", "zh", "en", etc.).
func (w *Whisper) SetLanguage(lang string) {
	w.language = lang
}

// Transcribe runs whisper on the given WAV file and returns transcribed text.
func (w *Whisper) Transcribe(wavPath string) (string, error) {
	cmd := exec.Command(w.exePath,
		"-m", w.modelPath,
		"-f", wavPath,
		"-np",                // no progress output
		"--no-timestamps",    // text only, no timestamps
		"-l", w.language,
	)
	cmd.Dir = w.workDir // DLLs loaded from here
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return "", fmt.Errorf("whisper failed: %s: %w", errMsg, err)
		}
		return "", fmt.Errorf("whisper failed: %w", err)
	}

	text := strings.TrimSpace(stdout.String())
	if text == "" {
		// try stderr as fallback (sometimes whisper outputs there)
		text = strings.TrimSpace(stderr.String())
	}
	return text, nil
}
