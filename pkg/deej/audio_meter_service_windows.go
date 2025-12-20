// +build windows

package deej

import (
	"errors"
	"strings"
	"unsafe"

	ole "github.com/go-ole/go-ole"
	ps "github.com/mitchellh/go-ps"
	wca "github.com/moutend/go-wca"
	"go.uber.org/zap"
)

// AudioMeterService queries Windows Core Audio API to detect which applications
// are currently outputting audio. This is used to drive LED indicators based on
// actual audio activity rather than just process presence.
type AudioMeterService struct {
	logger *zap.SugaredLogger
}

// ProcessAudioLevel represents the audio level for a process.
type ProcessAudioLevel struct {
	ProcessName string
	PeakValue   float32
	IsActive    bool // true if peak > threshold
}

const (
	// audioActiveThreshold is the minimum peak level to consider audio "active".
	// Values below this are treated as silence (handles noise floor).
	audioActiveThreshold = 0.001
)

// NewAudioMeterService creates a new AudioMeterService instance.
func NewAudioMeterService(logger *zap.SugaredLogger) *AudioMeterService {
	return &AudioMeterService{
		logger: logger.Named("audio-meter"),
	}
}

// GetActiveAudioProcesses returns a map of process names (lowercase) that are
// currently outputting audio above the threshold. It enumerates all audio
// endpoints and their sessions, querying the peak meter for each.
func (ams *AudioMeterService) GetActiveAudioProcesses() (map[string]bool, error) {
	levels, err := ams.GetAudioPeakLevels()
	if err != nil {
		return nil, err
	}

	activeProcesses := make(map[string]bool)
	for name, level := range levels {
		if level > audioActiveThreshold {
			activeProcesses[name] = true
		}
	}
	return activeProcesses, nil
}

// GetAudioPeakLevels returns a map of process names (lowercase) to their current
// peak audio levels (0.0-1.0). It enumerates all audio endpoints and their sessions.
func (ams *AudioMeterService) GetAudioPeakLevels() (map[string]float32, error) {
	peakLevels := make(map[string]float32)

	// Initialize COM for this goroutine
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		oleError := &ole.OleError{}
		// Code 1 = S_FALSE (already initialized) - this is fine
		if errors.As(err, &oleError) && oleError.Code() != 1 {
			ams.logger.Warnw("COM init failed", "error", err)
			return nil, err
		}
	}
	defer ole.CoUninitialize()

	// Get the device enumerator
	var mmDeviceEnumerator *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(
		wca.CLSID_MMDeviceEnumerator,
		0,
		wca.CLSCTX_ALL,
		wca.IID_IMMDeviceEnumerator,
		&mmDeviceEnumerator,
	); err != nil {
		ams.logger.Warnw("Failed to create device enumerator", "error", err)
		return nil, err
	}
	defer mmDeviceEnumerator.Release()

	// Enumerate active render (output) devices
	var deviceCollection *wca.IMMDeviceCollection
	if err := mmDeviceEnumerator.EnumAudioEndpoints(wca.ERender, wca.DEVICE_STATE_ACTIVE, &deviceCollection); err != nil {
		ams.logger.Warnw("Failed to enumerate audio endpoints", "error", err)
		return nil, err
	}
	defer deviceCollection.Release()

	var deviceCount uint32
	if err := deviceCollection.GetCount(&deviceCount); err != nil {
		ams.logger.Warnw("Failed to get device count", "error", err)
		return nil, err
	}

	ams.logger.Debugw("Scanning audio devices", "count", deviceCount)

	// Query each device's audio sessions for peak levels
	for deviceIdx := uint32(0); deviceIdx < deviceCount; deviceIdx++ {
		var endpoint *wca.IMMDevice
		if err := deviceCollection.Item(deviceIdx, &endpoint); err != nil {
			continue
		}
		ams.queryDeviceSessionLevels(endpoint, peakLevels)
		endpoint.Release()
	}

	// Log peak levels at Debug level (only when there are some)
	if len(peakLevels) > 0 {
		ams.logger.Debugw("Audio peak levels", "levels", peakLevels)
	}

	return peakLevels, nil
}

// queryDeviceSessions enumerates all audio sessions on a device and checks
// their peak audio levels, adding active processes to the map.
func (ams *AudioMeterService) queryDeviceSessions(endpoint *wca.IMMDevice, activeProcesses map[string]bool) {
	// Get session manager for this device
	var audioSessionManager2 *wca.IAudioSessionManager2
	if err := endpoint.Activate(
		wca.IID_IAudioSessionManager2,
		wca.CLSCTX_ALL,
		nil,
		&audioSessionManager2,
	); err != nil {
		return // Some devices don't support session enumeration
	}
	defer audioSessionManager2.Release()

	// Get session enumerator
	var sessionEnumerator *wca.IAudioSessionEnumerator
	if err := audioSessionManager2.GetSessionEnumerator(&sessionEnumerator); err != nil {
		return
	}
	defer sessionEnumerator.Release()

	var sessionCount int
	if err := sessionEnumerator.GetCount(&sessionCount); err != nil {
		return
	}

	// Query each session
	for sessionIdx := 0; sessionIdx < sessionCount; sessionIdx++ {
		ams.querySession(sessionEnumerator, sessionIdx, activeProcesses)
	}
}

