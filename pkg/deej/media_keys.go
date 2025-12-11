package deej

import (
	"syscall"
	"unsafe"

	"go.uber.org/zap"
)

var (
	user32           = syscall.NewLazyDLL("user32.dll")
	procSendInput    = user32.NewProc("SendInput")
)

const (
	INPUT_KEYBOARD    = 1
	KEYEVENTF_KEYUP   = 0x0002
	VK_MEDIA_PLAY_PAUSE = 0xB3
	VK_MEDIA_NEXT_TRACK = 0xB0
	VK_MEDIA_PREV_TRACK = 0xB1
)

type keyboardInput struct {
	wVk         uint16
	wScan       uint16
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

type input struct {
	inputType uint32
	ki        keyboardInput
	padding   uint64
}

// MediaController handles media key simulation
type MediaController struct {
	logger *zap.SugaredLogger
}

// NewMediaController creates a new MediaController
func NewMediaController(logger *zap.SugaredLogger) *MediaController {
	return &MediaController{
		logger: logger.Named("media"),
	}
}

// PlayPause simulates pressing the play/pause media key
func (mc *MediaController) PlayPause() error {
	mc.logger.Info("Simulating Play/Pause key press")
	return mc.sendMediaKey(VK_MEDIA_PLAY_PAUSE)
}

// NextTrack simulates pressing the next track media key
func (mc *MediaController) NextTrack() error {
	mc.logger.Info("Simulating Next Track key press")
	return mc.sendMediaKey(VK_MEDIA_NEXT_TRACK)
}

// PrevTrack simulates pressing the previous track media key
func (mc *MediaController) PrevTrack() error {
	mc.logger.Info("Simulating Previous Track key press")
	return mc.sendMediaKey(VK_MEDIA_PREV_TRACK)
}

func (mc *MediaController) sendMediaKey(vk uint16) error {
	// Key down
	inputDown := input{
		inputType: INPUT_KEYBOARD,
		ki: keyboardInput{
			wVk: vk,
		},
	}

	// Key up
	inputUp := input{
		inputType: INPUT_KEYBOARD,
		ki: keyboardInput{
			wVk:     vk,
			dwFlags: KEYEVENTF_KEYUP,
		},
	}

	inputs := []input{inputDown, inputUp}

	ret, _, _ := procSendInput.Call(
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&inputs[0])),
		uintptr(unsafe.Sizeof(inputs[0])),
	)

	if ret == 0 {
		mc.logger.Warn("SendInput returned 0, key press may have failed")
	}

	return nil
}
