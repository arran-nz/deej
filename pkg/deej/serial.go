package deej

import (
	"bufio"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
	"go.uber.org/zap"

	"github.com/omriharel/deej/pkg/deej/util"
)

// SerialIO provides a deej-aware abstraction layer to managing serial I/O
type SerialIO struct {
	comPort  string
	baudRate uint

	deej   *Deej
	logger *zap.SugaredLogger

	stopChannel chan bool
	connected   bool
	connOptions *serial.Mode
	conn        serial.Port
	writeMu     sync.Mutex

	lastKnownNumSliders        int
	currentSliderPercentValues []float32

	sliderMoveConsumers []chan SliderMoveEvent
}

// SliderMoveEvent represents a single slider move captured by deej
type SliderMoveEvent struct {
	SliderID     int
	PercentValue float32
}

var expectedLinePattern = regexp.MustCompile(`^\d{1,4}(\|\d{1,4})*\r\n$`)

// NewSerialIO creates a SerialIO instance that uses the provided deej
// instance's connection info to establish communications with the arduino chip
func NewSerialIO(deej *Deej, logger *zap.SugaredLogger) (*SerialIO, error) {
	logger = logger.Named("serial")

	sio := &SerialIO{
		deej:                deej,
		logger:              logger,
		stopChannel:         make(chan bool),
		connected:           false,
		conn:                nil,
		sliderMoveConsumers: []chan SliderMoveEvent{},
	}

	logger.Debug("Created serial i/o instance")

	// respond to config changes
	sio.setupOnConfigReload()

	return sio, nil
}

// Start attempts to connect to our arduino chip
func (sio *SerialIO) Start() error {

	// don't allow multiple concurrent connections
	if sio.connected {
		sio.logger.Warn("Already connected, can't start another without closing first")
		return errors.New("serial: connection already active")
	}

	sio.connOptions = &serial.Mode{
		BaudRate: sio.deej.config.ConnectionInfo.BaudRate,
		DataBits: 8,
		StopBits: serial.OneStopBit,
		Parity:   serial.NoParity,
	}

	sio.comPort = sio.deej.config.ConnectionInfo.COMPort
	sio.baudRate = uint(sio.deej.config.ConnectionInfo.BaudRate)

	sio.logger.Debugw("Attempting serial connection",
		"comPort", sio.comPort,
		"baudRate", sio.connOptions.BaudRate)

	var err error
	sio.conn, err = serial.Open(sio.comPort, sio.connOptions)
	if err != nil {

		// might need a user notification here, TBD
		sio.logger.Warnw("Failed to open serial connection", "error", err)
		return fmt.Errorf("open serial connection: %w", err)
	}

	namedLogger := sio.logger.Named(strings.ToLower(sio.comPort))

	namedLogger.Infow("Connected", "conn", sio.conn)

	// Set DTR to enable bidirectional communication (required for CH340 chips)
	if err := sio.conn.SetDTR(true); err != nil {
		namedLogger.Warnw("Failed to set DTR", "error", err)
	}

	sio.connected = true

	// read lines or await a stop
	go func() {
		connReader := bufio.NewReader(sio.conn)
		lineChannel := sio.readLine(namedLogger, connReader)

		for {
			select {
			case <-sio.stopChannel:
				sio.close(namedLogger)
			case line := <-lineChannel:
				sio.handleLine(namedLogger, line)
			}
		}
	}()

	return nil
}

// Stop signals us to shut down our serial connection, if one is active
func (sio *SerialIO) Stop() {
	if sio.connected {
		sio.logger.Debug("Shutting down serial connection")
		sio.stopChannel <- true
	} else {
		sio.logger.Debug("Not currently connected, nothing to stop")
	}
}

// SubscribeToSliderMoveEvents returns an unbuffered channel that receives
// a sliderMoveEvent struct every time a slider moves
func (sio *SerialIO) SubscribeToSliderMoveEvents() chan SliderMoveEvent {
	ch := make(chan SliderMoveEvent)
	sio.sliderMoveConsumers = append(sio.sliderMoveConsumers, ch)

	return ch
}

// SendLEDState sends a command to the Arduino to turn an LED on or off
func (sio *SerialIO) SendLEDState(sliderID int, on bool) error {
	if !sio.connected || sio.conn == nil {
		return errors.New("serial: not connected")
	}

	state := "0"
	if on {
		state = "1"
	}

	command := fmt.Sprintf("#L%d:%s\n", sliderID, state)

	sio.writeMu.Lock()
	defer sio.writeMu.Unlock()

	_, err := sio.conn.Write([]byte(command))
	if err != nil {
		sio.logger.Warnw("Failed to send LED state", "sliderID", sliderID, "on", on, "error", err)
		return fmt.Errorf("write LED state: %w", err)
	}

	if sio.deej.Verbose() {
		sio.logger.Debugw("Sent LED state", "sliderID", sliderID, "on", on)
	}

	return nil
}