// querySession checks a single audio session's peak level and adds the process
// to activeProcesses if it's above the threshold.
func (ams *AudioMeterService) querySession(sessionEnumerator *wca.IAudioSessionEnumerator, sessionIdx int, activeProcesses map[string]bool) {
	var audioSessionControl *wca.IAudioSessionControl
	if err := sessionEnumerator.GetSession(sessionIdx, &audioSessionControl); err != nil {
		return
	}

	// Get IAudioSessionControl2 for process ID
	dispatch, err := audioSessionControl.QueryInterface(wca.IID_IAudioSessionControl2)
	if err != nil {
		audioSessionControl.Release()
		return
	}
	audioSessionControl.Release()

	audioSessionControl2 := (*wca.IAudioSessionControl2)(unsafe.Pointer(dispatch))
	defer audioSessionControl2.Release()

	// Get process ID
	var pid uint32
	audioSessionControl2.GetProcessId(&pid)

	if pid == 0 {
		// System sounds session - skip for LED purposes
		return
	}

	// Look up process name
	process, err := ps.FindProcess(int(pid))
	if err != nil || process == nil {
		return
	}
	processName := strings.ToLower(process.Executable())

	// Query IAudioMeterInformation for peak level
	meterDispatch, err := audioSessionControl2.QueryInterface(IID_IAudioMeterInformation)
	if err != nil {
		return
	}

	audioMeter := (*IAudioMeterInformation)(unsafe.Pointer(meterDispatch))
	defer audioMeter.Release()

	// Get peak value (0.0 to 1.0)
	peak, err := audioMeter.GetPeakValue()
	if err != nil {
		ams.logger.Warnw("Failed to get peak value", "process", processName, "error", err)
		return
	}

	ams.logger.Debugw("Peak level", "process", processName, "peak", peak)

	if peak > audioActiveThreshold {
		activeProcesses[processName] = true
	}
}

// queryDeviceSessionLevels enumerates all audio sessions on a device and gets
// their peak audio levels, storing them in the peakLevels map.
func (ams *AudioMeterService) queryDeviceSessionLevels(endpoint *wca.IMMDevice, peakLevels map[string]float32) {
	var audioSessionManager2 *wca.IAudioSessionManager2
	if err := endpoint.Activate(
		wca.IID_IAudioSessionManager2,
		wca.CLSCTX_ALL,
		nil,
		&audioSessionManager2,
	); err != nil {
		return
	}
	defer audioSessionManager2.Release()

	var sessionEnumerator *wca.IAudioSessionEnumerator
	if err := audioSessionManager2.GetSessionEnumerator(&sessionEnumerator); err != nil {
		return
	}
	defer sessionEnumerator.Release()

	var sessionCount int
	if err := sessionEnumerator.GetCount(&sessionCount); err != nil {
		return
	}

	for sessionIdx := 0; sessionIdx < sessionCount; sessionIdx++ {
		ams.querySessionLevel(sessionEnumerator, sessionIdx, peakLevels)
	}
}

// querySessionLevel gets a single audio session's peak level and stores it.
func (ams *AudioMeterService) querySessionLevel(sessionEnumerator *wca.IAudioSessionEnumerator, sessionIdx int, peakLevels map[string]float32) {
	var audioSessionControl *wca.IAudioSessionControl
	if err := sessionEnumerator.GetSession(sessionIdx, &audioSessionControl); err != nil {
		return
	}

	dispatch, err := audioSessionControl.QueryInterface(wca.IID_IAudioSessionControl2)
	if err != nil {
		audioSessionControl.Release()
		return
	}
	audioSessionControl.Release()

	audioSessionControl2 := (*wca.IAudioSessionControl2)(unsafe.Pointer(dispatch))
	defer audioSessionControl2.Release()

	var pid uint32
	audioSessionControl2.GetProcessId(&pid)

	if pid == 0 {
		return
	}

	process, err := ps.FindProcess(int(pid))
	if err != nil || process == nil {
		return
	}
	processName := strings.ToLower(process.Executable())

	meterDispatch, err := audioSessionControl2.QueryInterface(IID_IAudioMeterInformation)
	if err != nil {
		return
	}

	audioMeter := (*IAudioMeterInformation)(unsafe.Pointer(meterDispatch))
	defer audioMeter.Release()

	peak, err := audioMeter.GetPeakValue()
	if err != nil {
		return
	}

	// Keep highest peak if process has multiple sessions
	if existing, ok := peakLevels[processName]; !ok || peak > existing {
		peakLevels[processName] = peak
	}
}
