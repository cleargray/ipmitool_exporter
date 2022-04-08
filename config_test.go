package main

import (
	"testing"
)

var (
	testConfig     string
	testBadConfig  string
	testGoodConfig string
	safeConfTest   = &SafeConfig{
		C: &Config{},
	}
)

func TestGoodReloadConfig(t *testing.T) {
	testGoodConfig := "./ipmi_remote.yml"
	res := safeConfTest.ReloadConfig(testGoodConfig)
	if res != nil {
		t.Errorf("Config file %s not loaded.\n Error is: %s", testGoodConfig, res)
	}
}

func TestBadReloadConfig(t *testing.T) {
	testBadConfig := "./test_config.yml"
	res := safeConfTest.ReloadConfig(testBadConfig)
	if res == nil {
		t.Errorf("Bad config file %s was loaded.\n", testBadConfig)
	}
}

func TestHasModule(t *testing.T) {
	testGoodConfig := "./ipmi_remote.yml"
	safeConfTest.ReloadConfig(testGoodConfig)
	module := "example"
	res := safeConfTest.HasModule(module)
	if res != true {
		t.Errorf("Existing '%s' module check failed.\n", module)
	}
	module = "example1"
	res = safeConfTest.HasModule(module)
	if res == true {
		t.Errorf("Non-existing '%s' module check failed.\n", module)
	}
}

func TestConfigForTarget(t *testing.T) {
	testGoodConfig := "./ipmi_remote.yml"
	safeConfTest.ReloadConfig(testGoodConfig)
	target := "localhost"
	module := "example"
	res := safeConfTest.ConfigForTarget(target, module)
	if res.User != "example_user" && res.Password != "example_pass" {
		t.Errorf("Wrong module '%s' loaded for target '%s'", module, target)
	}

	target = "localhost"
	module = "example1"
	res = safeConfTest.ConfigForTarget(target, module)
	if res.User != "default_user" && res.Password != "default_pass" {
		t.Errorf("Default module not loaded instead of non-existing module '%s'", module)
	}
}
