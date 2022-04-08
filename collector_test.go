package main

import (
	"math"
	"strings"
	"testing"
)

var (
	collTestConfig   string
	collSafeConfTest = &SafeConfig{
		C: &Config{},
	}
)

func TestIpmitoolConfig(t *testing.T) {
	collTestConfig := "./ipmi_remote.yml"
	collSafeConfTest.ReloadConfig(collTestConfig)
	collTarget := "localhost"
	collModule := "example"
	config := collSafeConfTest.ConfigForTarget(collTarget, collModule)
	res := ipmitoolConfig(config)
	resString := strings.Join(res, " ")
	expect := "-L administrator -U example_user -P example_pass -N 5"
	if resString != expect {
		t.Errorf("Wrong config line '%s' generatet for module '%s'", resString, collModule)
	}
}

func TestSplitSensorOutput(t *testing.T) {
	collSensorOutput := `CPU1 Temp        | 31.000     | degrees C  | ok    | 0.000     | 0.000     | 0.000     | 90.000    | 95.000    | 95.000
P1-DIMMA2 Temp   | na         |            | na    | na        | na        | na        | na        | na        | na
Chassis Intru    | 0x0        | discrete   | 0x0000| na        | na        | na        | na        | na        | na`
	res, err := splitSensorOutput(collSensorOutput)
	expectName := "CPU1Temp"
	expectValue := float64(0)
	if err != nil {
		t.Errorf("splitSensorOutput() call failed. Reason: %s", err)
	}
	if res[0].Name != expectName {
		t.Errorf("Whitespace sanitizin failed.\n Expect: %s\n Got: %s", expectName, res[0].Name)
	}
	if !math.IsNaN(res[1].Value) {
		t.Errorf("NaN conversion failed.\n Value: %f is not math.NaN", res[1].Value)
	}
	if res[2].Value != expectValue {
		t.Errorf("HEX to float64 conversion failed.\n Expect: %f\n Got: %f", expectValue, res[2].Value)
	}
}

func TestSplitFwumOutput(t *testing.T) {
	collFwumOutput := `FWUM extension Version 1.3

IPMC Info
=========
Manufacturer Id           : 10876
Board Id                  : 2130
Firmware Revision         : 3.76`
	res, err := splitFwumOutput(collFwumOutput)
	expectManID := float64(10876)
	expectFWVer := 3.76
	if err != nil {
		t.Errorf("splitFwumOutput() call failed. Reason: %s", err)
	}
	if res[4].Name != "ManufacturerId" && res[4].Value != expectManID {
		t.Errorf("Manufacturer Id check failed.\n Expect:\n value: %f\n Got:\n value: %f", expectManID, res[4].Value)
	}
	if res[6].Name != "FirmwareRevision" && res[6].Value != expectFWVer {
		t.Errorf("Firmware Revision check failed.\n Expect:\n value: %f\n Got:\n value: %f", expectFWVer, res[6].Value)
	}
}

func TestSplitFruOutput(t *testing.T) {
	collFruOutput := `FRU Device Description : Builtin FRU Device (ID 0)
Chassis Type          : Other
Chassis Part Number   : CSE-747BTS-R2K04BP
Chassis Serial        : C7470KH08MS0040
Board Mfg Date        : Mon Jan  1 03:00:00 1996
Board Mfg             : Supermicro
Board Serial          : VM187S012298
Board Part Number     : X10DRG-Q
Product Manufacturer  : Supermicro
Product Part Number   : SYS-7048GR-TR
Product Serial        : E16953528901097`
	res, err := splitFruOutput(collFruOutput)
	expectProductPN := "SYS-7048GR-TR"
	expectProductMfg := "Supermicro"
	if err != nil {
		t.Errorf("splitFruOutput() call failed. Reason: %s", err)
	}
	if res[9].Name != "ProductPartNumber" && res[9].Value != expectProductPN {
		t.Errorf("Product Part Number check failed.\n Expect:\n value: %s\n Got:\n value: %s", expectProductPN, res[9].Value)
	}
	if res[5].Name != "FirmwareRevision" && res[5].Value != expectProductMfg {
		t.Errorf("Board Mfg check failed.\n Expect:\n value: %s\n Got:\n value: %s", expectProductMfg, res[5].Value)
	}
}

func TestGetChassisPowerState(t *testing.T) {
	collChassisOutput := `Chassis Power is off`
	res, err := getChassisPowerState(collChassisOutput)
	expect := 0
	if err != nil {
		t.Errorf("getChassisPowerState() call failed. Reason: %s", err)
	}
	if res != expect {
		t.Errorf("Chassis power state check failed.\n Expect:\n value: %v\n Got:\n value: %v", expect, res)
	}
}
