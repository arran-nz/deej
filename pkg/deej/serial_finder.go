package deej

import (
	"strings"
	"time"

	"go.bug.st/serial"
	"go.uber.org/zap"
)

const (
	probeTimeout       = 2 * time.Second
	probeReadTimeout   = 100 * time.Millisecond
	requiredValidLines = 2
)

// findDeejPort enumerates serial ports and returns the first one that speaks the deej protocol.
// Returns empty string if no port is found.
func findDeejPort(logger *zap.SugaredLogger, baudRate int) string {
	ports, err := serial.GetPortsList()
	if err != nil {
		logger.Warnw("Failed to enumerate serial ports", "error", err)
		return ""
	}

	if len(ports) == 0 {
		logger.Debug("No serial ports found")
		return ""
	}

	logger.Debugw("Scanning serial ports", "ports", ports)

	for _, portName := range ports {
		if probePort(logger, portName, baudRate) {
			logger.Infow("Found deej device", "port", portName)
			return portName
		}
	}

	logger.Debug("No deej device found on any port")
	return ""
}

// probePort opens a serial port and checks if it produces deej-protocol data.
// Reads directly from the serial port (no bufio) to avoid hanging on dead ports
// where Read returns (0, nil) on timeout — bufio would retry ~100 times internally.
func probePort(logger *zap.SugaredLogger, portName string, baudRate int) bool {
	mode := &serial.Mode{
		BaudRate: baudRate,
		DataBits: 8,
		StopBits: serial.OneStopBit,
		Parity:   serial.NoParity,
	}

	conn, err := serial.Open(portName, mode)
	if err != nil {
		logger.Debugw("Skipping port (can't open)", "port", portName, "error", err)
		return false
	}
	defer conn.Close()

	// Short per-read timeout so we return quickly from dead ports;
	// the outer deadline bounds total probe time.
	if err := conn.SetReadTimeout(probeReadTimeout); err != nil {
		logger.Debugw("Skipping port (can't set timeout)", "port", portName, "error", err)
		return false
	}

	buf := make([]byte, 256)
	var accumulated string
	validLines := 0
	deadline := time.Now().Add(probeTimeout)

	for time.Now().Before(deadline) {
		n, err := conn.Read(buf)
		if err != nil {
			break
		}
		if n == 0 {
			continue
		}

		accumulated += string(buf[:n])

		// Extract and validate complete lines
		for {
			idx := strings.Index(accumulated, "\n")
			if idx == -1 {
				break
			}
			line := accumulated[:idx+1]
			accumulated = accumulated[idx+1:]

			if expectedLinePattern.MatchString(line) {
				validLines++
				if validLines >= requiredValidLines {
					return true
				}
			}
		}
	}

	return false
}
