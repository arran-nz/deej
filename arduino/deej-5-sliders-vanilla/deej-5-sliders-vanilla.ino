const int NUM_SLIDERS = 2;
const int analogInputs[NUM_SLIDERS] = {A0, A1};
const int ledPins[NUM_SLIDERS] = {2, 3};

// Buttons for media control
const int NUM_BUTTONS = 3;
const int buttonPins[NUM_BUTTONS] = {12, 11, 10};  // Play/Pause, Prev, Next
bool lastButtonStates[NUM_BUTTONS] = {HIGH, HIGH, HIGH};
bool buttonStates[NUM_BUTTONS] = {HIGH, HIGH, HIGH};
unsigned long lastDebounceTimes[NUM_BUTTONS] = {0, 0, 0};
const unsigned long debounceDelay = 50;

int analogSliderValues[NUM_SLIDERS];
bool ledStates[NUM_SLIDERS] = {false, false};

// Buffer for incoming serial commands
char inputBuffer[16];
int inputIndex = 0;
bool receivingCommand = false;
unsigned long lastReceiveTime = 0;

void setup() {
  for (int i = 0; i < NUM_SLIDERS; i++) {
    pinMode(analogInputs[i], INPUT);
    pinMode(ledPins[i], OUTPUT);
    digitalWrite(ledPins[i], LOW);
  }

  // Buttons with internal pull-up (pressed = LOW)
  for (int i = 0; i < NUM_BUTTONS; i++) {
    pinMode(buttonPins[i], INPUT_PULLUP);
  }

  Serial.begin(9600);

  // Wait for serial to stabilize and clear any garbage in buffer
  delay(100);
  while (Serial.available() > 0) {
    Serial.read();
  }
}

void loop() {
  // Check for incoming commands
  checkSerialInput();

  // Check button presses
  checkButtons();

  // Only send slider data if we're not in the middle of receiving a command
  if (!receivingCommand) {
    updateSliderValues();
    sendSliderValues();
  }

  updateLEDs();
  delay(10);
}

void checkButtons() {
  for (int i = 0; i < NUM_BUTTONS; i++) {
    bool reading = digitalRead(buttonPins[i]);

    if (reading != lastButtonStates[i]) {
      lastDebounceTimes[i] = millis();
    }

    if ((millis() - lastDebounceTimes[i]) > debounceDelay) {
      if (reading != buttonStates[i]) {
        buttonStates[i] = reading;
        // Button pressed (LOW because of pull-up)
        if (buttonStates[i] == LOW) {
          Serial.print("#B");
          Serial.println(i);  // Send button ID: 0=Play/Pause, 1=Prev, 2=Next
        }
      }
    }

    lastButtonStates[i] = reading;
  }
}

void updateSliderValues() {
  for (int i = 0; i < NUM_SLIDERS; i++) {
    analogSliderValues[i] = analogRead(analogInputs[i]);
  }
}

void sendSliderValues() {
  String builtString = String("");

  for (int i = 0; i < NUM_SLIDERS; i++) {
    builtString += String((int)analogSliderValues[i]);

    if (i < NUM_SLIDERS - 1) {
      builtString += String("|");
    }
  }

  Serial.println(builtString);
}

void checkSerialInput() {
  while (Serial.available() > 0) {
    char c = Serial.read();
    lastReceiveTime = millis();

    // Command format: #L<id>:<state>\n (# prefix makes it unlikely to match noise)
    if (c == '#' && inputIndex == 0) {
      // Start of potential command
      receivingCommand = true;
      inputBuffer[inputIndex++] = c;
    } else if (receivingCommand) {
      if (c == '\n' || c == '\r') {
        if (inputIndex > 0) {
          inputBuffer[inputIndex] = '\0';
          processCommand(inputBuffer);
        }
        inputIndex = 0;
        receivingCommand = false;
      } else if (inputIndex < 15) {
        inputBuffer[inputIndex++] = c;
      } else {
        // Buffer overflow, reset
        inputIndex = 0;
        receivingCommand = false;
      }
    }
    // Ignore non-command data (echoed slider values, noise)
  }

  // Timeout: if we started receiving but didn't get newline, reset after 100ms
  if (receivingCommand && (millis() - lastReceiveTime > 100)) {
    inputIndex = 0;
    receivingCommand = false;
  }
}

void processCommand(char* cmd) {
  // Parse LED command format: #L<id>:<state>
  // Example: #L0:1 (LED 0 on), #L1:0 (LED 1 off)
  if (cmd[0] == '#' && cmd[1] == 'L') {
    int ledId = -1;
    int state = -1;

    // Find colon position
    char* colonPos = strchr(cmd, ':');
    if (colonPos != NULL) {
      *colonPos = '\0';  // Split string at colon
      ledId = atoi(cmd + 2);  // Parse LED ID (after '#L')
      state = atoi(colonPos + 1);  // Parse state (after ':')

      if (ledId >= 0 && ledId < NUM_SLIDERS) {
        ledStates[ledId] = (state != 0);
      }
    }
  }
}

void updateLEDs() {
  for (int i = 0; i < NUM_SLIDERS; i++) {
    digitalWrite(ledPins[i], ledStates[i] ? HIGH : LOW);
  }
}
