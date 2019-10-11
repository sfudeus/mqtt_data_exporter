package tasmota

import (
	"errors"
	"github.com/klaper_/mqtt_data_exporter/prom"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const sensorClientId = "tasmota_sensor"

var sensorNames = []string{"SI7021", "SDS0X1", "BH1750", "BMP280"}
var sensorTypes = []SensorType{Temperature, Pressure, Humidity, PM10, PM2, Illuminance}
var unitFields = regexp.MustCompile("Unit$")

type SensorType string

const (
	Temperature SensorType = "Temperature"
	Pressure    SensorType = "Pressure"
	Humidity    SensorType = "Humidity"
	PM10        SensorType = "PM10"
	PM2         SensorType = "PM2.5"
	Illuminance SensorType = "Illuminance"
)

type sensorCollector struct {
	channel      chan interface{}
	metricsStore *prom.Metrics
}

type sensorData struct {
	Type       SensorType
	SensorName string
	Value      float64
}

type sensor struct {
	DeviceName string
	Sensors    []sensorData
}

func (sensor *sensor) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var tmp map[string]interface{}
	err := unmarshal(&tmp)
	if err != nil {
		return err
	}
	debug("sensor", "parsed input to: %+v", tmp)

	sensor.Sensors = getSensorData(tmp)

	return nil
}

func getSensorData(data map[string]interface{}) (sensors []sensorData) {
	for i := range sensorNames {
		tmp, ok := data[sensorNames[i]]
		if ok {
			for t := range sensorTypes {
				value, err := getSingleReadout(sensorNames[i], sensorTypes[t], tmp)
				if err != nil {
					continue
				}
				sensors = append(sensors, *value)
			}
		}
	}
	return
}

func getSingleReadout(sensorName string, sensorType SensorType, input interface{}) (*sensorData, error) {
	var value float64
	var ok bool

	data, ok := input.(map[interface{}]interface{})
	if !ok {
		warn("sensor", "[sensorType: %s][1] Got wrong type (%T) for sensor data", sensorType, input)
		return nil, errors.New("got wrong type for sensor data")
	}
	debug("sensor", "[sensorType: %s][2] Got sensor data: %+v", sensorType, data)
	interfaceValue, ok := data[string(sensorType)]
	if !ok {
		warn("sensor", "[sensorType: %s][3] Could not get sensor value, got: %+v", sensorType, interfaceValue)
		return nil, errors.New("got wrong type for sensor data")
	}

	switch interfaceValue.(type) {
	case float64:
		value, ok = interfaceValue.(float64)
		if !ok {
			warn("sensor", "[sensorType: %s][7][float64] Could not get sensor value, got: %+v (%T)", sensorType, interfaceValue, interfaceValue)
			return nil, errors.New("got wrong type for sensor data")
		}
	case int:
		intValue, ok := interfaceValue.(int)
		if !ok {
			warn("sensor", "[sensorType: %s][8][int] Could not get int value, got: %+v (%T)", sensorType, interfaceValue, interfaceValue)
			return nil, errors.New("got wrong type for sensor data")
		}
		value = float64(intValue)
	}
	debug("sensor", "[sensorType: %s][9] Parsed sensor value to: (%T) %+v", sensorType, value, value)

	return &sensorData{
			Type:       sensorType,
			SensorName: sensorName,
			Value:      value,
		},
		nil
}

func getKeys(input map[string]interface{}) (keys []string, units []string) {
	for k := range input {
		if unitFields.MatchString(k) {
			units = append(units, k)
		} else {
			keys = append(keys, k)
		}
	}
	return
}

func isSensorMessage(topic string) bool {
	split := strings.Split(topic, "/")
	return len(split) == 3 && split[2] == "SENSOR"
}

func newSensorCollector(metricsStore *prom.Metrics) (collector *sensorCollector) {
	for sensor := range sensorTypes {
		if !strings.HasPrefix(string(sensorTypes[sensor]), "PM") {
			metricsStore.RegisterGauge(
				string(sensorTypes[sensor]),
				"tasmota_sensor_"+strings.Replace(strings.ToLower(string(sensorTypes[sensor])), ".", "", 1),
				string(sensorTypes[sensor])+"tasmota sensor data",
				[]string{"sensor_name"},
			)
		}
	}
	metricsStore.RegisterGauge(
		"pm",
		"tasmota_sensor_pm",
		"PM tasmota entity",
		[]string{"sensor_name", "resolution"},
	)
	return &sensorCollector{
		channel:      make(chan interface{}),
		metricsStore: metricsStore,
	}
}

func (collector *sensorCollector) collector() {
	for tmp := range collector.channel {
		message, err := receiveMessage(tmp, "sensor", isSensorMessage)
		if err != nil {
			continue
		}

		sensor := sensor{}
		err = yaml.Unmarshal([]byte((message).Payload()), &sensor)
		if err != nil {
			fatal("sensor", "error while unmarshaling", err)
			return
		}
		sensor.DeviceName = message.GetDeviceName()
		info("sensor", "message: %+v", sensor)

		collector.updateState(sensor)
	}
}

func (collector *sensorCollector) updateState(sensor sensor) {
	for i := range sensor.Sensors {
		data := sensor.Sensors[i]
		if !strings.HasPrefix(string(data.Type), "PM") {
			data := sensor.Sensors[i]
			collector.metricsStore.GaugeSet(
				string(data.Type),
				sensor.DeviceName,
				map[string]string{
					"sensor_name": data.SensorName,
				},
				data.Value,
			)
		} else {
			collector.metricsStore.GaugeSet(
				"pm",
				sensor.DeviceName,
				map[string]string{
					"sensor_name": data.SensorName,
					"resolution":  string(data.Type),
				},
				data.Value,
			)
		}
	}
}
