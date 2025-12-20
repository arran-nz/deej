#include <Wire.h>
#include <Adafruit_GFX.h>
#include <Adafruit_SSD1306.h>

// OLED Display
#define SCREEN_WIDTH 128
#define SCREEN_HEIGHT 64
#define OLED_RESET -1
#define OLED_ADDRESS 0x3C
Adafruit_SSD1306 display(SCREEN_WIDTH, SCREEN_HEIGHT, &Wire, OLED_RESET);

const int NUM_SLIDERS = 4;
const int analogInputs[NUM_SLIDERS] = {A0, A1, A2, A3};
const int ledPins[NUM_SLIDERS] = {14, 15, 16, 10};  // All on right side

// Buttons for media control
const int NUM_BUTTONS = 3;
const int buttonPins[NUM_BUTTONS] = {8, 9, 4};  // All on left side
bool lastButtonStates[NUM_BUTTONS] = {HIGH, HIGH, HIGH};
bool buttonStates[NUM_BUTTONS] = {HIGH, HIGH, HIGH};
unsigned long lastDebounceTimes[NUM_BUTTONS] = {0, 0, 0};
const unsigned long debounceDelay = 50;

int analogSliderValues[NUM_SLIDERS];
bool ledStates[NUM_SLIDERS] = {false, false, false, false};
int audioPeaks[NUM_SLIDERS] = {0, 0, 0, 0};  // 0-100 audio levels from deej
char appNames[NUM_SLIDERS][5] = {"", "", "", ""};  // 4-char app names + null

// Moving average filter for noise reduction
const int NUM_SAMPLES = 10;
int samples[NUM_SLIDERS][NUM_SAMPLES];
int sampleIndex = 0;

// Buffer for incoming serial commands
char inputBuffer[48];  // Increased for #AP:50:chr,75:fir,30:dis,0:
int inputIndex = 0;
bool receivingCommand = false;
unsigned long lastReceiveTime = 0;

// Display update throttle
unsigned long lastDisplayUpdate = 0;
const unsigned long displayUpdateInterval = 50;  // Update display every 50ms

// DEEJ connection tracking
unsigned long lastDeejCommand = 0;
const unsigned long deejTimeoutMs = 10000;  // 10 seconds

// Quiet mode for firmware uploads (stops serial output to allow 1200 baud reset)
unsigned long quietUntil = 0;

