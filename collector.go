package main

import (
	"bufio"
	"bytes"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
)

const (
	namespace   = "ipmi"
	targetLocal = ""
)

var (
	fruBoardDateRegex     = regexp.MustCompile(`\sBoard\sMfg\sDate\s*:\s*(?P<value>.*)`)
	ipmiCurrentPowerRegex = regexp.MustCompile(`^Chassis\s*Power\s*is\s*(?P<value>on|off*)`)
	ipSourceRegex         = regexp.MustCompile(`^IP\sAddress\sSource\s*:\s*(?P<value>.*)`)
	macAddressRegex       = regexp.MustCompile(`^MAC\sAddress\s*:\s*(?P<value>.*)`)
	defaultGatewayRegex   = regexp.MustCompile(`^Default\sGateway\sIP\s*:\s*(?P<value>.*)`)
	vlanIDRegex           = regexp.MustCompile(`^802.1q\sVLAN\sID\s*:\s*(?P<value>.*)`)
	vlanPriorityRegex     = regexp.MustCompile(`^802.1q\sVLAN\sPriority\s*:\s*(?P<value>.*)`)
	subnetMaskRegex       = regexp.MustCompile(`^Subnet\sMask\s*:\s*(?P<value>.*)`)
	firmwareRevRegex      = regexp.MustCompile(`^Firmware\sRevision\s*:\s*(?P<value>.*)`)
	ipmiVersionRegex      = regexp.MustCompile(`^IPMI\sVersion\s*:\s*(?P<value>.*)`)
	manufacturerRegex     = regexp.MustCompile(`^Manufacturer\sName\s*:\s*(?P<value>.*)`)
	dcmiAvgPowerRegex     = regexp.MustCompile(`^\s*Average\spower\sreading\sover\ssample\speriod:\s*(?P<value>.*) Watts`)
	dcmiInstaPowerRegex   = regexp.MustCompile(`^\s*Instantaneous\spower\sreading:\s*(?P<value>.*) Watts`)
	dcmiMinPowerRegex     = regexp.MustCompile(`^\s*Minimum\sduring\ssampling\speriod:\s*(?P<value>.*) Watts`)
	dcmiMaxPowerRegex     = regexp.MustCompile(`^\s*Maximum\sduring\ssampling\speriod:\s*(?P<value>.*) Watts`)
)

type fruData struct {
	Name  string
	Value string
}

type lanData struct {
	Name  string
	Value string
}

type sensorData struct {
	Name  string
	Value float64
	Type  string
	State string
}

type dcmiPowerData struct {
	Name  string
	Value float64
}

type fwumData struct {
	Name  string
	Value float64
}

type bmcData struct {
	Name  string
	Value string
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
		"Chassis Power Supply device status (0=missing, 1=present).",
		[]string{"name"},
		nil,
	)

	chassisPowerDeviceStateDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "chassis_power_dev", "state"),
		"Reported state of a Power Supply (0=missing, 1=present).",
		[]string{"name"},
		nil,
	)

	chassisPowerStateDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "power", "state"),
		"Reported Chassis Power State (0=off, 1=on).",
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
		prometheus.BuildFQName(namespace, "sensor_power", "state"),
		"Reported state of a power sensor (1=ok, 0=critical).",
		[]string{"name"},
		nil,
	)

	powerConsumptionDesc = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "dcmi", "power_consumption_watts"),
		"Current power consumption in Watts.",
		[]string{"name"},
		nil,
	)

	fwumInfo = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "fwum", "info"),
		"Constant metric with value '1' providing details about the BMC.",
		[]string{"firmware_revision", "manufacturer_id"},
		nil,
	)

	bmcInfo = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "bmc", "info"),
		"Constant metric with value '1' providing details about the BMC.",
		[]string{"name", "value"},
		nil,
	)

	fruInfo = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "fru", "info"),
		"Constant metric with value '1' providing details from FRU.",
		[]string{"name", "value"},
		nil,
	)

	lanInfo = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "lan", "info"),
		"Constant metric with value '1' providing details from LAN.",
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
	if config.Interface != "" {
		args = append(args, "-I", config.Interface)
	}
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
		args = append(args, "-N", strconv.FormatInt(config.Timeout, 10))
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
	case "bmc":
		cmdCommand = append(cmdCommand, "bmc", "info")
	case "lan":
		cmdCommand = append(cmdCommand, "lan", "print")
	case "dcmi-power":
		cmdCommand = append(cmdCommand, "dcmi", "power", "reading", "1_min")
	default:
		log.Errorf("Unknown ipmitool command: '%s'\n", command)
		cmdCommand = append(cmdCommand, "")
	}

	if target.host != "" {
		cmdConfig = append(cmdConfig, "-H", target.host)
	}
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
			log.Errorf("Error while calling %s for %s: %s", command, targetName(target.host), cmd)
			//log.Fatal(err)
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