// SendAllLEDStates sends all LED states in a single batched command
// Format: #LS:1,0,1,0\n (comma-separated states in slider order)
func (sio *SerialIO) SendAllLEDStates(states map[int]bool, numSliders int) error {
	if !sio.connected || sio.conn == nil {
		return errors.New("serial: not connected")
	}

	// Build comma-separated state string
	stateStrs := make([]string, numSliders)
	for i := 0; i < numSliders; i++ {
		if states[i] {
			stateStrs[i] = "1"
		} else {
			stateStrs[i] = "0"
		}
	}

	command := fmt.Sprintf("#LS:%s\n", strings.Join(stateStrs, ","))

	sio.writeMu.Lock()
	defer sio.writeMu.Unlock()

	_, err := sio.conn.Write([]byte(command))
	if err != nil {
		sio.logger.Warnw("Failed to send all LED states", "error", err)
		return fmt.Errorf("write all LED states: %w", err)
	}

	if sio.deej.Verbose() {
		sio.logger.Debugw("Sent all LED states", "states", states)
	}

	return nil
}

// SendAudioPeaks sends audio peak levels with app names for all sliders
// Format: #AP:50:chrm,75:frfx,30:dscd,0:\n (peak:name pairs)
func (sio *SerialIO) SendAudioPeaks(peaks map[int]int, names map[int]string, numSliders int) error {
	if !sio.connected || sio.conn == nil {
		return errors.New("serial: not connected")
	}

	// Build comma-separated peak:name pairs
	parts := make([]string, numSliders)
	for i := 0; i < numSliders; i++ {
		name := shortenAppName(names[i])
		parts[i] = fmt.Sprintf("%d:%s", peaks[i], name)
	}

	command := fmt.Sprintf("#AP:%s\n", strings.Join(parts, ","))

	sio.writeMu.Lock()
	defer sio.writeMu.Unlock()

	_, err := sio.conn.Write([]byte(command))
	if err != nil {
		sio.logger.Warnw("Failed to send audio peaks", "error", err)
		return fmt.Errorf("write audio peaks: %w", err)
	}

	if sio.deej.Verbose() {
		sio.logger.Debugw("Sent audio peaks", "peaks", peaks, "names", names)
	}

	return nil
}

// shortenAppName creates a 4-char abbreviation by removing vowels
// e.g., "chrome" → "chrm", "firefox" → "frfx", "discord" → "dscd"
func shortenAppName(name string) string {
	if name == "" {
		return ""
	}

	vowels := "aeiouAEIOU"
	var result []byte

	// First pass: collect consonants
	for i := 0; i < len(name) && len(result) < 4; i++ {
		if !strings.ContainsRune(vowels, rune(name[i])) {
			result = append(result, name[i])
		}
	}

	// If not enough consonants, add vowels from the beginning
	if len(result) < 4 {
		for i := 0; i < len(name) && len(result) < 4; i++ {
			if strings.ContainsRune(vowels, rune(name[i])) {
				result = append(result, name[i])
			}
		}
	}

	// If still not enough, just take first chars
	if len(result) < 4 && len(name) >= 4 {
		return name[:4]
	}

	return string(result)
}

func (sio *SerialIO) setupOnConfigReload() {
	configReloadedChannel := sio.deej.config.SubscribeToChanges()

	const stopDelay = 50 * time.Millisecond

	go func() {
		for {
			select {
			case <-configReloadedChannel:

				// make any config reload unset our slider number to ensure process volumes are being re-set
				// (the next read line will emit SliderMoveEvent instances for all sliders)\
				// this needs to happen after a small delay, because the session map will also re-acquire sessions
				// whenever the config file is reloaded, and we don't want it to receive these move events while the map
				// is still cleared. this is kind of ugly, but shouldn't cause any issues
				go func() {
					<-time.After(stopDelay)
					sio.lastKnownNumSliders = 0
				}()

				// if connection params have changed, attempt to stop and start the connection
				if sio.deej.config.ConnectionInfo.COMPort != sio.comPort ||
					sio.deej.config.ConnectionInfo.BaudRate != int(sio.baudRate) {

					sio.logger.Info("Detected change in connection parameters, attempting to renew connection")
					sio.Stop()

					// let the connection close
					<-time.After(stopDelay)

					if err := sio.Start(); err != nil {
						sio.logger.Warnw("Failed to renew connection after parameter change", "error", err)
					} else {
						sio.logger.Debug("Renewed connection successfully")
					}
				}
			}
		}
	}()
}