// Forward declaration
void showMessage(const char* line1, const char* line2);

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
    showMessage("DEEJ", "Starting...");
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

  // Only send slider data if we're not in quiet mode or receiving a command
  if (!receivingCommand && millis() >= quietUntil) {
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
  // Store new samples
  for (int i = 0; i < NUM_SLIDERS; i++) {
    samples[i][sampleIndex] = analogRead(analogInputs[i]);
  }
  sampleIndex = (sampleIndex + 1) % NUM_SAMPLES;

  // Calculate moving average
  for (int i = 0; i < NUM_SLIDERS; i++) {
    long sum = 0;
    for (int j = 0; j < NUM_SAMPLES; j++) {
      sum += samples[i][j];
    }
    analogSliderValues[i] = sum / NUM_SAMPLES;
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

  // Check for DEEJ timeout (only after initial connection)
  if (lastDeejCommand > 0 && millis() - lastDeejCommand > deejTimeoutMs) {
    showMessage("NO DEEJ", "Check connection");
    return;
  }

  display.clearDisplay();

  // Calculate bar dimensions - 4 bars, no border, 4px margin
  const int barSpacing = 4;
  const int barWidth = (128 - (barSpacing * 3)) / 4;  // ~29px per bar
  const int halfWidth = barWidth / 2;
  const int barMaxHeight = 54;
  const int barY = 0;
  const int startX = 0;

  // Draw 4 volume bars (reversed order to match dial layout)
  for (int i = 0; i < NUM_SLIDERS; i++) {
    int x = startX + i * (barWidth + barSpacing);
    int dataIdx = NUM_SLIDERS - 1 - i;  // Reverse the data index

    // Map slider value (0-1023) to bar height
    int sliderHeight = map(analogSliderValues[dataIdx], 0, 1023, 0, barMaxHeight);

    // Map audio peak (0-100) to bar height
    int peakHeight = map(audioPeaks[dataIdx], 0, 100, 0, barMaxHeight);

    // Left half: audio peak from deej (no border)
    if (peakHeight > 0) {
      display.fillRect(x, barY + barMaxHeight - peakHeight, halfWidth, peakHeight, SSD1306_WHITE);
    }

    // Right half: slider value (no border)
    if (sliderHeight > 0) {
      display.fillRect(x + halfWidth, barY + barMaxHeight - sliderHeight, halfWidth, sliderHeight, SSD1306_WHITE);
    }

    // Draw app name at bottom (TitleCase)
    display.setTextColor(SSD1306_WHITE);
    display.setCursor(x, 56);
    if (appNames[dataIdx][0] != '\0') {
      // TitleCase: first char uppercase, rest lowercase
      char titleName[5];
      titleName[0] = toupper(appNames[dataIdx][0]);
      titleName[1] = tolower(appNames[dataIdx][1]);
      titleName[2] = tolower(appNames[dataIdx][2]);
      titleName[3] = tolower(appNames[dataIdx][3]);
      titleName[4] = '\0';
      display.print(titleName);
    } else {
      display.print("----");
    }
  }

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
      } else if (inputIndex < 47) {
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

// Display a centered message box with 1-2 lines of text
void showMessage(const char* line1, const char* line2) {
  display.clearDisplay();

  // Calculate box dimensions
  int boxWidth = 100;
  int boxHeight = 30;
  int boxX = (128 - boxWidth) / 2;
  int boxY = (64 - boxHeight) / 2;

  // Draw bordered box
  display.drawRect(boxX, boxY, boxWidth, boxHeight, SSD1306_WHITE);
  display.drawRect(boxX + 1, boxY + 1, boxWidth - 2, boxHeight - 2, SSD1306_WHITE);

  // Draw text centered
  display.setTextColor(SSD1306_WHITE);

  int16_t x1, y1;
  uint16_t w1, h1;
  display.getTextBounds(line1, 0, 0, &x1, &y1, &w1, &h1);
  display.setCursor(boxX + (boxWidth - w1) / 2, boxY + 6);
  display.print(line1);

  if (line2[0] != '\0') {
    display.getTextBounds(line2, 0, 0, &x1, &y1, &w1, &h1);
    display.setCursor(boxX + (boxWidth - w1) / 2, boxY + 18);
    display.print(line2);
  }

  display.display();
}

void processCommand(char* cmd) {
  if (cmd[0] != '#') {
    return;
  }

  // Track DEEJ activity
  lastDeejCommand = millis();

  // Quiet mode: #Q - stops serial output for 10 seconds to allow 1200 baud reset
  if (cmd[1] == 'Q') {
    quietUntil = millis() + 10000;
    showMessage("QUIET", "Upload ready");
    return;
  }

  // Audio peak command with names: #AP:50:chr,75:fir,30:dis,0:
  if (cmd[1] == 'A' && cmd[2] == 'P' && cmd[3] == ':') {
    char* ptr = cmd + 4;
    int idx = 0;

    while (*ptr != '\0' && idx < NUM_SLIDERS) {
      // Parse peak value
      audioPeaks[idx] = atoi(ptr);
      if (audioPeaks[idx] > 100) audioPeaks[idx] = 100;

      // Skip to colon
      while (*ptr != '\0' && *ptr != ':') ptr++;
      if (*ptr == ':') ptr++;

      // Parse app name (up to 4 chars)
      int nameIdx = 0;
      while (*ptr != '\0' && *ptr != ',' && nameIdx < 4) {
        appNames[idx][nameIdx++] = *ptr++;
      }
      appNames[idx][nameIdx] = '\0';

      // Skip to next entry
      while (*ptr != '\0' && *ptr != ',') ptr++;
      if (*ptr == ',') ptr++;

      idx++;
    }
    return;
  }

  // Legacy audio peak command: #AS:50,75,30,80 (0-100 for each slider)
  if (cmd[1] == 'A' && cmd[2] == 'S' && cmd[3] == ':') {
    char* ptr = cmd + 4;
    int idx = 0;

    while (*ptr != '\0' && idx < NUM_SLIDERS) {
      audioPeaks[idx] = atoi(ptr);
      if (audioPeaks[idx] > 100) audioPeaks[idx] = 100;
      idx++;

      while (*ptr != '\0' && *ptr != ',') ptr++;
      if (*ptr == ',') ptr++;
    }
    return;
  }

  if (cmd[1] != 'L') {
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
