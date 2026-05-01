package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"os"
	"syscall"
	"unsafe"
)

// --- Windows API ---

var (
	winmm                   = syscall.NewLazyDLL("winmm.dll")
	procWaveInGetNumDevs    = winmm.NewProc("waveInGetNumDevs")
	procWaveInGetDevCapsW   = winmm.NewProc("waveInGetDevCapsW")
	procWaveInOpen          = winmm.NewProc("waveInOpen")
	procWaveInClose         = winmm.NewProc("waveInClose")
	procWaveInPrepareHeader = winmm.NewProc("waveInPrepareHeader")
	procWaveInUnprepareHdr  = winmm.NewProc("waveInUnprepareHeader")
	procWaveInAddBuffer     = winmm.NewProc("waveInAddBuffer")
	procWaveInStart         = winmm.NewProc("waveInStart")
	procWaveInStop          = winmm.NewProc("waveInStop")
	procWaveInReset         = winmm.NewProc("waveInReset")

	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	procGlobalAlloc = kernel32.NewProc("GlobalAlloc")
	procGlobalFree  = kernel32.NewProc("GlobalFree")
)

// --- constants ---

const (
	CALLBACK_NULL   = 0
	WAVE_FORMAT_PCM = 1
	WHDR_DONE       = 0x00000001
	GMEM_FIXED      = 0x0000

	SampleRate    = 16000
	NumChannels   = 1
	BitsPerSample = 16
	BytesPerFrame = NumChannels * BitsPerSample / 8 // 2

	// No poll loop: N fixed buffers cover entire recording.
	// 32 buffers × 2s = 64s max recording (enough for PoC).
	bufferSeconds = 2
	numBuffers    = 32
	bufferBytes   = SampleRate * BytesPerFrame * bufferSeconds // 64000
)

// --- structs (Windows ABI) ---

type _WAVEFORMATEX struct {
	wFormatTag      uint16
	nChannels       uint16
	nSamplesPerSec  uint32
	nAvgBytesPerSec uint32
	nBlockAlign     uint16
	wBitsPerSample  uint16
	cbSize          uint16
}

func newPCMFormat(sr uint32) _WAVEFORMATEX {
	return _WAVEFORMATEX{
		wFormatTag:      WAVE_FORMAT_PCM,
		nChannels:       NumChannels,
		nSamplesPerSec:  sr,
		nAvgBytesPerSec: sr * BytesPerFrame,
		nBlockAlign:     BytesPerFrame,
		wBitsPerSample:  BitsPerSample,
		cbSize:          0,
	}
}

type _WAVEHDR struct {
	lpData          uintptr
	dwBufferLength  uint32
	dwBytesRecorded uint32
	dwUser          uintptr
	dwFlags         uint32
	dwLoops         uint32
	lpNext          uintptr
	reserved        uintptr
}

type _WAVEINCAPSW struct {
	wMid            uint16
	wPid            uint16
	vDriverVersion  uint32
	szPname         [64]byte
	dwFormats       uint32
	wChannels       uint16
	wReserved1      uint16
}

// --- Device ---

type Device struct {
	ID   int
	Name string
}

// EnumerateDevices returns all available audio input devices.
func EnumerateDevices() ([]Device, error) {
	count, _, _ := procWaveInGetNumDevs.Call()
	if count == 0 {
		return nil, nil
	}
	devices := make([]Device, 0, int(count))
	for i := 0; i < int(count); i++ {
		var caps _WAVEINCAPSW
		ret, _, _ := procWaveInGetDevCapsW.Call(
			uintptr(i),
			uintptr(unsafe.Pointer(&caps)),
			uintptr(unsafe.Sizeof(caps)),
		)
		if ret != 0 {
			continue
		}
		u16 := (*[32]uint16)(unsafe.Pointer(&caps.szPname[0]))
		name := syscall.UTF16ToString(u16[:])
		if name == "" {
			name = fmt.Sprintf("麦克风 %d", i)
		}
		devices = append(devices, Device{ID: i, Name: name})
	}
	return devices, nil
}

// --- Recorder ---

