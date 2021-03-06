package main

import (
	"bufio"
	"bytes"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"syscall"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
)

const (
	namespace   = "ipmi"
	targetLocal = ""
)

var (
	ipmiCurrentPowerRegex = regexp.MustCompile(`^Chassis\s*Power\s*is\s*(?P<value>on|off*)`)
)

type fruData struct {
	Name  string
	Value string
}

type sensorData struct {
	Name  string
	Value float64
	Type  string
	State string
}

type fwumData struct {
	Name  string
	Value float64
}

type collector struct {
	target string
	module string
	config *SafeConfig
}

type ipmiTarget struct {
	host   string
	config IPMIConfig
}

var (
	sensorStateDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "sensor", "state"),
		"Indicates the severity of the state reported by an IPMI sensor (0=ok, 1=critical, 2=non-recoverable, 3=non-critical, 4=not-specified).",
		[]string{"name", "type"},
		nil,
	)

	sensorValueDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "sensor", "value"),
		"Generic data read from an IPMI sensor of unknown type, relying on labels for context.",
		[]string{"name", "type"},
		nil,
	)

	chassisIntrusionDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "chassis_int", "value"),
		"State of Chassis Intrusion.",
		[]string{"name"},
		nil,
	)

	chassisIntrusionStateDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "chassis_int", "state"),
		"Reported state of a Chassis Intrusion (0=ok, 1=intrusion).",
		[]string{"name"},
		nil,
	)

	chassisPowerDeviceDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "chassis_power_dev", "value"),
		"Chassis Power Supply device status.",
		[]string{"name"},
		nil,
	)

	chassisPowerDeviceStateDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "chassis_power_dev", "state"),
		"Reported state of a Chassis Power State (0=on, 1=off).",
		[]string{"name"},
		nil,
	)

	chassisPowerStateDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "chassis_power", "state"),
		"Reported state of a Chassis Power State (0=on, 1=off).",
		[]string{"name"},
		nil,
	)

	fanSpeedDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "fan_speed", "rpm"),
		"Fan speed in rotations per minute.",
		[]string{"name"},
		nil,
	)

	fanSpeedStateDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "fan_speed", "state"),
		"Reported state of a fan speed sensor (0=ok, 1=critical, 2=non-recoverable, 3=non-critical, 4=not-specified).",
		[]string{"name"},
		nil,
	)

	temperatureDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "temperature", "celsius"),
		"Temperature reading in degree Celsius.",
		[]string{"name"},
		nil,
	)

	temperatureStateDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "temperature", "state"),
		"Reported state of a temperature sensor (0=ok, 1=critical, 2=non-recoverable, 3=non-critical, 4=not-specified).",
		[]string{"name"},
		nil,
	)

	voltageDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "voltage", "volts"),
		"Voltage reading in Volts.",
		[]string{"name"},
		nil,
	)

	voltageStateDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "voltage", "state"),
		"Reported state of a voltage sensor (0=ok, 1=critical, 2=non-recoverable, 3=non-critical, 4=not-specified).",
		[]string{"name"},
		nil,
	)

	currentDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "current", "amperes"),
		"Current reading in Amperes.",
		[]string{"name"},
		nil,
	)

	currentStateDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "current", "state"),
		"Reported state of a current sensor (0=ok, 1=critical, 2=non-recoverable, 3=non-critical, 4=not-specified).",
		[]string{"name"},
		nil,
	)

	powerDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "power", "watts"),
		"Power reading in Watts.",
		[]string{"name"},
		nil,
	)

	powerStateDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "power", "state"),
		"Reported state of a power sensor (1=ok, 0=critical).",
		[]string{"name"},
		nil,
	)

	powerConsumption = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "dcmi", "power_consumption_watts"),
		"Current power consumption in Watts.",
		[]string{},
		nil,
	)

	bmcInfo = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "bmc", "info"),
		"Constant metric with value '1' providing details about the BMC.",
		[]string{"firmware_revision", "manufacturer_id"},
		nil,
	)

	fruInfo = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "fru", "info"),
		"Constant metric with value '1' providing details from FRU.",
		[]string{"name", "value"},
		nil,
	)

	upDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "up"),
		"'1' if a scrape of the IPMI device was successful, '0' otherwise.",
		[]string{"collector"},
		nil,
	)

	durationDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "scrape_duration", "seconds"),
		"Returns how long the scrape took to complete in seconds.",
		nil,
		nil,
	)
)

