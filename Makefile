# Override the default common all.
.PHONY: all
all: precheck style unused build test

include Makefile.common

# DOCKER_IMAGE_NAME ?= ipmitool-exporter
# DOCKER_REPO       ?= nexus-nid.stc/