// Recorder captures audio using Windows waveIn API.
// No background goroutine: buffers are pre-allocated and filled by the driver.
// Stop() returns all captured data synchronously.
type Recorder struct {
	devID int
	handle uintptr // HWAVEIN
	hdrs   []_WAVEHDR
	bufPtrs []uintptr // GlobalAlloc pointers for cleanup
}

// NewRecorder creates a recorder for the given device ID.
func NewRecorder(devID int) *Recorder {
	return &Recorder{devID: devID}
}

// Start begins capturing audio.
func (r *Recorder) Start() error {
	wf := newPCMFormat(SampleRate)

	ret, _, _ := procWaveInOpen.Call(
		uintptr(unsafe.Pointer(&r.handle)),
		uintptr(r.devID),
		uintptr(unsafe.Pointer(&wf)),
		0, 0, CALLBACK_NULL,
	)
	if ret != 0 {
		r.handle = 0
		return fmt.Errorf("waveInOpen failed: err=%d", ret)
	}

	// Allocate fixed buffers via GlobalAlloc (no GC interaction)
	r.hdrs = make([]_WAVEHDR, numBuffers)
	r.bufPtrs = make([]uintptr, numBuffers)

	for i := range r.hdrs {
		buf, _, _ := procGlobalAlloc.Call(GMEM_FIXED, bufferBytes)
		if buf == 0 {
			r.cleanup()
			return fmt.Errorf("GlobalAlloc[%d] failed", i)
		}
		r.bufPtrs[i] = buf
		r.hdrs[i] = _WAVEHDR{
			lpData:         buf,
			dwBufferLength: bufferBytes,
		}
	}

	// Prepare and queue all buffers
	for i := range r.hdrs {
		ret, _, _ = procWaveInPrepareHeader.Call(
			r.handle, uintptr(unsafe.Pointer(&r.hdrs[i])), unsafe.Sizeof(r.hdrs[i]),
		)
		if ret != 0 {
			r.cleanup()
			return fmt.Errorf("waveInPrepareHeader[%d] failed: err=%d", i, ret)
		}
		ret, _, _ = procWaveInAddBuffer.Call(
			r.handle, uintptr(unsafe.Pointer(&r.hdrs[i])), unsafe.Sizeof(r.hdrs[i]),
		)
		if ret != 0 {
			r.cleanup()
			return fmt.Errorf("waveInAddBuffer[%d] failed: err=%d", i, ret)
		}
	}

	// Start
	ret, _, _ = procWaveInStart.Call(r.handle)
	if ret != 0 {
		r.cleanup()
		return fmt.Errorf("waveInStart failed: err=%d", ret)
	}
	return nil
}

// Stop halts recording, reads all buffers, and returns 16-bit mono 16kHz PCM.
func (r *Recorder) Stop() ([]byte, error) {
	if r.handle == 0 {
		return nil, errors.New("recorder not started")
	}

	// Stop + reset returns all pending buffers with WHDR_DONE set
	procWaveInStop.Call(r.handle)
	procWaveInReset.Call(r.handle)

	// Collect data from all done buffers
	var data []byte
	for i := range r.hdrs {
		if r.hdrs[i].dwFlags&WHDR_DONE != 0 {
			n := r.hdrs[i].dwBytesRecorded
			if n > 0 && r.hdrs[i].lpData != 0 {
				src := unsafe.Slice((*byte)(unsafe.Pointer(r.hdrs[i].lpData)), int(n))
				data = append(data, src...)
			}
		}
	}

	// Diagnostic: log PCM data quality
	if len(data) > 0 {
		samples := len(data) / 2
		maxVal := int16(0)
		nonzero := 0
		for i := 0; i < len(data)-1; i += 2 {
			s := int16(data[i]) | int16(data[i+1])<<8
			if s < 0 {
				s = -s
			}
			if s > maxVal {
				maxVal = s
			}
			if s > 100 {
				nonzero++
			}
		}
		log.Printf("[audio] PCM 诊断: 采样=%d, 峰值=%d, 非静音帧=%d (%.1f%%)",
			samples, maxVal, nonzero, float64(nonzero)/float64(samples)*100)
		if len(data) >= 8 {
			s0 := int16(data[0]) | int16(data[1])<<8
			s1 := int16(data[2]) | int16(data[3])<<8
			s2 := int16(data[4]) | int16(data[5])<<8
			s3 := int16(data[6]) | int16(data[7])<<8
			log.Printf("[audio] 前4采样值: %d %d %d %d", s0, s1, s2, s3)
		}
	} else {
		log.Println("[audio] PCM 诊断: 数据为空!")
	}

	r.cleanup()
	return data, nil
}