func ipmitoolConfig(config IPMIConfig) []string {
	var args []string
	if config.Privilege != "" {
		args = append(args, "-L", config.Privilege)
	}
	if config.User != "" {
		args = append(args, "-U", config.User)
	}
	if config.Password != "" {
		args = append(args, "-P", config.Password)
	}
	if config.Timeout != 0 {
		args = append(args, "-N", strconv.FormatInt(config.Timeout, 36))
	}
	return args
}

func ipmitoolOutput(target ipmiTarget, command string) (string, error) {
	var cmdCommand []string
	cmdConfig := ipmitoolConfig(target.config)
	switch command {
	case "sensor":
		cmdCommand = append(cmdCommand, "sensor", "list")
	case "fru":
		cmdCommand = append(cmdCommand, "fru", "list")
	case "power":
		cmdCommand = append(cmdCommand, "power", "status")
	case "fwum":
		cmdCommand = append(cmdCommand, "fwum", "info")
	default:
		log.Errorf("Unknown ipmitool command: '%s'\n", command)
		cmdCommand = append(cmdCommand, "")
	}

	cmdConfig = append(cmdConfig, "-H", target.host)
	cmdConfig = append(cmdConfig, cmdCommand...)

	cmd := exec.Command("ipmitool", cmdConfig...)
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf
	err := cmd.Run()
	if err != nil {
		if command == "fwum" {
			// Because fwum return exit code 1 even if everything is OK.
			// Be carefull with it and properly check command output later
			// Yes, i know that next piece of code sucks, but can`t do anything
			// with it right now
			if exiterr, ok := err.(*exec.ExitError); ok {
				if status, ok := exiterr.Sys().(syscall.WaitStatus); ok {
				log.Debugf("Exit status of FWUM %d, but it was suppressed", status.ExitStatus())
				}
			}
		} else {
			log.Fatal(err)
		}
	}
	return outBuf.String(), err
}

func splitSensorOutput(impitoolOutput string) ([]sensorData, error) {
	var result []sensorData

	scanner := bufio.NewScanner(strings.NewReader(impitoolOutput))

	var err error

	for scanner.Scan() {
		var data sensorData
		line := scanner.Text()
		if len(line) > 0 {
			trimmedL := strings.ReplaceAll(line, " ", "")
			splittedL := strings.Split(trimmedL, "|")
			data.Name = splittedL[0]
			valueS := splittedL[1]
			convValueS, convErr := strconv.ParseUint(valueS, 0, 64)
			if valueS != "na" && convErr != nil {	
				data.Value, err = strconv.ParseFloat(valueS, 64)
				if err != nil {
					continue
				}
			} else if valueS != "na" && convErr == nil {
				data.Value = float64(convValueS)
			} else {
				data.Value = math.NaN()
			}
			data.Type = splittedL[2]
			data.State = splittedL[3]
			result = append(result, data)
		}
	}
	return result, err
}

func splitFwumOutput(impitoolOutput string) ([]fwumData, error) {
	var result []fwumData

	scanner := bufio.NewScanner(strings.NewReader(impitoolOutput))

	var err error

	for scanner.Scan() {
		var data fwumData
		line := scanner.Text()
		trimmedL := strings.ReplaceAll(line, " ", "")
		re := regexp.MustCompile(`:`)
		sanitizedL := re.FindStringSubmatch(trimmedL)
		if sanitizedL != nil {
			splittedL := strings.Split(trimmedL, ":")
			data.Name = splittedL[0]
			data.Value, err = strconv.ParseFloat(splittedL[1], 64)
			if err != nil {
				return result, err
			}
		}
		result = append(result, data)
	}
	return result, err
}

