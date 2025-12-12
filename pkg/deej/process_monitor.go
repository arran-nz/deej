package deej

import (
	"strings"
	"time"

	ps "github.com/mitchellh/go-ps"
	"go.uber.org/zap"
)

const (
	// processCheckInterval is how often to check for running processes (process mode)
	processCheckInterval = 2 * time.Second

	// audioMeterCheckInterval is how often to poll audio levels (audio mode).
	// Faster polling since audio can start/stop quickly.
	audioMeterCheckInterval = 500 * time.Millisecond
)

// ProcessMonitor checks if mapped applications are running (process mode) or
// outputting audio (audio mode) and updates LED states accordingly.
type ProcessMonitor struct {
	deej   *Deej
	serial *SerialIO
	logger *zap.SugaredLogger

	audioMeter *AudioMeterService

	stopChannel     chan bool
	lastKnownStates map[int]bool
	numSliders      int
}

// NewProcessMonitor creates a new ProcessMonitor instance.
// Note: AudioMeterService is created in Start() after config is loaded.
func NewProcessMonitor(deej *Deej, serial *SerialIO, logger *zap.SugaredLogger) *ProcessMonitor {
	logger = logger.Named("process-monitor")

	return &ProcessMonitor{
		deej:            deej,
		serial:          serial,
		logger:          logger,
		stopChannel:     make(chan bool),
		lastKnownStates: make(map[int]bool),
	}
}

// Start begins monitoring processes and updating LED states.
func (pm *ProcessMonitor) Start() {
	pm.logger.Debug("Starting process monitor")

	// Create audio meter service if in audio mode.
	// This must be done here (not in constructor) because config is loaded
	// in Initialize() which runs after NewProcessMonitor().
	if pm.deej.config.LEDMode == LEDModeAudio {
		pm.logger.Info("Audio mode enabled - LEDs will track audio output")
		pm.audioMeter = NewAudioMeterService(pm.logger)
	} else {
		pm.logger.Info("Process mode enabled - LEDs will track running processes")
	}

	go pm.monitorLoop()
}

// Stop signals the process monitor to stop.
func (pm *ProcessMonitor) Stop() {
	pm.logger.Debug("Stopping process monitor")
	pm.stopChannel <- true
}

func (pm *ProcessMonitor) monitorLoop() {
	// Select polling interval based on mode
	checkInterval := processCheckInterval
	if pm.deej.config.LEDMode == LEDModeAudio {
		checkInterval = audioMeterCheckInterval
	}
	pm.logger.Debugw("Monitor loop started", "checkInterval", checkInterval)

	processTicker := time.NewTicker(checkInterval)
	defer processTicker.Stop()

	// Set up LED refresh ticker if configured
	var refreshTicker *time.Ticker
	var refreshChan <-chan time.Time

	refreshInterval := pm.deej.config.LEDRefreshInterval
	if refreshInterval > 0 {
		refreshTicker = time.NewTicker(refreshInterval)
		refreshChan = refreshTicker.C
		defer refreshTicker.Stop()
		pm.logger.Debugw("LED refresh enabled", "interval", refreshInterval)
	}

	// Initial check
	pm.checkProcesses()

	for {
		select {
		case <-pm.stopChannel:
			pm.logger.Debug("Process monitor stopped")
			return
		case <-processTicker.C:
			pm.checkProcesses()
		case <-refreshChan:
			pm.refreshAllLEDs()
		}
	}
}

// checkProcesses queries active processes/audio and updates LED states.
func (pm *ProcessMonitor) checkProcesses() {
	var activeProcesses map[string]bool

	if pm.audioMeter != nil {
		// Audio mode: check which processes are outputting audio
		var err error
		activeProcesses, err = pm.audioMeter.GetActiveAudioProcesses()
		if err != nil {
			if pm.deej.Verbose() {
				pm.logger.Warnw("Failed to get active audio processes", "error", err)
			}
			return
		}
	} else {
		// Process mode: check which processes are running
		processes, err := ps.Processes()
		if err != nil {
			pm.logger.Warnw("Failed to enumerate processes", "error", err)
			return
		}

		activeProcesses = make(map[string]bool)
		for _, p := range processes {
			activeProcesses[strings.ToLower(p.Executable())] = true
		}
	}

	// Check each slider mapping and update LED state if changed
	pm.deej.config.SliderMapping.iterate(func(sliderID int, targets []string) {
		active := pm.isAnyTargetActive(targets, activeProcesses)

		// Track highest slider ID for batched refresh
		if sliderID >= pm.numSliders {
			pm.numSliders = sliderID + 1
		}

		// Only send update if state changed
		if lastState, exists := pm.lastKnownStates[sliderID]; !exists || lastState != active {
			pm.lastKnownStates[sliderID] = active

			if err := pm.serial.SendLEDState(sliderID, active); err != nil {
				if pm.deej.Verbose() {
					pm.logger.Warnw("Failed to update LED state", "sliderID", sliderID, "error", err)
				}
			} else {
				pm.logger.Infow("LED state changed", "sliderID", sliderID, "on", active)
			}
		}
	})
}

// refreshAllLEDs sends the current state of all LEDs as a batched command.
// This ensures Arduino stays in sync even if individual commands were missed.
func (pm *ProcessMonitor) refreshAllLEDs() {
	if pm.numSliders == 0 {
		return
	}

	if err := pm.serial.SendAllLEDStates(pm.lastKnownStates, pm.numSliders); err != nil {
		if pm.deej.Verbose() {
			pm.logger.Warnw("Failed to refresh LED states", "error", err)
		}
	}
}

// isAnyTargetActive checks if any of the target processes are active.
func (pm *ProcessMonitor) isAnyTargetActive(targets []string, activeProcesses map[string]bool) bool {
	for _, target := range targets {
		targetLower := strings.ToLower(target)

		// In process mode, special sessions are always "active" (they always exist)
		if pm.audioMeter == nil {
			switch targetLower {
			case masterSessionName, inputSessionName, systemSessionName:
				return true
			}
		}

		// Skip unmapped/current window targets - these don't map to specific processes
		switch targetLower {
		case specialTargetTransformPrefix + specialTargetAllUnmapped,
			specialTargetTransformPrefix + specialTargetCurrentWindow:
			return false
		}

		// Check if this process is active
		if activeProcesses[targetLower] {
			return true
		}
	}

	return false
}