func (sio *SerialIO) close(logger *zap.SugaredLogger) {
	if err := sio.conn.Close(); err != nil {
		logger.Warnw("Failed to close serial connection", "error", err)
	} else {
		logger.Debug("Serial connection closed")
	}

	sio.conn = nil
	sio.connected = false
}

func (sio *SerialIO) readLine(logger *zap.SugaredLogger, reader *bufio.Reader) chan string {
	ch := make(chan string)

	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {

				if sio.deej.Verbose() {
					logger.Warnw("Failed to read line from serial", "error", err, "line", line)
				}

				// just ignore the line, the read loop will stop after this
				return
			}

			if sio.deej.Verbose() {
				logger.Debugw("Read new line", "line", line)
			}

			// deliver the line to the channel
			ch <- line
		}
	}()

	return ch
}

func (sio *SerialIO) handleLine(logger *zap.SugaredLogger, line string) {
	// Check for button commands first (format: #B<id>\r\n)
	if strings.HasPrefix(line, "#B") {
		sio.handleButtonCommand(logger, line)
		return
	}

	// this function receives an unsanitized line which is guaranteed to end with LF,
	// but most lines will end with CRLF. it may also have garbage instead of
	// deej-formatted values, so we must check for that! just ignore bad ones
	if !expectedLinePattern.MatchString(line) {
		return
	}

	// trim the suffix
	line = strings.TrimSuffix(line, "\r\n")

	// split on pipe (|), this gives a slice of numerical strings between "0" and "1023"
	splitLine := strings.Split(line, "|")
	numSliders := len(splitLine)

	// update our slider count, if needed - this will send slider move events for all
	if numSliders != sio.lastKnownNumSliders {
		logger.Infow("Detected sliders", "amount", numSliders)
		sio.lastKnownNumSliders = numSliders
		sio.currentSliderPercentValues = make([]float32, numSliders)

		// reset everything to be an impossible value to force the slider move event later
		for idx := range sio.currentSliderPercentValues {
			sio.currentSliderPercentValues[idx] = -1.0
		}
	}

	// for each slider:
	moveEvents := []SliderMoveEvent{}
	for sliderIdx, stringValue := range splitLine {

		// convert string values to integers ("1023" -> 1023)
		number, _ := strconv.Atoi(stringValue)

		// turns out the first line could come out dirty sometimes (i.e. "4558|925|41|643|220")
		// so let's check the first number for correctness just in case
		if sliderIdx == 0 && number > 1023 {
			sio.logger.Debugw("Got malformed line from serial, ignoring", "line", line)
			return
		}

		// map the value from raw to a "dirty" float between 0 and 1 (e.g. 0.15451...)
		dirtyFloat := float32(number) / 1023.0

		// normalize it to an actual volume scalar between 0.0 and 1.0 with 2 points of precision
		normalizedScalar := util.NormalizeScalar(dirtyFloat)

		// if sliders are inverted, take the complement of 1.0
		if sio.deej.config.InvertSliders {
			normalizedScalar = 1 - normalizedScalar
		}

		// check if it changes the desired state (could just be a jumpy raw slider value)
		if util.SignificantlyDifferent(sio.currentSliderPercentValues[sliderIdx], normalizedScalar, sio.deej.config.NoiseReductionLevel) {

			// if it does, update the saved value and create a move event
			sio.currentSliderPercentValues[sliderIdx] = normalizedScalar

			moveEvents = append(moveEvents, SliderMoveEvent{
				SliderID:     sliderIdx,
				PercentValue: normalizedScalar,
			})

			if sio.deej.Verbose() {
				logger.Debugw("Slider moved", "event", moveEvents[len(moveEvents)-1])
			}
		}
	}

	// deliver move events if there are any, towards all potential consumers
	if len(moveEvents) > 0 {
		for _, consumer := range sio.sliderMoveConsumers {
			for _, moveEvent := range moveEvents {
				consumer <- moveEvent
			}
		}
	}
}

func (sio *SerialIO) handleButtonCommand(logger *zap.SugaredLogger, line string) {
	// Format: #B<id>\r\n
	line = strings.TrimSuffix(line, "\r\n")
	line = strings.TrimSuffix(line, "\n")

	if len(line) < 3 {
		return
	}

	buttonID := line[2:] // Get everything after "#B"

	if sio.deej.Verbose() {
		logger.Debugw("Button pressed", "buttonID", buttonID)
	}

	switch buttonID {
	case "0":
		sio.deej.mediaController.PlayPause()
	case "1":
		sio.deej.mediaController.PrevTrack()
	case "2":
		sio.deej.mediaController.NextTrack()
	default:
		logger.Warnw("Unknown button ID", "buttonID", buttonID)
	}
}