func splitFruOutput(impitoolOutput string) ([]fruData, error) {
	var result []fruData

	scanner := bufio.NewScanner(strings.NewReader(impitoolOutput))

	var err error
	for scanner.Scan() {
		var data fruData
		line := scanner.Text()
		if len(line) > 0 {
			trimmedL := strings.ReplaceAll(line, " ", "")
			splittedL := strings.Split(trimmedL, ":")
			data.Name = splittedL[0]
			data.Value = splittedL[1]
			result = append(result, data)
		}
	}
	return result, err
}

func getChassisPowerState(ipmitoolOutput string) (int, error) {
	scanner := bufio.NewScanner(strings.NewReader(ipmitoolOutput))

	var err error

	for scanner.Scan() {
		line := scanner.Text()
		if len(line) > 0 {
			value := ipmiCurrentPowerRegex.FindStringSubmatch(line)[1]
			if value == "on" {
				return 0, err
			}
		}
	}
	return 1, err
}

// Describe implements Prometheus.Collector.
func (c collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- sensorStateDesc
	ch <- sensorValueDesc
	ch <- fanSpeedDesc
	ch <- temperatureDesc
	ch <- powerConsumption
	ch <- upDesc
	ch <- durationDesc
	ch <- chassisPowerDeviceDesc
	ch <- chassisIntrusionDesc
}

func collectTypedSensor(ch chan<- prometheus.Metric, desc, stateDesc *prometheus.Desc, state float64, data sensorData) {
	ch <- prometheus.MustNewConstMetric(
		desc,
		prometheus.GaugeValue,
		data.Value,
		data.Name,
	)
	ch <- prometheus.MustNewConstMetric(
		stateDesc,
		prometheus.GaugeValue,
		state,
		data.Name,
	)
}

func collectGenericSensor(ch chan<- prometheus.Metric, state float64, data sensorData) {
	ch <- prometheus.MustNewConstMetric(
		sensorValueDesc,
		prometheus.GaugeValue,
		data.Value,
		data.Name,
		data.Type,
	)
	ch <- prometheus.MustNewConstMetric(
		sensorStateDesc,
		prometheus.GaugeValue,
		state,
		data.Name,
		data.Type,
	)
}

func collectSensorMonitoring(ch chan<- prometheus.Metric, target ipmiTarget) (int, error) {
	output, err := ipmitoolOutput(target, "sensor")
	if err != nil {
		log.Errorf("Failed to collect ipmitool sensor data from %s: %s", targetName(target.host), err)
		return 0, err
	}
	results, err := splitSensorOutput(output)
	if err != nil {
		log.Errorf("Failed to parse ipmitool sensor data from %s: %s", targetName(target.host), err)
		return 0, err
	}
	for _, data := range results {
		var state float64

		switch data.State {
		case "ok":
			state = 0
		case "cr":
			state = 1
		case "nr":
			state = 2
		case "nc":
			state = 3
		case "ns":
			state = 4
		case "0x0000":
			state = 0
		case "0x0100":
			state = 1
		case "na":
			state = math.NaN()
		default:
			log.Errorf("Unknown sensor state: '%s'\n", data.State)
			state = math.NaN()
		}

		switch data.Type {
		case "RPM":
			collectTypedSensor(ch, fanSpeedDesc, fanSpeedStateDesc, state, data)
		case "degrees C":
			collectTypedSensor(ch, temperatureDesc, temperatureStateDesc, state, data)
		case "Ampers":
			collectTypedSensor(ch, currentDesc, currentStateDesc, state, data)
		case "Volts":
			collectTypedSensor(ch, voltageDesc, voltageStateDesc, state, data)
		case "Watts":
			collectTypedSensor(ch, powerDesc, powerStateDesc, state, data)
		case "discrete":
			if res, err := regexp.MatchString("ChassisIntru", data.Name); res {
				if err != nil {
					collectTypedSensor(ch, chassisIntrusionDesc, chassisIntrusionStateDesc, state, data)
				}
			} else if res, err := regexp.MatchString(`PS\dStatus*`, data.Name); res {
				if err != nil {
					collectTypedSensor(ch, chassisPowerDeviceDesc, chassisPowerDeviceStateDesc, state, data)
				}
			}
		default:
			collectGenericSensor(ch, state, data)
		}
	}
	return 1, nil
}