func (r *Recorder) cleanup() {
	for i := range r.hdrs {
		if r.hdrs[i].lpData != 0 {
			procWaveInUnprepareHdr.Call(
				r.handle, uintptr(unsafe.Pointer(&r.hdrs[i])), unsafe.Sizeof(r.hdrs[i]),
			)
		}
	}
	for _, p := range r.bufPtrs {
		if p != 0 {
			procGlobalFree.Call(p)
		}
	}
	r.hdrs = nil
	r.bufPtrs = nil
	if r.handle != 0 {
		procWaveInClose.Call(r.handle)
		r.handle = 0
	}
}

// --- WAV writer ---

// WriteWAV writes 16-bit mono 16kHz PCM data as a WAV file.
func WriteWAV(path string, pcm []byte) error {
	if len(pcm)%2 != 0 {
		return errors.New("PCM data not 16-bit aligned")
	}
	ds := len(pcm)
	rs := 36 + ds

	sr := uint32(SampleRate)
	bf := uint16(BytesPerFrame)
	bs := uint16(BitsPerSample)
	br := sr * uint32(bf)

	hdr := make([]byte, 44)
	copy(hdr[0:4], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(rs))
	copy(hdr[8:12], "WAVE")
	copy(hdr[12:16], "fmt ")
	binary.LittleEndian.PutUint32(hdr[16:20], 16) // chunk size
	binary.LittleEndian.PutUint16(hdr[20:22], 1)  // PCM
	binary.LittleEndian.PutUint16(hdr[22:24], 1)  // mono
	binary.LittleEndian.PutUint32(hdr[24:28], sr) // sample rate
	binary.LittleEndian.PutUint32(hdr[28:32], br) // byte rate
	binary.LittleEndian.PutUint16(hdr[32:34], bf) // block align
	binary.LittleEndian.PutUint16(hdr[34:36], bs) // bits per sample
	copy(hdr[36:40], "data")
	binary.LittleEndian.PutUint32(hdr[40:44], uint32(ds))

	out, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	if _, err = out.Write(hdr); err != nil {
		out.Close()
		return fmt.Errorf("write header: %w", err)
	}
	if _, err = out.Write(pcm); err != nil {
		out.Close()
		return fmt.Errorf("write data: %w", err)
	}
	out.Close()

	// Readback validation
	rb := make([]byte, 44)
	fd, err := syscall.Open(path, syscall.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("readback open: %w", err)
	}
	if _, err = syscall.Read(fd, rb); err != nil {
		syscall.Close(fd)
		return fmt.Errorf("readback read: %w", err)
	}
	syscall.Close(fd)
	if string(rb[0:4]) != "RIFF" || string(rb[8:12]) != "WAVE" {
		return fmt.Errorf("WAV header invalid: RIFF=%q WAVE=%q", rb[0:4], rb[8:12])
	}
	log.Printf("[audio] WAV 验证通过: %s (%d Hz, %d-bit, mono)", path, sr, bs)
	// Hex dump first 44 bytes
	fd2, err := os.Open(path)
	if err == nil {
		raw := make([]byte, 44)
		n, _ := fd2.Read(raw)
		fd2.Close()
		if n == 44 {
			line := ""
			for i, b := range raw {
				line += fmt.Sprintf("%02X ", b)
				if (i+1)%16 == 0 || i == n-1 {
					log.Printf("[audio] WAV hex: %s", line)
					line = ""
				}
			}
		}
	}
	return nil
}
