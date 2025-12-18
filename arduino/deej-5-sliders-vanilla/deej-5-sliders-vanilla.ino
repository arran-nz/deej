#include <Wire.h>
#include <Adafruit_GFX.h>
#include <Adafruit_SSD1306.h>

// OLED Display
#define SCREEN_WIDTH 128
#define SCREEN_HEIGHT 64
#define OLED_RESET -1
#define OLED_ADDRESS 0x3C
Adafruit_SSD1306 display(SCREEN_WIDTH, SCREEN_HEIGHT, &Wire, OLED_RESET);

const int NUM_SLIDERS = 5;
const int analogInputs[NUM_SLIDERS] = {A0, A1, A2, A3, A6};
const int ledPins[NUM_SLIDERS] = {14, 15, 16, 6, 7};  // Moved 0,1 to 14,15 for I2C OLED

// Buttons for media control
const int NUM_BUTTONS = 3;
const int buttonPins[NUM_BUTTONS] = {8, 9, 10};  // Play/Pause, Prev, Next
bool lastButtonStates[NUM_BUTTONS] = {HIGH, HIGH, HIGH};
bool buttonStates[NUM_BUTTONS] = {HIGH, HIGH, HIGH};
unsigned long lastDebounceTimes[NUM_BUTTONS] = {0, 0, 0};
const unsigned long debounceDelay = 50;

int analogSliderValues[NUM_SLIDERS];
bool ledStates[NUM_SLIDERS] = {false, false, false, false, false};

// Buffer for incoming serial commands
char inputBuffer[16];
int inputIndex = 0;
bool receivingCommand = false;
unsigned long lastReceiveTime = 0;

// Display update throttle
unsigned long lastDisplayUpdate = 0;
const unsigned long displayUpdateInterval = 50;  // Update display every 50ms

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

  // Initialize OLED display
  if (display.begin(SSD1306_SWITCHCAPVCC, OLED_ADDRESS)) {
    display.clearDisplay();
    display.setTextSize(1);
    display.setTextColor(SSD1306_WHITE);
    display.setCursor(0, 0);
    display.println(F("Volume Controller"));
    display.println(F("5 Sliders Ready"));
    display.display();
    delay(1000);
  }

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
  updateDisplay();
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

void updateDisplay() {
  // Throttle display updates to avoid flickering
  if (millis() - lastDisplayUpdate < displayUpdateInterval) {
    return;
  }
  lastDisplayUpdate = millis();

  display.clearDisplay();

  // Draw title bar
  display.setTextSize(1);
  display.setCursor(0, 0);
  display.print(F("VOL"));

  // Calculate bar dimensions
  const int barWidth = 20;
  const int barSpacing = 5;
  const int barMaxHeight = 48;
  const int barY = 14;  // Start below title
  const int startX = 4;

  // Draw 5 volume bars
  for (int i = 0; i < NUM_SLIDERS; i++) {
    int x = startX + i * (barWidth + barSpacing);

    // Map analog value (0-1023) to bar height
    int barHeight = map(analogSliderValues[i], 0, 1023, 0, barMaxHeight);

    // Draw bar outline
    display.drawRect(x, barY, barWidth, barMaxHeight, SSD1306_WHITE);

    // Draw filled portion (from bottom up)
    if (barHeight > 0) {
      display.fillRect(x + 1, barY + barMaxHeight - barHeight, barWidth - 2, barHeight, SSD1306_WHITE);
    }

    // Draw slider number below
    display.setCursor(x + 7, 56);
    display.print(i);

    // Draw LED indicator dot if active
    if (ledStates[i]) {
      display.fillCircle(x + barWidth/2, 10, 2, SSD1306_WHITE);
    }
  }

  // Show percentage of first slider on the right
  int pct = map(analogSliderValues[0], 0, 1023, 0, 100);
  display.setCursor(100, 0);
  display.print(pct);
  display.print(F("%"));

  display.display();
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
  if (cmd[0] != '#' || cmd[1] != 'L') {
    return;
  }

  // Batched LED command: #LS:1,0,1 (all LED states comma-separated)
  if (cmd[2] == 'S' && cmd[3] == ':') {
    char* ptr = cmd + 4;  // Start after "#LS:"
    int ledIndex = 0;

    while (*ptr != '\0' && ledIndex < NUM_SLIDERS) {
      ledStates[ledIndex] = (*ptr != '0');
      ledIndex++;

      // Skip to next value (past comma)
      while (*ptr != '\0' && *ptr != ',') {
        ptr++;
      }
      if (*ptr == ',') {
        ptr++;
      }
    }
    return;
  }

  // Single LED command: #L<id>:<state>
  // Example: #L0:1 (LED 0 on), #L1:0 (LED 1 off)
  char* colonPos = strchr(cmd, ':');
  if (colonPos != NULL) {
    *colonPos = '\0';
    int ledId = atoi(cmd + 2);
    int state = atoi(colonPos + 1);

    if (ledId >= 0 && ledId < NUM_SLIDERS) {
      ledStates[ledId] = (state != 0);
    }
  }
}

void updateLEDs() {
  for (int i = 0; i < NUM_SLIDERS; i++) {
    digitalWrite(ledPins[i], ledStates[i] ? HIGH : LOW);
  }
}
