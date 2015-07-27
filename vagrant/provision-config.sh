#!/bin/bash
set -e

# Passed as arguments to provisioning from Vagrantfile
MASTER_IP=$1
NUM_NODES=$2
NODE_IPS=$3

INSTANCE_PREFIX=openshift
MASTER_NAME="${INSTANCE_PREFIX}-master"
MASTER_TAG="${INSTANCE_PREFIX}-master"
NODE_TAG="${INSTANCE_PREFIX}-node"
NODE_NAMES=($(eval echo ${INSTANCE_PREFIX}-node-{1..${NUM_NODES}}))
NODE_IP_RANGES=($(eval echo "10.245.{2..${NUM_NODES}}.2/24"))
NODE_SCOPES=""

MASTER_USER=vagrant
MASTER_PASSWD=vagrant