func splitDcmiPowerOutput(impitoolOutput string) ([]dcmiPowerData, error) {
	var result []dcmiPowerData

	scanner := bufio.NewScanner(strings.NewReader(impitoolOutput))

	var err error

	for scanner.Scan() {
		var data dcmiPowerData
		line := scanner.Text()
		if len(line) > 0 {
			dcmiAvgPower := dcmiAvgPowerRegex.FindStringSubmatch(line)
			if dcmiAvgPower != nil {
				for i, name := range dcmiAvgPowerRegex.SubexpNames() {
					if name != "value" {
						continue
					}
					data.Name = "Avg power consumption"
					data.Value, err = strconv.ParseFloat(dcmiAvgPower[i], 64)
					if err != nil {
						continue
					}
					result = append(result, data)
				}
			}
			dcmiMinPower := dcmiMinPowerRegex.FindStringSubmatch(line)
			if dcmiMinPower != nil {
				for i, name := range dcmiMinPowerRegex.SubexpNames() {
					if name != "value" {
						continue
					}
					data.Name = "Min power consumption"
					data.Value, err = strconv.ParseFloat(dcmiMinPower[i], 64)
					if err != nil {
						continue
					}
					result = append(result, data)
				}
			}
			dcmiMaxPower := dcmiMaxPowerRegex.FindStringSubmatch(line)
			if dcmiMaxPower != nil {
				for i, name := range dcmiMaxPowerRegex.SubexpNames() {
					if name != "value" {
						continue
					}
					data.Name = "Max power consumption"
					data.Value, err = strconv.ParseFloat(dcmiMaxPower[i], 64)
					if err != nil {
						continue
					}
					result = append(result, data)
				}
			}
			dcmiInstaPower := dcmiInstaPowerRegex.FindStringSubmatch(line)
			if dcmiInstaPower != nil {
				for i, name := range dcmiInstaPowerRegex.SubexpNames() {
					if name != "value" {
						continue
					}
					data.Name = "Instantaneous power consumption"
					data.Value, err = strconv.ParseFloat(dcmiInstaPower[i], 64)
					if err != nil {
						continue
					}
					result = append(result, data)
				}
			}
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

func splitBmcOutput(impitoolOutput string) ([]bmcData, error) {
	var result []bmcData

	scanner := bufio.NewScanner(strings.NewReader(impitoolOutput))

	var err error

	for scanner.Scan() {
		var data bmcData
		line := scanner.Text()
		if len(line) > 0 {
			firmwareRev := firmwareRevRegex.FindStringSubmatch(line)
			if firmwareRev != nil {
				for i, name := range firmwareRevRegex.SubexpNames() {
					if name != "value" {
						continue
					}
					data.Name = "FirmwareRevision"
					data.Value = firmwareRev[i]
					result = append(result, data)
					break
				}
				continue
			}
			ipmiVersion := ipmiVersionRegex.FindStringSubmatch(line)
			if ipmiVersion != nil {
				for i, name := range ipmiVersionRegex.SubexpNames() {
					if name != "value" {
						continue
					}
					data.Name = "IPMIVersion"
					data.Value = ipmiVersion[i]
					result = append(result, data)
					break
				}
				continue
			}
			manufacturer := manufacturerRegex.FindStringSubmatch(line)
			if manufacturer != nil {
				for i, name := range manufacturerRegex.SubexpNames() {
					if name != "value" {
						continue
					}
					data.Name = "Manufacturer"
					data.Value = manufacturer[i]
					result = append(result, data)
					break
				}
				break
			}
		}
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
			boardDate := fruBoardDateRegex.FindStringSubmatch(line)
			if boardDate != nil {
				for i, name := range fruBoardDateRegex.SubexpNames() {
					if name != "value" {
						continue
					}
					data.Name = "BoardMfgDate"
					data.Value = boardDate[i]
					result = append(result, data)
					break
				}
				continue
			}
			trimmedL := strings.ReplaceAll(line, " ", "")
			splittedL := strings.Split(trimmedL, ":")
			data.Name = splittedL[0]
			data.Value = splittedL[1]
			result = append(result, data)
		}
	}
	return result, err
}

func splitLANOutput(impitoolOutput string) ([]lanData, error) {
	var result []lanData

	scanner := bufio.NewScanner(strings.NewReader(impitoolOutput))

	var err error
	for scanner.Scan() {
		var data lanData
		line := scanner.Text()
		if len(line) > 0 {
			ipSource := ipSourceRegex.FindStringSubmatch(line)
			if ipSource != nil {
				for i, name := range ipSourceRegex.SubexpNames() {
					if name != "value" {
						continue
					}
					data.Name = "IPSource"
					data.Value = strings.ReplaceAll(ipSource[i], " ", "")
					result = append(result, data)
					break
				}
				continue
			}
			subnetMask := subnetMaskRegex.FindStringSubmatch(line)
			if subnetMask != nil {
				for i, name := range subnetMaskRegex.SubexpNames() {
					if name != "value" {
						continue
					}
					data.Name = "SubnetMask"
					data.Value = subnetMask[i]
					result = append(result, data)
					break
				}
				continue
			}
			macMatch := macAddressRegex.FindStringSubmatch(line)
			if macMatch != nil {
				for i, name := range macAddressRegex.SubexpNames() {
					if name != "value" {
						continue
					}
					data.Name = "MACAddress"
					data.Value = macMatch[i]
					result = append(result, data)
					break
				}
				continue
			}
			defGateway := defaultGatewayRegex.FindStringSubmatch(line)
			if defGateway != nil {
				for i, name := range defaultGatewayRegex.SubexpNames() {
					if name != "value" {
						continue
					}
					data.Name = "DefaultGateway"
					data.Value = defGateway[i]
					result = append(result, data)
					break
				}
				continue
			}
			vlanID := vlanIDRegex.FindStringSubmatch(line)
			if vlanID != nil {
				for i, name := range vlanIDRegex.SubexpNames() {
					if name != "value" {
						continue
					}
					data.Name = "VLANID"
					data.Value = vlanID[i]
					result = append(result, data)
					break
				}
				continue
			}
			vlanPriority := vlanPriorityRegex.FindStringSubmatch(line)
			if vlanPriority != nil {
				for i, name := range vlanPriorityRegex.SubexpNames() {
					if name != "value" {
						continue
					}
					data.Name = "VLANPriority"
					data.Value = vlanPriority[i]
					result = append(result, data)
					break
				}
				break
			}
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
				return 1, err
			}
		}
	}
	return 0, err
}

// Describe implements Prometheus.Collector.
func (c collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- sensorStateDesc
	ch <- sensorValueDesc
	ch <- fanSpeedDesc
	ch <- temperatureDesc
	ch <- powerConsumptionDesc
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
					// TODO log error
					collectTypedSensor(ch, chassisIntrusionDesc, chassisIntrusionStateDesc, state, data)
				} else {
					collectTypedSensor(ch, chassisIntrusionDesc, chassisIntrusionStateDesc, state, data)
				}
			} else if res, err := regexp.MatchString(`PS\dStatus*`, data.Name); res {
				if err != nil {
					// TODO log error
					collectTypedSensor(ch, chassisPowerDeviceDesc, chassisPowerDeviceStateDesc, state, data)
				} else {
					collectTypedSensor(ch, chassisPowerDeviceDesc, chassisPowerDeviceStateDesc, state, data)
				}
			}
		default:
			collectGenericSensor(ch, state, data)
		}
	}
	return 1, nil
}