func collectFRUMonitoring(ch chan<- prometheus.Metric, target ipmiTarget) (int, error) {
	output, err := ipmitoolOutput(target, "fru")
	if err != nil {
		log.Debugf("Failed to collect ipmitool fru data from %s: %s", targetName(target.host), err)
		return 0, err
	}
	results, err := splitFruOutput(output)
	if err != nil {
		log.Errorf("Failed to parse ipmitool fru data from %s: %s", targetName(target.host), err)
		return 0, err
	}

	for _, data := range results {
		ch <- prometheus.MustNewConstMetric(
			fruInfo,
			prometheus.GaugeValue,
			1,
			data.Name, data.Value,
		)
	}
	return 1, nil
}

func collectBmcInfo(ch chan<- prometheus.Metric, target ipmiTarget) (int, error) {
	output, _ := ipmitoolOutput(target, "fwum")
	// Then fwum collector will work without exit code 1 -- uncomment this error check:
	// if err != nil {
	// 	log.Debugf("Failed to collect ipmtool fwum data from %s: %s", targetName(target.host), err)
	// 	return 0, err
	// }
	results, err := splitFwumOutput(output)
	if err != nil {
		log.Errorf("Failed to collect ipmtool fwum data from %s: %s", targetName(target.host), err)
		return 0, err
	}

	var firmwareRevision, manufacturerID string

	for _, data := range results {
		switch data.Name {
		case "FirmwareRevision":
			firmwareRevision = strconv.FormatFloat(data.Value, 'f', 6, 64)
		case "ManufacturerId":
			manufacturerID = strconv.FormatFloat(data.Value, 'f', 6, 64)
		}
	}
	ch <- prometheus.MustNewConstMetric(
		bmcInfo,
		prometheus.GaugeValue,
		1,
		firmwareRevision, manufacturerID,
	)
	return 1, nil
}

func collectPowerState(ch chan<- prometheus.Metric, target ipmiTarget) (int, error) {
	output, err := ipmitoolOutput(target, "power")
	if err != nil {
		log.Debugf("Failed to collect ipmtool power data from %s: %s", targetName(target.host), err)
		return 0, err
	}
	result, err := getChassisPowerState(output)
	if err != nil {
		log.Errorf("Failed to collect ipmtool power data from %s: %s", targetName(target.host), err)
		return 0, err
	}
	ch <- prometheus.MustNewConstMetric(
		chassisPowerStateDesc,
		prometheus.GaugeValue,
		float64(result),
		"power_state",
	)
	return 1, nil
}

func markCollectorUp(ch chan<- prometheus.Metric, name string, up int) {
	ch <- prometheus.MustNewConstMetric(
		upDesc,
		prometheus.GaugeValue,
		float64(up),
		name,
	)
}

// Collect implements Prometheus.Collector.
func (c collector) Collect(ch chan<- prometheus.Metric) {
	start := time.Now()
	defer func() {
		duration := time.Since(start).Seconds()
		log.Debugf("Scrape of target %s took %f seconds.", targetName(c.target), duration)
		ch <- prometheus.MustNewConstMetric(
			durationDesc,
			prometheus.GaugeValue,
			duration,
		)
	}()

	config := c.config.ConfigForTarget(c.target, c.module)
	target := ipmiTarget{
		host:   c.target,
		config: config,
	}

	for _, collector := range config.Collectors {
		var up int
		log.Debugf("Running collector: %s", collector)
		switch collector {
		case "sensor":
			up, _ = collectSensorMonitoring(ch, target)
		case "fru":
			up, _ = collectFRUMonitoring(ch, target)
		case "fwum":
			up, _ = collectBmcInfo(ch, target)
		}
		markCollectorUp(ch, collector, up)
	}
	collectPowerState(ch, target)
}

func contains(s []int64, elm int64) bool {
	for _, a := range s {
		if a == elm {
			return true
		}
	}
	return false
}

func escapePassword(password string) string {
	return strings.Replace(password, "#", "\\#", -1)
}

func targetName(target string) string {
	if targetIsLocal(target) {
		return "[local]"
	}
	return target
}

func targetIsLocal(target string) bool {
	return target == targetLocal
}
