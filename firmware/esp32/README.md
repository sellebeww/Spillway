# Spillway ESP32 water level node

Ultrasonic distance sensor above a water surface, median of 5 pings, signed JSON (HMAC-SHA256) over MQTT QoS 1 every 30 s.
Protocol: [docs/PROTOCOL.md](../../docs/PROTOCOL.md). Safety statement: Spillway is a community aid, not an official early warning system.

**Status: compiles (`pio run`) and the portable core passes host tests (`pio test -e native`, protocol golden vectors). It has NOT been run on real hardware by the author of this commit.** Treat the first flash as a test, and verify the first message in the admin UI.

## Build and flash

```sh
pip install platformio                      # or a venv
cd firmware/esp32
cp include/secrets.example.h include/secrets.h   # then edit: Wi-Fi, broker, DEVICE_KEY, DEVICE_SECRET_HEX
pio test -e native                          # host unit tests, no board needed
pio run                                     # build
pio run -t upload && pio device monitor     # flash and watch the serial log
```

`secrets.h` is git-ignored. Register the device in the admin UI first: the secret (64 hex characters) is shown once.
Pins, interval, battery divider and sensor limits are in `include/config.h`.

## Wiring (ESP32 DevKit, 30/38 pin)

| Sensor pin | ESP32 | Note |
|---|---|---|
| VCC | 5 V (VIN) | HC-SR04 needs 5 V; JSN-SR04T works from 3.3 to 5 V |
| GND | GND | |
| TRIG | GPIO 5 | 3.3 V logic is enough to trigger |
| ECHO | GPIO 18 | **5 V on HC-SR04: use a divider** (1 kOhm from ECHO to GPIO, 2 kOhm from GPIO to GND). Never connect 5 V straight to the ESP32 |
| Battery (optional) | GPIO 34 (ADC1) via 100 kOhm / 100 kOhm divider | set `PIN_BATT_ADC` to 34; `BATT_DIVIDER` 2.0 |

Use the waterproof JSN-SR04T (separate transducer on a cable) for outdoor use, mount it pointing straight down, over open water, away from walls and pipes (side echoes), and shield the electronics from rain.

## Calibration in a bucket (finding `offset_cm`)

`level_cm = offset_cm - raw_mm / 10`, where `offset_cm` is the height of the sensor face above the datum zero (cm).

1. Mount the sensor over a bucket, face down, the way it will hang in the field. Bucket depth must exceed the sensor blind zone (JSN-SR04T about 25 cm) plus the stroke you want to test.
2. Pick a datum zero you can measure again later: for the bucket, the inside bottom. In the field this is a fixed mark (a staff gauge zero, a bridge bolt).
3. Fill to a known depth `h` cm measured with a ruler (water depth above the datum). Read `raw_mm` from the serial log or the admin UI (last reading) at the same moment.
4. `offset_cm = h + raw_mm / 10`. Repeat at 3 depths (for example 10, 20, 30 cm above the bottom); the three results must agree within the accuracy below. Use their average. A trend means the sensor is not pointing straight down, or the temperature assumption is off.
5. Enter it as a new calibration version in the admin UI (calibration is append-only with `valid_from`). The admin preview shows the resulting `level_cm`.
6. Re-check after moving or re-mounting the sensor: a new mount needs a new calibration version.

## Accuracy limits (honest)

- Datasheet resolution is about 3 mm, typical accuracy 1 cm or worse. Median of 5 removes single echo outliers, not systematic error.
- Speed of sound changes with air temperature (about 0.17 percent per degree C): the firmware assumes `AMBIENT_TEMP_C` (28). A 10 degree C error is about 1.7 percent, that is 34 mm at 2 m. Sensors in the sun see hotter air than the reading. There is no temperature sensor in this build.
- Blind zone (JSN-SR04T about 25 cm): readings below `SENSOR_MIN_MM` (250) are discarded; beyond `SENSOR_MAX_MM` (4500) too. When the median is invalid nothing is sent, so the site shows DATA TERTUNDA instead of a wrong number.
- Soft targets (foam, waves, floating debris, rain on the surface) scatter echoes; expect noise during heavy rain and flow. Spikes are scored by the server (docs/QUALITY.md).
- The node uses the Wi-Fi network you give it and does not sleep: about 80 to 120 mA, so a battery needs solar or a small bank. Deep sleep is not implemented (BACKLOG).
- The clock comes from NTP; without sync the node sends nothing. Messages queued while MQTT is down are retransmitted by the MQTT stack (bounded outbox) and arrive flagged as late.
- The HMAC self-test runs at boot against the protocol golden vectors; failure halts the node with `FATAL` on serial.

## Why Arduino and not ESP-IDF

Smaller code for a single-sensor node, `pulseIn`/ADC/Wi-Fi/NTP helpers, and the same bundled esp-mqtt (QoS 1, automatic reconnect) and mbedtls as ESP-IDF. The portable core in `lib/spillway_core` has no Arduino dependency, so a move to ESP-IDF changes only `src/main.cpp`.
