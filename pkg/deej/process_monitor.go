package deej

import (
	"strings"
	"time"

	ps "github.com/mitchellh/go-ps"
	"go.uber.org/zap"
)

const (
	processCheckInterval = 2 * time.Second
)

// ProcessMonitor checks if mapped applications are running and updates LED states
type ProcessMonitor struct {
	deej   *Deej
	serial *SerialIO
	logger *zap.SugaredLogger

	stopChannel     chan bool
	lastKnownStates map[int]bool
	numSliders      int
}

// NewProcessMonitor creates a new ProcessMonitor instance
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

// Start begins monitoring processes and updating LED states
func (pm *ProcessMonitor) Start() {
	pm.logger.Debug("Starting process monitor")

	go pm.monitorLoop()
}

// Stop signals the process monitor to stop
func (pm *ProcessMonitor) Stop() {
	pm.logger.Debug("Stopping process monitor")
	pm.stopChannel <- true
}

func (pm *ProcessMonitor) monitorLoop() {
	processTicker := time.NewTicker(processCheckInterval)
	defer processTicker.Stop()

	// Set up LED refresh ticker if configured
	var refreshTicker *time.Ticker
	var refreshChan <-chan time.Time

	refreshInterval := pm.deej.config.LEDRefreshInterval
	if refreshInterval > 0 {
		refreshTicker = time.NewTicker(refreshInterval)
		refreshChan = refreshTicker.C
		defer refreshTicker.Stop()
		pm.logger.Infow("LED refresh enabled", "interval", refreshInterval)
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

func (pm *ProcessMonitor) checkProcesses() {
	// Get list of all running processes
	processes, err := ps.Processes()
	if err != nil {
		pm.logger.Warnw("Failed to enumerate processes", "error", err)
		return
	}

	// Build a set of running process names (lowercase for case-insensitive matching)
	runningProcesses := make(map[string]bool)
	for _, p := range processes {
		runningProcesses[strings.ToLower(p.Executable())] = true
	}

	// Check each slider mapping
	pm.deej.config.SliderMapping.iterate(func(sliderID int, targets []string) {
		running := pm.isAnyTargetRunning(targets, runningProcesses)

		// Track highest slider ID for refresh
		if sliderID >= pm.numSliders {
			pm.numSliders = sliderID + 1
		}

		// Only send update if state changed
		if lastState, exists := pm.lastKnownStates[sliderID]; !exists || lastState != running {
			pm.lastKnownStates[sliderID] = running

			if err := pm.serial.SendLEDState(sliderID, running); err != nil {
				if pm.deej.Verbose() {
					pm.logger.Warnw("Failed to update LED state", "sliderID", sliderID, "error", err)
				}
			} else {
				pm.logger.Infow("LED state changed", "sliderID", sliderID, "on", running)
			}
		}
	})
}

func (pm *ProcessMonitor) refreshAllLEDs() {
	if pm.numSliders == 0 {
		return
	}

	if err := pm.serial.SendAllLEDStates(pm.lastKnownStates, pm.numSliders); err != nil {
		if pm.deej.Verbose() {
			pm.logger.Warnw("Failed to refresh LED states", "error", err)
		}
	} else if pm.deej.Verbose() {
		pm.logger.Debug("Refreshed all LED states")
	}
}

func (pm *ProcessMonitor) isAnyTargetRunning(targets []string, runningProcesses map[string]bool) bool {
	for _, target := range targets {
		targetLower := strings.ToLower(target)

		// Special cases
		switch targetLower {
		case masterSessionName:
			// Master volume always exists
			return true
		case inputSessionName:
			// Mic always exists if there's a recording device
			return true
		case systemSessionName:
			// System sounds always exist on Windows
			return true
		case specialTargetTransformPrefix + specialTargetAllUnmapped,
			specialTargetTransformPrefix + specialTargetCurrentWindow:
			// These don't map to specific processes
			return false
		}

		// Check if this process is running
		if runningProcesses[targetLower] {
			return true
		}
	}

	return false
}
