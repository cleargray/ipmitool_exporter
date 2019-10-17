# ipmitool_exporter
Prometheus exporter for ipmitool util

Prometheus IPMI Exporter
========================

This is an IPMI exporter for [Prometheus](https://prometheus.io).

It supports both the regular `/metrics` endpoint, exposing metrics from the
host that the exporter is running on, as well as an `/ipmi` endpoint that
supports IPMI over RMCP - one exporter running on one host can be used to
monitor a large number of IPMI interfaces by passing the `target` parameter to
a scrape.

The exporter relies on tools from the
[ipmitool](https://github.com/ipmitool/ipmitool) suite for the actual IPMI
implementation.

## Installation

You need a Go development environment. Then, run the following to get the
source code and build and install the binary:

    go get github.com/cleargray/ipmitool_exporter

## Running

A minimal invocation looks like this:

    ./ipmitool_exporter

Supported parameters include:

 - `web.listen-address`: the address/port to listen on (default: `":9104"`)
 - `config.file`: path to the configuration file (default: none)
 - `ipmitool.path`: path to the FreeIPMI executables (default: rely on `$PATH`)

For syntax and a complete list of available parameters, run:

    ./ipmi_exporter -h

Make sure you have the ipmitool util installed

## Configuration

Simply scraping the standard `/metrics` endpoint will make the exporter emit
local IPMI metrics. No special configuration is required.

For remote metrics, the general configuration pattern is similar to that of the
[blackbox exporter](https://github.com/prometheus/blackbox_exporter), i.e.
Prometheus scrapes a small number (possibly one) of IPMI exporters with a
`target` and `module` URL parameter to tell the exporter which IPMI device it
should use to retrieve the IPMI metrics. We offer this approach as IPMI devices
often provide useful information even while the supervised host is turned off.
If you are running the exporter on a separate host anyway, it makes more sense
to have only a few of them, each probing many (possibly thousands of) IPMI
devices, rather than one exporter per IPMI device.

### IPMItool exporter

The exporter can read a configuration file by setting `config.file` (see
above). To collect local metrics, you might not even need one. For
remote metrics, it must contain at least user names and passwords for IPMI
access to all targets to be scraped. You can additionally specify the 
and privilege level to use.

The config file supports the notion of "modules", so that different
configurations can be re-used for groups of targets. See the section below on
how to set the module parameter in Prometheus. The special module "default" is
used in case the scrape does not request a specific module.

The configuration file also supports a blacklist of sensors, useful in case of
OEM-specific sensors that FreeIPMI cannot deal with properly or otherwise
misbehaving sensors. This applies to both local and remote metrics.

There are two commented example configuration files, see `ipmi_local.yml` for
scraping local host metrics and `ipmi_remote.yml` for scraping remote IPMI
interfaces.

### Prometheus

#### Local metrics

Collecting local IPMI metrics is fairly straightforward. Simply configure your
server to scrape the default metrics endpoint on the hosts running the
exporter.

```
- job_name: ipmi
  scrape_interval: 1m
  scrape_timeout: 30s
  metrics_path: /metrics
  scheme: http
  static_configs:
  - targets:
    - 10.1.2.23:9290
    - 10.1.2.24:9290
    - 10.1.2.25:9290
```

#### Remote metrics

To add your IPMI targets to Prometheus, you can use any of the supported
service discovery mechanism of your choice. The following example uses the
file-based SD and should be easy to adjust to other scenarios.

Create a YAML file that contains a list of targets, e.g.:

```
---
- targets:
  - 10.1.2.23
  - 10.1.2.24
  - 10.1.2.25
  - 10.1.2.26
  - 10.1.2.27
  - 10.1.2.28
  - 10.1.2.29
  - 10.1.2.30
  labels:
    job: ipmi_exporter
```

This file needs to be stored on the Prometheus server host.  Assuming that this
file is called `/srv/ipmitool_exporter/targets.yml`, and the IPMI exporter is
running on a host that has the DNS name `ipmitool-exporter.internal.example.com`,
add the following to your Prometheus config:

```
- job_name: ipmi
  params:
    module: default
  scrape_interval: 1m
  scrape_timeout: 30s
  metrics_path: /ipmi
  scheme: http
  file_sd_configs:
  - files:
    - /srv/ipmitool_exporter/targets.yml
    refresh_interval: 5m
  relabel_configs:
  - source_labels: [__address__]
    separator: ;
    regex: (.*)
    target_label: __param_target
    replacement: ${1}
    action: replace
  - source_labels: [__param_target]
    separator: ;
    regex: (.*)
    target_label: instance
    replacement: ${1}
    action: replace
  - separator: ;
    regex: .*
    target_label: __address__
    replacement: ipmitool-exporter.internal.example.com:9290
    action: replace
```

This assumes that all hosts use the default module. If you are using modules in
the config file, like in the provided `ipmi_remote.yml` example config, you
will need to specify on job for each module, using the respective group of
targets.

In a more extreme case, for example if you are using different passwords on
every host, a good approach is to generate an exporter config file that uses
the target name as module names, which would allow you to have single job that
uses label replace to set the module. Leave out the `params` in the job
definition and instead add a relabel rule like this one:

```
  - source_labels: [__address__]
    separator: ;
    regex: (.*)
    target_label: __param_module
    replacement: ${1}
    action: replace
```

For more information, e.g. how to use mechanisms other than a file to discover
the list of hosts to scrape, please refer to the [Prometheus
documentation](https://prometheus.io/docs).

## Exported data

### Scrape meta data

These metrics provide data about the scrape itself:

 - `ipmi_up{collector="<NAME>"}` is `1` if the data for this collector could
   successfully be retrieved from the remote host, `0` otherwise. The following
   collectors are available and can be enabled or disabled in the config:
   - `sensor`: collects IPMI sensor data. If it fails, sensor metrics (see below)
     will not be available
   - `fwum`: collects Firmware data. If it fails, metrics will not be available
   - `fru`: collects BMC details. If if fails, BMC info metrics (see below)
     will not be available
 - `ipmi_scrape_duration_seconds` is the amount of time it took to retrieve the
   data