func collectFRUInfo(ch chan<- prometheus.Metric, target ipmiTarget) (int, error) {
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

func collectLANInfo(ch chan<- prometheus.Metric, target ipmiTarget) (int, error) {
	output, err := ipmitoolOutput(target, "lan")
	if err != nil {
		log.Debugf("Failed to collect ipmitool lan data from %s: %s", targetName(target.host), err)
		return 0, err
	}
	results, err := splitLANOutput(output)
	if err != nil {
		log.Errorf("Failed to parse ipmitool lan data from %s: %s", targetName(target.host), err)
		return 0, err
	}

	for _, data := range results {
		ch <- prometheus.MustNewConstMetric(
			lanInfo,
			prometheus.GaugeValue,
			1,
			data.Name, data.Value,
		)
	}
	return 1, nil
}

func collectBmcInfo(ch chan<- prometheus.Metric, target ipmiTarget) (int, error) {
	output, err := ipmitoolOutput(target, "bmc")
	if err != nil {
		log.Debugf("Failed to collect ipmtool bmc data from %s: %s", targetName(target.host), err)
		return 0, err
	}
	results, err := splitBmcOutput(output)
	if err != nil {
		log.Errorf("Failed to collect ipmtool bmc data from %s: %s", targetName(target.host), err)
		return 0, err
	}

	for _, data := range results {
		ch <- prometheus.MustNewConstMetric(
			bmcInfo,
			prometheus.GaugeValue,
			1,
			data.Name, data.Value,
		)
	}
	return 1, nil
}

func collectDcmiPowerInfo(ch chan<- prometheus.Metric, target ipmiTarget) (int, error) {
	output, err := ipmitoolOutput(target, "dcmi-power")
	if err != nil {
		log.Debugf("Failed to collect ipmtool dcmi power data from %s: %s", targetName(target.host), err)
		return 0, err
	}
	results, err := splitDcmiPowerOutput(output)
	if err != nil {
		log.Errorf("Failed to collect ipmtool dcmi power data from %s: %s", targetName(target.host), err)
		return 0, err
	}

	for _, data := range results {
		ch <- prometheus.MustNewConstMetric(
			powerConsumptionDesc,
			prometheus.GaugeValue,
			data.Value,
			data.Name,
		)
	}
	return 1, nil
}

func collectFwumInfo(ch chan<- prometheus.Metric, target ipmiTarget) (int, error) {
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
		fwumInfo,
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
		"PowerState",
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
			up, _ = collectFRUInfo(ch, target)
		case "lan":
			up, _ = collectLANInfo(ch, target)
		case "bmc":
			up, _ = collectBmcInfo(ch, target)
		case "fwum":
			up, _ = collectFwumInfo(ch, target)
		case "dcmi-power":
			up, _ = collectDcmiPowerInfo(ch, target)
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
